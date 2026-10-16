package googleworkspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

func storageReport(customer, email string, params map[string]string) string {
	var ps []string
	for name, v := range params {
		ps = append(ps, fmt.Sprintf(`{"name":%q,"intValue":%q}`, name, v))
	}
	return fmt.Sprintf(`{"entity":{"customerId":%q,"userEmail":%q,"type":"USER"},"parameters":[%s]}`, customer, email, strings.Join(ps, ","))
}

func TestStorageUsageReadsEveryPageWithAllStorageParametersAndKeepsUnknownUnknown(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, want := range []string{"accounts:gmail_used_quota_in_mb", "accounts:drive_used_quota_in_mb", "accounts:gplus_photos_used_quota_in_mb", "accounts:used_quota_in_mb", "accounts:total_quota_in_mb"} {
			if !strings.Contains(q.Get("parameters"), want) {
				t.Errorf("parameters %q lack %s", q.Get("parameters"), want)
			}
		}
		if q.Get("pageToken") == "second" {
			fmt.Fprintf(w, `{"usageReports":[%s]}`, storageReport("CtenantA", "grace@a.example", nil))
			return
		}
		fmt.Fprintf(w, `{"usageReports":[%s],"nextPageToken":"second"}`, storageReport("CtenantA", "ada@a.example", map[string]string{"accounts:gmail_used_quota_in_mb": "300", "accounts:used_quota_in_mb": "500", "accounts:total_quota_in_mb": "1000", "accounts:drive_used_quota_in_mb": "0"}))
	})
	u, err := c.ListStorageUsage(context.Background(), time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	if err != nil || len(u.Users) != 2 || u.Date != "2026-10-12" {
		t.Fatalf("u=%+v err=%v", u, err)
	}
	ada, grace := u.Users[0], u.Users[1]
	if *ada.GmailMB != 300 || *ada.UsedMB != 500 || *ada.TotalMB != 1000 || ada.DriveMB == nil || *ada.DriveMB != 0 || ada.PhotosMB != nil {
		t.Fatalf("ada=%+v", ada)
	}
	if grace.Used() != nil || grace.Percent() != nil || grace.StateAt(80) != StorageUnknown {
		t.Fatalf("missing figures became numbers: %+v", grace)
	}
}

func TestStorageUsageRefusesAnotherCustomersReportAndReportsNoData(t *testing.T) {
	mismatch := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"usageReports":[%s]}`, storageReport("CtenantB", "x@b.example", nil))
	})
	if _, err := mismatch.ListStorageUsage(context.Background(), time.Now()); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err=%v", err)
	}
	empty := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"usageReports":[]}`) })
	if _, err := empty.ListStorageUsage(context.Background(), time.Now()); !errors.Is(err, ErrNoUsageData) {
		t.Fatalf("err=%v", err)
	}
}

func TestStorageUserFiguresNeverInventNumbers(t *testing.T) {
	parts := StorageUser{GmailMB: i64(100), DriveMB: i64(50)}
	if u := parts.Used(); u == nil || *u != 150 {
		t.Fatalf("sum of parts: %v", u)
	}
	if u := (StorageUser{GmailMB: i64(100), UsedMB: i64(400)}).Used(); *u != 400 {
		t.Fatal("Google's own total must win over the parts")
	}
	if (StorageUser{}).Used() != nil {
		t.Fatal("no figures became a number")
	}
	if (StorageUser{UsedMB: i64(5), TotalMB: i64(0)}).Quota() != nil || (StorageUser{UsedMB: i64(5), TotalMB: i64(-1)}).Quota() != nil {
		t.Fatal("a non-positive quota is no limit")
	}
	if (StorageUser{UsedMB: i64(5), TotalMB: i64(0)}).Percent() != nil {
		t.Fatal("percent of no limit")
	}
	if p := (StorageUser{UsedMB: i64(0), TotalMB: i64(100)}).Percent(); p == nil || *p != 0 {
		t.Fatalf("a reported zero must stay zero: %v", p)
	}
}

func TestStorageStatesAtAThreshold(t *testing.T) {
	cases := []struct {
		u    StorageUser
		want StorageState
	}{
		{StorageUser{}, StorageUnknown},
		{StorageUser{UsedMB: i64(10)}, StorageNoLimit},
		{StorageUser{UsedMB: i64(79), TotalMB: i64(100)}, StorageOK},
		{StorageUser{UsedMB: i64(80), TotalMB: i64(100)}, StorageNear},
		{StorageUser{UsedMB: i64(99), TotalMB: i64(100)}, StorageNear},
		{StorageUser{UsedMB: i64(100), TotalMB: i64(100)}, StorageOver},
		{StorageUser{UsedMB: i64(150), TotalMB: i64(100)}, StorageOver},
	}
	for _, c := range cases {
		if got := c.u.StateAt(80); got != c.want {
			t.Errorf("%+v = %s, want %s", c.u, got, c.want)
		}
	}
}

