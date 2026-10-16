package application

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

func userUsageHandler(customer string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/u1" || r.URL.Path == "/users/ada@a.example":
			fmt.Fprintf(w, `{"id":"u1","primaryEmail":"ada@a.example","customerId":%q}`, customer)
		case strings.HasPrefix(r.URL.Path, "/usage/users/ada@a.example/dates/"):
			fmt.Fprint(w, `{"usageReports":[{"entity":{"customerId":"Ca","userEmail":"ada@a.example","type":"USER"},"parameters":[{"name":"accounts:gmail_used_quota_in_mb","intValue":"120"},{"name":"accounts:total_quota_in_mb","intValue":"30720"},{"name":"gmail:num_emails_sent","intValue":"0"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestUserUsageResolvesTheUserAsksOnlyForTheUsageScopeAndLogsNothing(t *testing.T) {
	f := newDriveFixture(t, userUsageHandler("Ca"))
	res, err := f.srv.GetWorkspaceUserUsage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserUsageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "u1", Days: 3}))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Msg
	if m.UserEmail != "ada@a.example" || m.DaysRequested != 3 || len(m.Days) != 3 {
		t.Fatalf("res=%v", m)
	}
	d := m.Days[0]
	if d.GmailUsedMb == nil || *d.GmailUsedMb != 120 || d.TotalMb == nil || *d.TotalMb != 30720 || d.EmailsSent == nil || *d.EmailsSent != 0 || d.DriveUsedMb != nil {
		t.Fatalf("day=%v", d)
	}
	if len(f.opened) != 1 || len(f.opened[0]) != 1 || f.opened[0][0] != googleworkspace.UsageScope {
		t.Fatalf("scopes=%v", f.opened)
	}
	if len(f.rows.ops) != 0 {
		t.Fatal("an aggregate report read was logged as a local operation")
	}
}

func TestUserUsageDefaultsToFourteenDays(t *testing.T) {
	f := newDriveFixture(t, userUsageHandler("Ca"))
	res, err := f.srv.GetWorkspaceUserUsage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserUsageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada@a.example"}))
	if err != nil || res.Msg.DaysRequested != 14 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestUserUsageIsRefusedForAnotherCustomersUserBeforeAnyReportIsRead(t *testing.T) {
	f := newDriveFixture(t, userUsageHandler("Cother"))
	_, err := f.srv.GetWorkspaceUserUsage(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserUsageRequest{ConnectionId: f.conn, TenantId: "a", UserId: "u1"}))
	if err == nil {
		t.Fatal("another customer's user accepted")
	}
	for _, h := range f.hits {
		if strings.HasPrefix(h, "/usage/") {
			t.Fatalf("report read for a foreign user: %v", f.hits)
		}
	}
}

func TestUserUsageRequestsAreRejectedBeforeGoogleIsReached(t *testing.T) {
	f := newDriveFixture(t, userUsageHandler("Ca"))
	call := func(ctxUser, tenant, user string, days int32) error {
		_, err := f.srv.GetWorkspaceUserUsage(asUser(ctxUser), connect.NewRequest(&neoboxv1.GetWorkspaceUserUsageRequest{ConnectionId: f.conn, TenantId: tenant, UserId: user, Days: days}))
		return err
	}
	if connect.CodeOf(call("owner", "a", "", 0)) != connect.CodeInvalidArgument {
		t.Fatal("empty user accepted")
	}
	if connect.CodeOf(call("owner", "a", "u1", 31)) != connect.CodeInvalidArgument || connect.CodeOf(call("owner", "a", "u1", -1)) != connect.CodeInvalidArgument {
		t.Fatal("days out of range accepted")
	}
	if connect.CodeOf(call("other", "a", "u1", 0)) != connect.CodeNotFound || connect.CodeOf(call("owner", "foreign", "u1", 0)) != connect.CodeNotFound {
		t.Fatal("another user's tenant reachable")
	}
	if len(f.hits) != 0 || len(f.opened) != 0 {
		t.Fatalf("Google reached: %v %v", f.hits, f.opened)
	}
}
