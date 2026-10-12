package application

import (
	"context"
	"encoding/json"
	"errors"
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

func observabilityFixture(t *testing.T) (*GoogleWorkspaceServiceServer, *workspaceTestRepo, []string) {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	conns := connection.NewService(memory.New(), cipher)
	conns.Register(workspaceTestProvider{})
	ids := []string{}
	for _, name := range []string{"First Service Account", "Second Service Account"} {
		c, err := conns.Create(asUser("owner"), "owner", googleworkspace.ProviderType, name, googleworkspace.ConnectionConfig{ServiceAccountEmail: "shared@example.iam.gserviceaccount.com", ClientID: "123"}, googleworkspace.ConnectionSecret{ServiceAccountJSON: "KEY-SECRET"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	rows := &workspaceTestRepo{rows: map[string]*repo.Tenant{
		"a":         {ID: "a", UserID: "owner", ConnectionID: ids[0], CustomerID: "Ca", Name: "Tenant A", DelegatedAdminEmail: "admin@a.example", Status: "ok"},
		"b":         {ID: "b", UserID: "owner", ConnectionID: ids[0], CustomerID: "Cb", Name: "Tenant B", DelegatedAdminEmail: "admin@b.example", Status: "ok"},
		"duplicate": {ID: "duplicate", UserID: "owner", ConnectionID: ids[1], CustomerID: "Ca", Name: "Tenant A again", DelegatedAdminEmail: "other@a.example", Status: "ok"},
		"foreign":   {ID: "foreign", UserID: "other", ConnectionID: "foreign-connection", CustomerID: "Cc", Name: "Private", DelegatedAdminEmail: "admin@c.example", Status: "ok"},
	}}
	srv := NewGoogleWorkspaceServiceServer()
	srv.SetDeps(rows, conns)
	return srv, rows, ids
}
func TestOverviewReadsAllPagesKeepsPartialFailuresAndDeduplicatesCredentials(t *testing.T) {
	srv, _, ids := observabilityFixture(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customer := r.URL.Query().Get("customer")
		if customer == "Cc" {
			t.Error("other owner queried")
		}
		if customer == "Cb" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}]}}`)
			return
		}
		if r.URL.Path == "/groups" {
			fmt.Fprint(w, `{"groups":[{"id":"g"}]}`)
			return
		}
		if r.URL.Path != "/users" {
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.URL.Query().Get("pageToken") == "second" {
			fmt.Fprint(w, `{"users":[{"id":"known","customerId":"Ca","primaryEmail":"admin@a.example"},{"id":"unknown","customerId":"Ca","primaryEmail":"unknown@a.example"}]}`)
			return
		}
		fmt.Fprint(w, `{"users":[{"id":"known","customerId":"Ca","primaryEmail":"admin@a.example","isAdmin":true,"isDelegatedAdmin":false,"isEnrolledIn2Sv":false,"isEnforcedIn2Sv":true,"lastLoginTime":"2000-01-01T00:00:00Z","creationTime":"1999-01-01T00:00:00Z"}],"nextPageToken":"second"}`)
	}))
	defer fake.Close()
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		if sec.ServiceAccountJSON != "KEY-SECRET" {
			t.Error("wrong credential")
		}
		return &googleworkspace.Client{HTTP: fake.Client(), Endpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	result, err := srv.GetWorkspaceOverview(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceOverviewRequest{IncludeGroups: true, PageSize: 50}))
	if err != nil {
		t.Fatal(err)
	}
	r := result.Msg
	if len(r.Tenants) != 3 || len(r.Users) != 2 || r.TotalCount != 2 || r.UsersComplete {
		t.Fatalf("partial overview=%+v", r)
	}
	if r.Totals.UserCount != 2 || r.Totals.AdminsWithoutTwoStep != 1 || r.Totals.InactiveCount != 1 || r.Totals.SecurityUnknownCount != 1 {
		t.Fatalf("deduplicated security counts=%+v", r.Totals)
	}
	for _, summary := range r.Tenants {
		if summary.Tenant.CustomerId == "Cb" {
			if summary.UsersComplete || summary.UsersError == "" {
				t.Fatal("permission failure presented as empty healthy tenant")
			}
		} else if !summary.UsersComplete || summary.UserCount != 2 || summary.GroupCount == nil || *summary.GroupCount != 1 {
			t.Fatalf("paged tenant=%+v", summary)
		}
	}
	filtered, err := srv.GetWorkspaceOverview(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceOverviewRequest{SecurityFilter: "admins_without_two_step", PageSize: 1}))
	if err != nil || filtered.Msg.TotalCount != 1 || len(filtered.Msg.Users) != 1 || filtered.Msg.Users[0].User.Id != "known" {
		t.Fatalf("security filter=%v %v", filtered, err)
	}
	searched, err := srv.GetWorkspaceOverview(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceOverviewRequest{ConnectionId: ids[0], TenantId: "a", Query: "unknown@a.example"}))
	if err != nil || searched.Msg.TotalCount != 1 {
		t.Fatalf("tenant search=%v %v", searched, err)
	}
	if _, err = srv.GetWorkspaceOverview(asUser("other"), connect.NewRequest(&neoboxv1.GetWorkspaceOverviewRequest{ConnectionId: ids[0]})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-owner overview=%v", err)
	}
	if _, err = srv.GetWorkspaceOverview(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceOverviewRequest{TenantId: "a"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("ambiguous tenant=%v", err)
	}
}

type failingOperationRepo struct {
	*workspaceTestRepo
	failBegin, failFinish bool
}

func (r *failingOperationRepo) CreateOperation(ctx context.Context, o *repo.Operation) error {
	if r.failBegin {
		return errors.New("database error")
	}
	return r.workspaceTestRepo.CreateOperation(ctx, o)
}
func (r *failingOperationRepo) FinishOperation(ctx context.Context, o *repo.Operation) error {
	if r.failFinish {
		return errors.New("database error")
	}
	return r.workspaceTestRepo.FinishOperation(ctx, o)
}
func TestOperationRPCLogsActorAndSanitizedResultsAndFailsClosed(t *testing.T) {
	srv, rows, ids := observabilityFixture(t)
	store := &failingOperationRepo{workspaceTestRepo: rows}
	srv.SetDeps(store, srv.conns)
	writes := 0
	status := 200
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"id":"u","customerId":"Ca","primaryEmail":"u@a.example"}`)
			return
		}
		writes++
		w.WriteHeader(status)
		if status == 200 {
			fmt.Fprint(w, `{"id":"u","customerId":"Ca","primaryEmail":"u@a.example","password":"SERVER-SECRET"}`)
		} else {
			fmt.Fprint(w, `{"error":{"message":"PASSWORD-SECRET"}}`)
		}
	}))
	defer fake.Close()
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		return &googleworkspace.Client{HTTP: fake.Client(), Endpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	call := func() error {
		_, err := srv.ResetWorkspaceUserPassword(asUser("owner"), connect.NewRequest(&neoboxv1.ResetWorkspaceUserPasswordRequest{ConnectionId: ids[0], TenantId: "a", UserId: "u", Password: "PASSWORD-SECRET", ChangePasswordAtNextLogin: true}))
		return err
	}
	if err := call(); err != nil {
		t.Fatal(err)
	}
	for _, o := range rows.ops {
		if o.Status != "succeeded" || o.ActorID != "owner" || o.CustomerID != "Ca" || o.Requested["password_changed"] != true {
			t.Fatalf("operation=%+v", o)
		}
		raw, _ := json.Marshal(o)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatal("secret in persistent log")
		}
	}
	before := writes
	store.failBegin = true
	if err := call(); connect.CodeOf(err) != connect.CodeUnavailable || writes != before {
		t.Fatalf("unlogged write=%v writes=%d", err, writes)
	}
	store.failBegin = false
	status = 503
	if err := call(); connect.CodeOf(err) != connect.CodeUnknown {
		t.Fatalf("unknown outcome=%v", err)
	}
	found := false
	for _, o := range rows.ops {
		if o.Status == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown Google outcome not recorded")
	}
	status = 200
	store.failFinish = true
	if err := call(); connect.CodeOf(err) != connect.CodeUnknown {
		t.Fatalf("outcome persistence failure=%v", err)
	}
	found = false
	for _, o := range rows.ops {
		if o.Status == "pending" {
			found = true
		}
	}
	if !found {
		t.Fatal("pending log was not retained")
	}
	if _, err := srv.ListWorkspaceOperations(asUser("other"), connect.NewRequest(&neoboxv1.ListWorkspaceOperationsRequest{ConnectionId: ids[0]})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-owner history=%v", err)
	}
}
func TestAuditRangeValidation(t *testing.T) {
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	for _, in := range []*neoboxv1.ListWorkspaceAuditActivitiesRequest{{Application: "unknown"}, {StartTime: "invalid"}, {StartTime: now.Add(-181 * 24 * time.Hour).Format(time.RFC3339)}, {StartTime: now.Format(time.RFC3339), EndTime: now.Add(-time.Hour).Format(time.RFC3339)}} {
		if _, err := workspaceAuditQuery(in, now); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid audit filter accepted: %v", err)
		}
	}
	q, err := workspaceAuditQuery(&neoboxv1.ListWorkspaceAuditActivitiesRequest{}, now)
	if err != nil || q.Application != "login" || !q.Start.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("default audit window=%+v %v", q, err)
	}
}

