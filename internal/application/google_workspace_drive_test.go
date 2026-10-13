package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type driveFixture struct {
	srv    *GoogleWorkspaceServiceServer
	rows   *workspaceTestRepo
	conn   string
	opened [][]string // scopes of each Google client opened
	hits   []string
}

func newDriveFixture(t *testing.T, reports http.HandlerFunc) *driveFixture {
	t.Helper()
	srv, rows, ids := observabilityFixture(t)
	f := &driveFixture{srv: srv, rows: rows, conn: ids[0]}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits = append(f.hits, r.URL.Path+"?"+r.URL.RawQuery)
		reports(w, r)
	}))
	t.Cleanup(fake.Close)
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		f.opened = append(f.opened, scopes)
		return &googleworkspace.Client{HTTP: fake.Client(), Endpoint: fake.URL, ReportsEndpoint: fake.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	return f
}

func driveAudit(events ...string) string {
	return fmt.Sprintf(`{"items":[{"id":{"time":"2026-10-12T01:00:00Z","uniqueQualifier":"1","customerId":"Ca","applicationName":"drive"},"actor":{"email":"ada@a.example"},"ipAddress":"203.0.113.9","events":[%s]}]}`, strings.Join(events, ","))
}

func driveEvent(name string, primary bool, extra string) string {
	return fmt.Sprintf(`{"name":%q,"type":"acl_change","parameters":[{"name":"doc_id","value":"d1"},{"name":"doc_title","value":"Budget"},{"name":"owner","value":"ada@a.example"},{"name":"visibility","value":"public_on_the_web"},{"name":"old_visibility","value":"private"},{"name":"primary_event","boolValue":%t}%s]}`, name, primary, extra)
}

func TestDriveActivitiesUseOnlyTheAuditScopeAndMapPresetsToGoogleFilters(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("customerId") != "Ca" {
			t.Error("read was not explicitly tenant scoped")
		}
		fmt.Fprint(w, driveAudit(driveEvent("change_document_visibility", true, `,{"name":"visibility_change","value":"external"}`), driveEvent("change_user_access", false, "")))
	})
	call := func(preset string, side bool) *neoboxv1.ListWorkspaceDriveActivitiesResponse {
		res, err := f.srv.ListWorkspaceDriveActivities(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: "a", Preset: preset, IncludeSideEffects: side}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	res := call("public_web", false)
	if len(f.opened) != 1 || len(f.opened[0]) != 1 || f.opened[0][0] != googleworkspace.AuditScope {
		t.Fatalf("scopes=%v", f.opened)
	}
	hit := f.hits[0]
	if !strings.HasPrefix(hit, "/activity/users/all/applications/drive?") || !strings.Contains(hit, "eventName=change_document_visibility") || !strings.Contains(hit, "filters=visibility%3D%3Dpublic_on_the_web") {
		t.Fatalf("hit=%s", hit)
	}
	if len(res.Activities) != 1 || len(res.Activities[0].Events) != 1 || res.HiddenEventCount != 1 {
		t.Fatalf("side effects not hidden and counted: %v", res)
	}
	e := res.Activities[0].Events[0]
	if e.DocTitle != "Budget" || e.Visibility != "public_on_the_web" || e.OldVisibility != "private" || e.VisibilityChange != "external" || e.Primary == nil || !*e.Primary || e.TargetUser != "" {
		t.Fatalf("event=%v", e)
	}
	if res.Activities[0].ActorEmail != "ada@a.example" || res.Activities[0].OccurredAt == nil {
		t.Fatalf("activity=%v", res.Activities[0])
	}
	if res := call("", true); len(res.Activities[0].Events) != 2 || res.HiddenEventCount != 0 {
		t.Fatalf("include side effects: %v", res)
	}
	if len(f.rows.ops) != 0 {
		t.Fatal("a read-only Google activity was logged as a local operation")
	}
}

