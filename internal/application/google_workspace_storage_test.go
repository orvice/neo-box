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

func storageRow(email, gmail, used, total string) string {
	var ps []string
	for name, v := range map[string]string{"accounts:gmail_used_quota_in_mb": gmail, "accounts:used_quota_in_mb": used, "accounts:total_quota_in_mb": total} {
		if v != "" {
			ps = append(ps, fmt.Sprintf(`{"name":%q,"intValue":%q}`, name, v))
		}
	}
	return fmt.Sprintf(`{"entity":{"customerId":"Ca","userEmail":%q,"type":"USER"},"parameters":[%s]}`, email, strings.Join(ps, ","))
}

func storageHandler(directoryFails bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/usage/users/all/dates/"):
			fmt.Fprintf(w, `{"usageReports":[%s,%s,%s,%s]}`,
				storageRow("ada@a.example", "700", "900", "1000"), storageRow("bob@a.example", "10", "100", "1000"),
				storageRow("cy@a.example", "", "2000", "1000"), storageRow("di@a.example", "", "", ""))
		case r.URL.Path == "/users":
			if directoryFails {
				w.WriteHeader(500)
				return
			}
			fmt.Fprint(w, `{"users":[
				{"id":"1","primaryEmail":"ada@a.example","customerId":"Ca","orgUnitPath":"/Sales","name":{"givenName":"Ada","familyName":"L"}},
				{"id":"2","primaryEmail":"bob@a.example","customerId":"Ca","orgUnitPath":"/Sales"},
				{"id":"3","primaryEmail":"cy@a.example","customerId":"Ca","orgUnitPath":"/Eng"},
				{"id":"4","primaryEmail":"di@a.example","customerId":"Ca","orgUnitPath":"/Eng"}]}`)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestStorageUsageSummarisesUsersAndOrgUnitsAgainstTheReportedQuota(t *testing.T) {
	f := newDriveFixture(t, storageHandler(false))
	res, err := f.srv.ListWorkspaceStorageUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceStorageUsageRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Msg
	if !m.HasData || m.UsersRead != 4 || m.TotalCount != 4 || m.ThresholdPercent != 80 || m.TotalUsedMb != 3000 {
		t.Fatalf("res=%v", m)
	}
	if m.NearLimitCount != 1 || m.OverLimitCount != 1 || m.UnknownUsedCount != 1 || m.NoLimitCount != 0 || m.Partial {
		t.Fatalf("counts near=%d over=%d unknown=%d nolimit=%d partial=%v", m.NearLimitCount, m.OverLimitCount, m.UnknownUsedCount, m.NoLimitCount, m.Partial)
	}
	if m.Users[0].Email != "cy@a.example" || m.Users[0].OrgUnitPath != "/Eng" || *m.Users[0].UsedPercent != 200 {
		t.Fatalf("first=%v", m.Users[0])
	}
	last := m.Users[3]
	if last.Email != "di@a.example" || last.UsedMb != nil || last.QuotaMb != nil || last.UsedPercent != nil {
		t.Fatalf("a user with no figures must stay unknown: %v", last)
	}
	if ada := m.Users[1]; ada.Name != "Ada L" || *ada.GmailMb != 700 || ada.DriveMb != nil {
		t.Fatalf("ada=%v", ada)
	}
	if len(m.OrgUnits) != 2 || m.OrgUnits[0].Path != "/Eng" || m.OrgUnits[0].OverLimitCount != 1 || m.OrgUnits[1].NearLimitCount != 1 || m.OrgUnits[1].QuotaMb == nil || *m.OrgUnits[1].QuotaMb != 1000 {
		t.Fatalf("units=%v", m.OrgUnits)
	}
	if len(f.opened) != 1 || len(f.opened[0]) != 1 || f.opened[0][0] != googleworkspace.UsageScope || len(f.rows.ops) != 0 {
		t.Fatalf("scopes=%v ops=%d", f.opened, len(f.rows.ops))
	}
}

func TestStorageUsageFiltersSortsAndPagesButSummarisesEveryone(t *testing.T) {
	f := newDriveFixture(t, storageHandler(false))
	call := func(r *neoboxv1.ListWorkspaceStorageUsageRequest) *neoboxv1.ListWorkspaceStorageUsageResponse {
		r.ConnectionId, r.TenantId = f.conn, "a"
		res, err := f.srv.ListWorkspaceStorageUsage(asUser("owner"), connect.NewRequest(r))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	m := call(&neoboxv1.ListWorkspaceStorageUsageRequest{OrgUnitPath: "/Sales", Sort: "percent"})
	if m.TotalCount != 2 || m.Users[0].Email != "ada@a.example" || len(m.OrgUnits) != 2 || m.UsersRead != 4 {
		t.Fatalf("unit filter: %v", m)
	}
	m = call(&neoboxv1.ListWorkspaceStorageUsageRequest{OnlyNearLimit: true, ThresholdPercent: 95})
	if m.TotalCount != 1 || m.Users[0].Email != "cy@a.example" || m.NearLimitCount != 0 || m.OverLimitCount != 1 {
		t.Fatalf("threshold 95: %v", m)
	}
	m = call(&neoboxv1.ListWorkspaceStorageUsageRequest{PageSize: 1, Page: 2})
	if len(m.Users) != 1 || m.Users[0].Email != "ada@a.example" || m.TotalCount != 4 {
		t.Fatalf("paging: %v", m)
	}
	if m = call(&neoboxv1.ListWorkspaceStorageUsageRequest{Query: "ADA"}); m.TotalCount != 1 {
		t.Fatalf("query: %v", m)
	}
}

func TestStorageUsageSaysSoWhenTheDirectoryCannotBeReadInsteadOfHidingTheFigures(t *testing.T) {
	f := newDriveFixture(t, storageHandler(true))
	res, err := f.srv.ListWorkspaceStorageUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceStorageUsageRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil {
		t.Fatal(err)
	}
	m := res.Msg
	if !m.Partial || m.PartialReason == "" || m.UsersRead != 4 || m.Users[0].OrgUnitPath != "" || len(m.OrgUnits) != 1 {
		t.Fatalf("res=%v", m)
	}
}

func TestStorageUsageWithoutAReportIsNoDataNotZero(t *testing.T) {
	f := newDriveFixture(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"usageReports":[]}`) })
	res, err := f.srv.ListWorkspaceStorageUsage(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceStorageUsageRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || res.Msg.HasData || res.Msg.TotalUsedMb != 0 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestStorageUsageRequestsAreRejectedBeforeGoogleIsReached(t *testing.T) {
	f := newDriveFixture(t, storageHandler(false))
	call := func(user, tenant string, r *neoboxv1.ListWorkspaceStorageUsageRequest) error {
		r.ConnectionId, r.TenantId = f.conn, tenant
		_, err := f.srv.ListWorkspaceStorageUsage(asUser(user), connect.NewRequest(r))
		return err
	}
	if connect.CodeOf(call("owner", "a", &neoboxv1.ListWorkspaceStorageUsageRequest{Sort: "random"})) != connect.CodeInvalidArgument {
		t.Fatal("sort accepted")
	}
	for _, th := range []int32{-1, 101} {
		if connect.CodeOf(call("owner", "a", &neoboxv1.ListWorkspaceStorageUsageRequest{ThresholdPercent: th})) != connect.CodeInvalidArgument {
			t.Fatalf("threshold %d accepted", th)
		}
	}
	if connect.CodeOf(call("other", "a", &neoboxv1.ListWorkspaceStorageUsageRequest{})) != connect.CodeNotFound || connect.CodeOf(call("owner", "foreign", &neoboxv1.ListWorkspaceStorageUsageRequest{})) != connect.CodeNotFound {
		t.Fatal("another user's tenant reachable")
	}
	if len(f.hits) != 0 || len(f.opened) != 0 {
		t.Fatalf("Google reached: %v %v", f.hits, f.opened)
	}
}
