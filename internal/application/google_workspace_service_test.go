package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/repo/connection/memory"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type workspaceTestRepo struct {
	repo.Repository
	rows map[string]*repo.Tenant
}

func (r *workspaceTestRepo) List(_ context.Context, u, c string) ([]*repo.Tenant, error) {
	out := []*repo.Tenant{}
	for _, t := range r.rows {
		if t.UserID == u && t.ConnectionID == c {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *workspaceTestRepo) Get(_ context.Context, u, c, id string) (*repo.Tenant, error) {
	t := r.rows[id]
	if t == nil || t.UserID != u || t.ConnectionID != c {
		return nil, repo.ErrNotFound
	}
	cp := *t
	return &cp, nil
}
func (r *workspaceTestRepo) Create(_ context.Context, t *repo.Tenant) error {
	for _, old := range r.rows {
		if old.ConnectionID == t.ConnectionID && old.CustomerID == t.CustomerID {
			return repo.ErrDuplicate
		}
	}
	cp := *t
	r.rows[t.ID] = &cp
	return nil
}
func (r *workspaceTestRepo) Update(_ context.Context, t *repo.Tenant) error {
	cp := *t
	r.rows[t.ID] = &cp
	return nil
}
func (r *workspaceTestRepo) SetStatus(_ context.Context, t *repo.Tenant) error {
	old := r.rows[t.ID]
	old.Status, old.StatusMessage, old.StatusCheckedAt = t.Status, t.StatusMessage, t.StatusCheckedAt
	return nil
}
func (r *workspaceTestRepo) Delete(_ context.Context, u, c, id string) error {
	t := r.rows[id]
	if t == nil || t.UserID != u || t.ConnectionID != c {
		return repo.ErrNotFound
	}
	delete(r.rows, id)
	return nil
}

type workspaceTestProvider struct{}

func (workspaceTestProvider) Type() string { return googleworkspace.ProviderType }
func (workspaceTestProvider) Verify(context.Context, json.RawMessage, json.RawMessage) error {
	return nil
}
func (workspaceTestProvider) Cleanup(context.Context, *connrepo.Connection) error { return nil }

func TestWorkspaceSharedKeyAndIndependentTenants(t *testing.T) {
	ctx := asUser("owner")
	store := memory.New()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	conns := connection.NewService(store, cipher)
	conns.Register(workspaceTestProvider{})
	shared, err := conns.Create(ctx, "owner", googleworkspace.ProviderType, "Shared", googleworkspace.ConnectionConfig{ServiceAccountEmail: "shared@example.iam.gserviceaccount.com", ClientID: "123"}, googleworkspace.ConnectionSecret{ServiceAccountJSON: "SHARED-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	rows := &workspaceTestRepo{rows: map[string]*repo.Tenant{}}
	blockedA := false
	writes := 0
	suspended := false
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		admin := r.Header.Get("X-Delegated-Admin")
		customer := "Ca"
		if admin == "admin@b.example" {
			customer = "Cb"
		}
		if blockedA && customer == "Ca" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}]}}`)
			return
		}
		if r.URL.Path == "/users" && r.Method == http.MethodGet {
			if r.URL.Query().Get("customer") != customer {
				t.Errorf("delegated token used for wrong tenant")
			}
			fmt.Fprintf(w, `{"users":[{"id":"user-%s","customerId":"%s"}]}`, customer, customer)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/customer/") {
			fmt.Fprint(w, `{"domains":[{"domainName":"a.example","verified":true}]}`)
			return
		}
		if r.Method != http.MethodGet {
			writes++
			if r.Method == http.MethodPatch {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if v, ok := body["suspended"].(bool); ok {
					suspended = v
				}
			}
		}
		// Cross-tenant ID always resolves to tenant B, regardless of credentials.
		if r.URL.Path == "/users/user-Cb" {
			customer = "Cb"
		}
		fmt.Fprintf(w, `{"id":"user-%s","customerId":"%s","primaryEmail":"user@a.example","name":{"givenName":"Ada","familyName":"L"},"suspended":%t}`, customer, customer, suspended)
	}))
	defer fake.Close()
	srv := NewGoogleWorkspaceServiceServer()
	srv.SetDeps(rows, conns)
	opened := 0
	srv.newClient = func(_ context.Context, cfg googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, admin string) (workspaceAPI, error) {
		opened++
		if sec.ServiceAccountJSON != "SHARED-SECRET" || cfg.ClientID != "123" {
			t.Fatal("tenants did not reuse the same key")
		}
		return &googleworkspace.Client{HTTP: &http.Client{Transport: workspaceAdminTransport{base: fake.Client().Transport, admin: admin}}, Endpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	add := func(name, customer, admin string) *neoboxv1.WorkspaceTenant {
		t.Helper()
		res, err := srv.CreateWorkspaceTenant(ctx, connect.NewRequest(&neoboxv1.CreateWorkspaceTenantRequest{ConnectionId: shared.ID, Name: name, CustomerId: customer, DelegatedAdminEmail: admin}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.Tenant
	}
	a := add("A", "Ca", "admin@a.example")
	b := add("B", "Cb", "admin@b.example")
	if a.Id == b.Id {
		t.Fatal("independent tenants have same ID")
	}
	list, err := srv.ListWorkspaceTenants(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceTenantsRequest{ConnectionId: shared.ID}))
	if err != nil || len(list.Msg.Tenants) != 2 {
		t.Fatalf("list=%v %v", list, err)
	}
	raw, _ := protojson.Marshal(list.Msg)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("tenant response exposes key")
	}
	before := opened
	if _, err = srv.ListWorkspaceTenants(asUser("other"), connect.NewRequest(&neoboxv1.ListWorkspaceTenantsRequest{ConnectionId: shared.ID})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other owner=%v", err)
	}
	if _, err = srv.ListWorkspaceUsers(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUsersRequest{ConnectionId: shared.ID, TenantId: "missing"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other tenant=%v", err)
	}
	if opened != before {
		t.Fatal("unauthorized requests opened Google client")
	}
	profile := &neoboxv1.WorkspaceUserProfile{GivenName: "Ada", FamilyName: "L", OrgUnitPath: "/"}
	if _, err = srv.UpdateWorkspaceUser(ctx, connect.NewRequest(&neoboxv1.UpdateWorkspaceUserRequest{ConnectionId: shared.ID, TenantId: a.Id, UserId: "user-Cb", Profile: profile})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross tenant update=%v", err)
	}
	if writes != 0 {
		t.Fatal("cross-tenant write sent")
	}
	if _, err = srv.CreateWorkspaceUser(ctx, connect.NewRequest(&neoboxv1.CreateWorkspaceUserRequest{ConnectionId: shared.ID, TenantId: a.Id, PrimaryEmail: "new@a.example", Profile: profile, Password: "secret-password", ChangePasswordAtNextLogin: true})); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.ResetWorkspaceUserPassword(ctx, connect.NewRequest(&neoboxv1.ResetWorkspaceUserPasswordRequest{ConnectionId: shared.ID, TenantId: a.Id, UserId: "user-Ca", Password: "another-password", ChangePasswordAtNextLogin: true})); err != nil {
		t.Fatal(err)
	}
	for _, v := range []bool{true, false} {
		res, err := srv.SetWorkspaceUserSuspended(ctx, connect.NewRequest(&neoboxv1.SetWorkspaceUserSuspendedRequest{ConnectionId: shared.ID, TenantId: a.Id, UserId: "user-Ca", Suspended: v}))
		if err != nil || res.Msg.User.Suspended != v {
			t.Fatalf("suspend=%v %v", res, err)
		}
	}
	if writes != 4 {
		t.Fatalf("writes=%d", writes)
	}
	blockedA = true
	if _, err = srv.ListWorkspaceUsers(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUsersRequest{ConnectionId: shared.ID, TenantId: a.Id})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("refusal=%v", err)
	}
	if _, err = srv.ListWorkspaceUsers(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceUsersRequest{ConnectionId: shared.ID, TenantId: b.Id})); err != nil {
		t.Fatal(err)
	}
	if rows.rows[a.Id].Status != "error" || rows.rows[b.Id].Status != "ok" {
		t.Fatal("health crossed tenants")
	}
	if _, err = srv.DeleteWorkspaceTenant(ctx, connect.NewRequest(&neoboxv1.DeleteWorkspaceTenantRequest{ConnectionId: shared.ID, TenantId: a.Id})); err != nil {
		t.Fatal(err)
	}
	if len(rows.rows) != 1 || writes != 4 {
		t.Fatal("tenant removal affected Google or another tenant")
	}
}

type workspaceAdminTransport struct {
	base  http.RoundTripper
	admin string
}

func (t workspaceAdminTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Delegated-Admin", t.admin)
	return t.base.RoundTrip(r)
}
func TestWorkspaceErrorsAndPasswords(t *testing.T) {
	for _, err := range []error{googleworkspace.ErrDelegation, &googleworkspace.APIError{Status: 401}, &googleworkspace.APIError{Status: 403}, googleworkspace.ErrUncertain} {
		if connect.CodeOf(mapWorkspaceErr(err)) == connect.CodeUnauthenticated {
			t.Fatal("provider failure signs Neo Box user out")
		}
	}
	if connect.CodeOf(mapWorkspaceErr(googleworkspace.ErrDelegation)) != connect.CodeFailedPrecondition {
		t.Fatal("wrong delegation code")
	}
	if connect.CodeOf(mapWorkspaceErr(googleworkspace.ErrUncertain)) != connect.CodeUnknown {
		t.Fatal("wrong uncertain code")
	}
	if workspacePassword("short") == nil || workspacePassword(strings.Repeat("x", 101)) == nil || workspacePassword("  valid password  ") != nil {
		t.Fatal("password validation")
	}
	if _, err := workspaceProfile(nil); err == nil {
		t.Fatal("missing profile accepted")
	}
	if _, err := workspaceProfile(&neoboxv1.WorkspaceUserProfile{GivenName: "A", FamilyName: "B", OrgUnitPath: "relative"}); err == nil {
		t.Fatal("relative OU accepted")
	}
}

func workspaceKeyJSON(t *testing.T, clientID string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"type": "service_account", "client_id": clientID, "client_email": "shared@example.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestWorkspaceConnectionKeyRotationAndSecretResponses(t *testing.T) {
	ctx := asUser("owner")
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	conns := connection.NewService(memory.New(), cipher)
	conns.Register(workspaceTestProvider{})
	srv := NewConnectionServiceServer()
	srv.SetService(conns)
	oldKey, newKey := workspaceKeyJSON(t, "123"), workspaceKeyJSON(t, "123")
	created, err := srv.CreateConnection(ctx, connect.NewRequest(&neoboxv1.CreateConnectionRequest{Name: "Shared", Settings: &neoboxv1.CreateConnectionRequest_GoogleWorkspace{GoogleWorkspace: &neoboxv1.GoogleWorkspaceConnectionSettings{ServiceAccountJson: oldKey}}}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Connection.Id
	update := func(raw string, clientID string) (*connect.Response[neoboxv1.UpdateConnectionResponse], error) {
		return srv.UpdateConnection(ctx, connect.NewRequest(&neoboxv1.UpdateConnectionRequest{Id: id, Name: "Shared", Settings: &neoboxv1.UpdateConnectionRequest_GoogleWorkspace{GoogleWorkspace: &neoboxv1.GoogleWorkspaceConnectionSettings{ServiceAccountEmail: "shared@example.iam.gserviceaccount.com", ClientId: clientID, ServiceAccountJson: raw}}}))
	}
	rotated, err := update(newKey, "123")
	if err != nil {
		t.Fatal(err)
	}
	// Leaving the key empty keeps the just-rotated key.
	if _, err = update("", "123"); err != nil {
		t.Fatal(err)
	}
	c, err := conns.Get(ctx, "owner", id)
	if err != nil {
		t.Fatal(err)
	}
	var secret googleworkspace.ConnectionSecret
	if err = conns.Open(c, nil, &secret); err != nil || secret.ServiceAccountJSON != newKey {
		t.Fatal("rotation or keep-current-key failed")
	}
	if strings.Contains(c.SecretCiphertext, "PRIVATE KEY") {
		t.Fatal("private key stored unencrypted")
	}
	if _, err = update(workspaceKeyJSON(t, "999"), "999"); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("identity change=%v", err)
	}
	for _, response := range []*neoboxv1.Connection{created.Msg.Connection, rotated.Msg.Connection} {
		raw, err := protojson.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private_key") || strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatal("response exposes private key")
		}
	}
}