func TestGoogleAuditRPCUsesOptionalReadScopeAndDoesNotCreateLocalOperations(t *testing.T) {
	srv, rows, ids := observabilityFixture(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/u" {
			fmt.Fprint(w, `{"id":"u","customerId":"Ca"}`)
			return
		}
		if r.URL.Query().Get("customerId") != "Ca" {
			t.Error("audit read was not explicitly tenant scoped")
		}
		fmt.Fprint(w, `{"items":[{"id":{"time":"2026-10-12T00:00:00Z","uniqueQualifier":"1","customerId":"Ca","applicationName":"login"},"events":[{"name":"login_success","parameters":[{"name":"scopes","multiValue":["read"]}]}]}],"nextPageToken":"next"}`)
	}))
	defer fake.Close()
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		if len(scopes) != 1 || scopes[0] != googleworkspace.AuditScope {
			t.Errorf("Google audit requested unrelated scopes: %v", scopes)
		}
		return &googleworkspace.Client{HTTP: fake.Client(), Endpoint: fake.URL, ReportsEndpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	result, err := srv.ListWorkspaceAuditActivities(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceAuditActivitiesRequest{ConnectionId: ids[0], TenantId: "a", Application: "login", UserKey: "u"}))
	if err != nil || len(result.Msg.Activities) != 1 || result.Msg.Activities[0].Events[0].Parameters == nil || result.Msg.NextPageToken != "next" {
		t.Fatalf("audit RPC=%v %v", result, err)
	}
	if len(rows.ops) != 0 {
		t.Fatal("read-only Google activity logged as a local mutation")
	}
}