func TestDriveActivityDropsAnActivityWhoseEventsAreAllSideEffects(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, driveAudit(driveEvent("change_user_access", false, "")))
	})
	res, err := f.srv.ListWorkspaceDriveActivities(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || len(res.Msg.Activities) != 0 || res.Msg.HiddenEventCount != 1 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestDriveRequestsAreRejectedBeforeGoogleIsReached(t *testing.T) {
	f := newDriveFixture(t, func(http.ResponseWriter, *http.Request) {})
	ctx := asUser("owner")
	if _, err := f.srv.ListWorkspaceDriveActivities(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: "a", Preset: "everything"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("preset=%v", err)
	}
	if _, err := f.srv.ListWorkspaceDriveActivities(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: "a", StartTime: "2020-01-01T00:00:00Z"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("window=%v", err)
	}
	if _, err := f.srv.ListWorkspaceDriveUsage(ctx, connect.NewRequest(&neoboxv1.ListWorkspaceDriveUsageRequest{ConnectionId: f.conn, TenantId: "a", Sort: "random"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("sort=%v", err)
	}
	for _, other := range []struct {
		ctx    context.Context
		tenant string
	}{{asUser("other"), "a"}, {ctx, "foreign"}} {
		if _, err := f.srv.ListWorkspaceDriveActivities(other.ctx, connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: other.tenant})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("activities %s=%v", other.tenant, err)
		}
		if _, err := f.srv.ListWorkspaceDriveUsage(other.ctx, connect.NewRequest(&neoboxv1.ListWorkspaceDriveUsageRequest{ConnectionId: f.conn, TenantId: other.tenant})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("usage %s=%v", other.tenant, err)
		}
	}
	if len(f.hits) != 0 || len(f.opened) != 0 {
		t.Fatalf("Google reached: %v %v", f.hits, f.opened)
	}
}

func usageRow(email, used, total string) string {
	params := []string{}
	if used != "" {
		params = append(params, fmt.Sprintf(`{"name":"accounts:drive_used_quota_in_mb","intValue":%q}`, used))
	}
	if total != "" {
		params = append(params, fmt.Sprintf(`{"name":"accounts:total_quota_in_mb","intValue":%q}`, total))
	}
	return fmt.Sprintf(`{"entity":{"customerId":"Ca","userEmail":%q,"type":"USER"},"parameters":[%s]}`, email, strings.Join(params, ","))
}

func TestDriveUsageRanksPagesAndKeepsUnknownUnknown(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"usageReports":[%s,%s,%s,%s]}`, usageRow("small@a.example", "100", "1000"), usageRow("big@a.example", "900", "1000"), usageRow("none@a.example", "", "1000"), usageRow("free@a.example", "50", "0"))
	})
	read := func(q, sortBy string, page, size int32) *neoboxv1.ListWorkspaceDriveUsageResponse {
		res, err := f.srv.ListWorkspaceDriveUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveUsageRequest{ConnectionId: f.conn, TenantId: "a", Query: q, Sort: sortBy, Page: page, PageSize: size}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	res := read("", "", 1, 3)
	if len(f.opened) != 1 || len(f.opened[0]) != 1 || f.opened[0][0] != googleworkspace.UsageScope {
		t.Fatalf("scopes=%v", f.opened)
	}
	if !res.HasData || res.Date == "" || res.TotalCount != 4 || res.UsersRead != 4 || res.TotalUsedMb != 1050 || res.UnknownUsedCount != 1 || res.Partial {
		t.Fatalf("res=%v", res)
	}
	emails := []string{}
	for _, u := range res.Users {
		emails = append(emails, u.Email)
	}
	if strings.Join(emails, ",") != "big@a.example,small@a.example,free@a.example" {
		t.Fatalf("page 1=%v", emails)
	}
	if p := res.Users[0].SharePercent; p == nil || *p != 90 {
		t.Fatalf("share=%v", p)
	}
	if res.Users[2].SharePercent != nil {
		t.Fatal("an unlimited quota got a percentage")
	}
	second := read("", "", 2, 3)
	if len(second.Users) != 1 || second.Users[0].Email != "none@a.example" || second.Users[0].UsedMb != nil {
		t.Fatalf("page 2=%v", second.Users)
	}
	if only := read("SMALL", "email", 1, 10); only.TotalCount != 1 || only.Users[0].Email != "small@a.example" {
		t.Fatalf("query=%v", only)
	}
	if len(f.rows.ops) != 0 {
		t.Fatal("usage read was logged as an operation")
	}
}

func TestDriveUsageWithoutAReportIsNotAnEmptyOrZeroResult(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"usageReports":[]}`) })
	res, err := f.srv.ListWorkspaceDriveUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveUsageRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || res.Msg.HasData || res.Msg.Date != "" || res.Msg.TotalUsedMb != 0 || len(res.Msg.Users) != 0 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestDriveMissingScopeIsReportedBesideTheViewWithoutChangingTenantHealth(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, _ *http.Request) {})
	f.srv.newClient = func(context.Context, googleworkspace.ConnectionConfig, googleworkspace.ConnectionSecret, string, string, ...string) (workspaceAPI, error) {
		return &googleworkspace.Client{HTTP: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, googleworkspace.ErrDelegation })}, Endpoint: "http://invalid", ReportsEndpoint: "http://invalid", CustomerID: "Ca", AdminEmail: "admin@a.example"}, nil
	}
	before := f.rows.rows["a"].Status
	_, err := f.srv.ListWorkspaceDriveUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveUsageRequest{ConnectionId: f.conn, TenantId: "a"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.UsageScope) {
		t.Fatalf("usage err=%v", err)
	}
	_, err = f.srv.ListWorkspaceDriveActivities(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceDriveActivitiesRequest{ConnectionId: f.conn, TenantId: "a"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.AuditScope) {
		t.Fatalf("activity err=%v", err)
	}
	if f.rows.rows["a"].Status != before {
		t.Fatal("a missing Drive scope changed the tenant's health")
	}
}

func TestDriveScopesNeverIncludeTheDriveAPI(t *testing.T) {
	for _, scope := range []string{googleworkspace.AuditScope, googleworkspace.UsageScope} {
		if !strings.Contains(scope, "admin.reports.") || strings.Contains(scope, "/auth/drive") {
			t.Fatalf("unexpected scope %s", scope)
		}
	}
}
