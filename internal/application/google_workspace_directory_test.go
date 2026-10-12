package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/repo/connection/memory"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type workspaceRejectTransport struct{}

func (workspaceRejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, googleworkspace.ErrDelegation
}

func TestWorkspaceFeatureScopesAndOwnershipAtRPCBoundary(t *testing.T) {
	ctx := asUser("owner")
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	conns := connection.NewService(memory.New(), cipher)
	conns.Register(workspaceTestProvider{})
	conn, err := conns.Create(ctx, "owner", googleworkspace.ProviderType, "shared", googleworkspace.ConnectionConfig{ServiceAccountEmail: "shared@example.iam.gserviceaccount.com", ClientID: "123"}, googleworkspace.ConnectionSecret{ServiceAccountJSON: "SHARED-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tenant := &repo.Tenant{ID: "t", UserID: "owner", ConnectionID: conn.ID, Name: "A", CustomerID: "CtenantA", DelegatedAdminEmail: "admin@a.example", Status: "ok", StatusCheckedAt: now}
	rows := &workspaceTestRepo{rows: map[string]*repo.Tenant{"t": tenant}}
	writes := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/users/foreign":
			fmt.Fprint(w, `{"id":"foreign","customerId":"CtenantB"}`)
		case "/users/u":
			fmt.Fprint(w, `{"id":"u","customerId":"CtenantA"}`)
		case "/users":
			if r.URL.Query().Get("customer") != "CtenantA" {
				t.Error("users not tenant scoped")
			}
			fmt.Fprint(w, `{"users":[{"id":"u","customerId":"CtenantA"}]}`)
		case "/groups":
			if r.URL.Query().Get("customer") != "CtenantA" {
				t.Error("groups not tenant scoped")
			}
			fmt.Fprint(w, `{"groups":[{"id":"own","email":"team@a.example"}]}`)
		case "/customer/CtenantA/orgunits":
			fmt.Fprint(w, `{"organizationUnits":[{"orgUnitId":"id:ou","orgUnitPath":"/Team","name":"Team"}]}`)
		case "/users/u/tokens":
			fmt.Fprint(w, `{"items":[{"clientId":"app","displayText":"Example"}]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()
	srv := NewGoogleWorkspaceServiceServer()
	srv.SetDeps(rows, conns)
	scopes := [][]string{}
	denyGroups := false
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, admin string, extra ...string) (workspaceAPI, error) {
		if sec.ServiceAccountJSON != "SHARED-SECRET" || customer != "CtenantA" || admin != "admin@a.example" {
			t.Fatal("wrong shared credential or tenant")
		}
		scopes = append(scopes, append([]string(nil), extra...))
		hc := fake.Client()
		if denyGroups && len(extra) == 1 && extra[0] == googleworkspace.GroupScope {
			hc = &http.Client{Transport: workspaceRejectTransport{}}
		}
		return &googleworkspace.Client{HTTP: hc, Endpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	if _, err = srv.ListWorkspaceGroups(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceGroupsRequest{ConnectionId: conn.ID, TenantId: "t"})); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.ListWorkspaceOrgUnits(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceOrgUnitsRequest{ConnectionId: conn.ID, TenantId: "t"})); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.ListWorkspaceUserTokens(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUserTokensRequest{ConnectionId: conn.ID, TenantId: "t", UserId: "u"})); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.ListWorkspaceUsers(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUsersRequest{ConnectionId: conn.ID, TenantId: "t"})); err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{googleworkspace.GroupScope, googleworkspace.OrgUnitScope, googleworkspace.SecurityScope, ""} {
		if expected == "" {
			if len(scopes[i]) != 0 {
				t.Fatal("optional scopes leaked into base user request")
			}
		} else if len(scopes[i]) != 1 || scopes[i][0] != expected {
			t.Fatalf("scopes[%d]=%v", i, scopes[i])
		}
	}
	before := len(scopes)
	if _, err = srv.DeleteWorkspaceGroup(asUser("other"), connect.NewRequest(&neoboxv1.DeleteWorkspaceGroupRequest{ConnectionId: conn.ID, TenantId: "t", GroupId: "own"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other owner=%v", err)
	}
	if _, err = srv.SignOutWorkspaceUser(ctx, connect.NewRequest(&neoboxv1.SignOutWorkspaceUserRequest{ConnectionId: conn.ID, TenantId: "other", UserId: "u"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other tenant=%v", err)
	}
	if len(scopes) != before {
		t.Fatal("unauthorized RPC opened client")
	}
	if _, err = srv.DeleteWorkspaceGroup(ctx, connect.NewRequest(&neoboxv1.DeleteWorkspaceGroupRequest{ConnectionId: conn.ID, TenantId: "t", GroupId: "foreign"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign group=%v", err)
	}
	if _, err = srv.SignOutWorkspaceUser(ctx, connect.NewRequest(&neoboxv1.SignOutWorkspaceUserRequest{ConnectionId: conn.ID, TenantId: "t", UserId: "foreign"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign security action=%v", err)
	}
	if writes != 0 {
		t.Fatal("unauthorized write sent")
	}
	if _, err = srv.AddWorkspaceGroupMember(ctx, connect.NewRequest(&neoboxv1.AddWorkspaceGroupMemberRequest{ConnectionId: conn.ID, TenantId: "t", GroupId: "own", Email: "u@a.example", Role: "INVALID"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("role validation=%v", err)
	}
	denyGroups = true
	originalHealth := tenant.Status
	_, err = srv.ListWorkspaceGroups(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceGroupsRequest{ConnectionId: conn.ID, TenantId: "t"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.GroupScope) {
		t.Fatalf("missing scope error=%v", err)
	}
	if tenant.Status != originalHealth {
		t.Fatal("optional permission failure changed base health")
	}
	if _, err = srv.ListWorkspaceUsers(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUsersRequest{ConnectionId: conn.ID, TenantId: "t"})); err != nil {
		t.Fatal("optional scope failure broke users", err)
	}
}