type overviewPageStub struct {
	workspaceAPI
	pages []*googleworkspace.UserPage
	calls int
}

func (s *overviewPageStub) ListUsers(context.Context, string, string, int) (*googleworkspace.UserPage, error) {
	p := s.pages[s.calls%len(s.pages)]
	s.calls++
	return p, nil
}
func TestOverviewScanCapsAndRepeatedTokensAreNeverReportedComplete(t *testing.T) {
	pages := []*googleworkspace.UserPage{}
	for page := 0; page < 10; page++ {
		p := &googleworkspace.UserPage{NextPageToken: fmt.Sprint(page + 1)}
		for row := 0; row < 500; row++ {
			p.Users = append(p.Users, googleworkspace.User{ID: fmt.Sprintf("%d-%d", page, row)})
		}
		pages = append(pages, p)
	}
	stub := &overviewPageStub{pages: pages}
	users, complete, err := scanWorkspaceUsers(context.Background(), stub)
	if err != nil || complete || len(users) != workspaceOverviewScanLimit || stub.calls != 10 {
		t.Fatalf("scan cap=%d complete=%v err=%v calls=%d", len(users), complete, err, stub.calls)
	}
	pages[9].NextPageToken = ""
	stub.calls = 0
	_, complete, err = scanWorkspaceUsers(context.Background(), stub)
	if err != nil || !complete {
		t.Fatalf("exact final-page limit=%v %v", complete, err)
	}
	stub = &overviewPageStub{pages: []*googleworkspace.UserPage{{Users: []googleworkspace.User{{ID: "u"}}, NextPageToken: "repeat"}}}
	users, complete, err = scanWorkspaceUsers(context.Background(), stub)
	if err == nil || complete || len(users) != 1 || stub.calls != 2 {
		t.Fatal("repeated cursor asserted complete or duplicated users")
	}
}