func fixtureRows() []StorageRow {
	dir := []User{
		{PrimaryEmail: "Ada@a.example", OrgUnitPath: "/Sales"},
		{PrimaryEmail: "bob@a.example", OrgUnitPath: "/Sales"},
		{PrimaryEmail: "cy@a.example", OrgUnitPath: "/Eng"},
		{PrimaryEmail: "di@a.example", OrgUnitPath: "/Eng"},
	}
	dir[0].Name.GivenName, dir[0].Name.FamilyName = "Ada", "Lovelace"
	return JoinStorage([]StorageUser{
		{Email: "ada@a.example", UsedMB: i64(900), TotalMB: i64(1000)},
		{Email: "bob@a.example", UsedMB: i64(100), TotalMB: i64(1000)},
		{Email: "cy@a.example", UsedMB: i64(2000), TotalMB: i64(1000)},
		{Email: "di@a.example", UsedMB: i64(50)},
		{Email: "ghost@a.example"},
	}, dir)
}

func TestJoinStorageMatchesByEmailIgnoringCaseAndKeepsUnmatchedUsers(t *testing.T) {
	rows := fixtureRows()
	if rows[0].OrgUnit != "/Sales" || rows[0].Name != "Ada Lovelace" {
		t.Fatalf("ada=%+v", rows[0])
	}
	if rows[4].OrgUnit != "" || len(rows) != 5 {
		t.Fatalf("an unmatched user was dropped or placed: %+v", rows[4])
	}
}

func TestFilterAndSortStorage(t *testing.T) {
	rows := fixtureRows()
	emails := func(rs []StorageRow) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Email)
		}
		return out
	}
	if got := emails(FilterStorage(rows, StorageFilter{Query: "LOVE"})); !reflect.DeepEqual(got, []string{"ada@a.example"}) {
		t.Fatalf("name search: %v", got)
	}
	if got := emails(FilterStorage(rows, StorageFilter{OrgUnit: "/Eng"})); len(got) != 2 {
		t.Fatalf("unit: %v", got)
	}
	if got := emails(FilterStorage(rows, StorageFilter{OnlyNear: true, Threshold: 80})); !reflect.DeepEqual(got, []string{"ada@a.example", "cy@a.example"}) {
		t.Fatalf("near or over: %v", got)
	}
	by := func(s string) []string {
		c := append([]StorageRow(nil), rows...)
		SortStorage(c, s)
		return emails(c)
	}
	if got := by("used"); !reflect.DeepEqual(got, []string{"cy@a.example", "ada@a.example", "bob@a.example", "di@a.example", "ghost@a.example"}) {
		t.Fatalf("used: %v", got)
	}
	// ghost has no figure and di has no quota: both come after those with one.
	if got := by("percent"); !reflect.DeepEqual(got, []string{"cy@a.example", "ada@a.example", "bob@a.example", "di@a.example", "ghost@a.example"}) {
		t.Fatalf("percent: %v", got)
	}
	if got := by("email"); !reflect.DeepEqual(got, []string{"ada@a.example", "bob@a.example", "cy@a.example", "di@a.example", "ghost@a.example"}) {
		t.Fatalf("email: %v", got)
	}
}

func TestSummarizeOrgUnits(t *testing.T) {
	got := SummarizeOrgUnits(fixtureRows(), 80)
	byPath := map[string]OrgUnitStorage{}
	for _, s := range got {
		byPath[s.Path] = s
	}
	eng, sales, none := byPath["/Eng"], byPath["/Sales"], byPath[""]
	if got[0].Path != "/Eng" {
		t.Fatalf("not largest first: %+v", got)
	}
	if eng.Users != 2 || eng.UsedMB != 2050 || eng.LargestMB != 2000 || eng.OverCount != 1 || eng.NearCount != 0 {
		t.Fatalf("eng=%+v", eng)
	}
	// di reports no quota; that must not make the unit's quota mixed.
	if eng.QuotaMB == nil || *eng.QuotaMB != 1000 || eng.QuotaMixed {
		t.Fatalf("eng quota=%v mixed=%v", eng.QuotaMB, eng.QuotaMixed)
	}
	if sales.NearCount != 1 || sales.UsedMB != 1000 || sales.QuotaMB == nil {
		t.Fatalf("sales=%+v", sales)
	}
	if none.Users != 1 || none.Unknown != 1 || none.UsedMB != 0 || none.QuotaMB != nil {
		t.Fatalf("users without a unit or figure: %+v", none)
	}
	mixed := SummarizeOrgUnits(JoinStorage([]StorageUser{
		{Email: "a@a", UsedMB: i64(1), TotalMB: i64(100)}, {Email: "b@a", UsedMB: i64(1), TotalMB: i64(200)}, {Email: "c@a", UsedMB: i64(1), TotalMB: i64(100)},
	}, []User{{PrimaryEmail: "a@a", OrgUnitPath: "/X"}, {PrimaryEmail: "b@a", OrgUnitPath: "/X"}, {PrimaryEmail: "c@a", OrgUnitPath: "/X"}}), 80)
	if mixed[0].QuotaMB != nil || !mixed[0].QuotaMixed {
		t.Fatalf("mixed=%+v", mixed[0])
	}
}
