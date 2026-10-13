package googleworkspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func reportsClient(t *testing.T, fn http.HandlerFunc) *Client {
	t.Helper()
	c := fakeClient(t, fn)
	c.ReportsEndpoint = c.Endpoint
	return c
}

func strParam(name, value string) AuditParameter { return AuditParameter{Name: name, Value: &value} }
func boolParam(name string, value bool) AuditParameter {
	return AuditParameter{Name: name, BoolValue: &value}
}

func TestDrivePresetsQueryGoogleWithAnEventAndFilter(t *testing.T) {
	want := map[string][2]string{
		"public_web":     {"change_document_visibility", "visibility==public_on_the_web"},
		"link_sharing":   {"change_document_visibility", "visibility==people_with_link"},
		"external_share": {"change_user_access", "visibility==shared_externally"},
		"ownership":      {"change_owner", ""},
		"downloads":      {"download", ""},
		"deletions":      {"delete", ""},
		"trash":          {"trash", ""},
		"all":            {"", ""},
	}
	for key, w := range want {
		p, ok := DrivePresetByKey(key)
		if !ok || p.Event != w[0] || p.Filters != w[1] {
			t.Errorf("%s = %+v", key, p)
		}
	}
	if p, ok := DrivePresetByKey(""); !ok || p.Key != "all" {
		t.Fatal("empty preset is not all")
	}
	if _, ok := DrivePresetByKey("drop table"); ok {
		t.Fatal("unknown preset accepted")
	}
	var gotPath string
	var gotQuery url.Values
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		fmt.Fprint(w, `{"items":[{"id":{"time":"2026-10-12T00:00:00Z","uniqueQualifier":"1","customerId":"CtenantA","applicationName":"drive"},"events":[]}]}`)
	})
	p, _ := DrivePresetByKey("public_web")
	now := time.Now().UTC()
	_, err := c.ListAuditActivities(context.Background(), AuditQuery{Application: "drive", EventName: p.Event, Filters: p.Filters, Start: now.Add(-time.Hour), End: now, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/activity/users/all/applications/drive" || gotQuery.Get("eventName") != "change_document_visibility" || gotQuery.Get("filters") != "visibility==public_on_the_web" || gotQuery.Get("customerId") != "CtenantA" {
		t.Fatalf("path=%s query=%v", gotPath, gotQuery)
	}
}

func TestDriveActivityFromAnotherCustomerIsRefused(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":{"time":"2026-10-12T00:00:00Z","uniqueQualifier":"1","customerId":"CtenantB","applicationName":"drive"},"events":[]}]}`)
	})
	now := time.Now().UTC()
	_, err := c.ListAuditActivities(context.Background(), AuditQuery{Application: "drive", Start: now.Add(-time.Hour), End: now, PageSize: 10})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestParseDriveEventKeepsMissingFieldsMissing(t *testing.T) {
	e := ParseDriveEvent(AuditEvent{Name: "change_user_access", Parameters: []AuditParameter{
		strParam("doc_id", "d1"), strParam("doc_title", "Budget"), strParam("doc_type", "spreadsheet"), strParam("owner", "ada@a.example"),
		strParam("visibility", "shared_externally"), strParam("old_visibility", "private"), strParam("visibility_change", "external"),
		strParam("target_user", "x@outside.test"), strParam("old_value", "none"), strParam("new_value", "can_edit"),
		boolParam("primary_event", true), boolParam("owner_is_shared_drive", false),
	}})
	if e.DocTitle != "Budget" || e.TargetUser != "x@outside.test" || e.OldRole != "none" || e.NewRole != "can_edit" || e.VisibilityChange != "external" || !e.IsPrimary() {
		t.Fatalf("event=%+v", e)
	}
	if e.TargetDomain != "" || e.NewOwner != "" || e.SharedDriveID != "" {
		t.Fatalf("invented fields: %+v", e)
	}
	// No primary flag: counted as primary rather than hidden.
	if bare := ParseDriveEvent(AuditEvent{Name: "download"}); !bare.IsPrimary() || bare.Primary != nil {
		t.Fatalf("bare=%+v", bare)
	}
	f := false
	side := ParseDriveEvent(AuditEvent{Name: "change_user_access", Parameters: []AuditParameter{{Name: "primary_event", BoolValue: &f}}})
	if side.IsPrimary() {
		t.Fatal("side effect treated as primary")
	}
	// A rename's old/new values are titles, not roles.
	rename := ParseDriveEvent(AuditEvent{Name: "rename", Parameters: []AuditParameter{strParam("old_value", "A"), strParam("new_value", "B")}})
	if rename.OldRole != "" || rename.NewRole != "" {
		t.Fatalf("titles read as roles: %+v", rename)
	}
	multi := ParseDriveEvent(AuditEvent{Name: "shared_drive_membership_change", Parameters: []AuditParameter{{Name: "target_user", MultiValue: []string{"a@x", "b@x"}}}})
	if multi.TargetUser != "a@x, b@x" {
		t.Fatalf("multi=%q", multi.TargetUser)
	}
}

func TestExposureNeverTreatsUnknownAsPrivate(t *testing.T) {
	cases := map[string]Exposure{
		"public_on_the_web": ExposurePublic, "people_with_link": ExposureLink, "shared_externally": ExposureExternal,
		"shared_internally": ExposureInternal, "people_within_domain_with_link": ExposureInternal, "public_in_the_domain": ExposureInternal,
		"private": ExposurePrivate, "unknown": ExposureUnknown, "": ExposureUnknown, "something_new": ExposureUnknown,
	}
	for in, want := range cases {
		if got := ExposureOf(in); got != want {
			t.Errorf("%q = %s, want %s", in, got, want)
		}
	}
}

func usageReport(customer, email string, used, total string) string {
	params := []string{}
	if used != "" {
		params = append(params, fmt.Sprintf(`{"name":"accounts:drive_used_quota_in_mb","intValue":%q}`, used))
	}
	if total != "" {
		params = append(params, fmt.Sprintf(`{"name":"accounts:total_quota_in_mb","intValue":%q}`, total))
	}
	return fmt.Sprintf(`{"entity":{"customerId":%q,"userEmail":%q,"type":"USER"},"parameters":[%s]}`, customer, email, strings.Join(params, ","))
}

func TestDriveUsageSkipsDaysWithoutDataAndReportsTheDate(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var days []string
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		day := strings.TrimPrefix(r.URL.Path, "/usage/users/all/dates/")
		days = append(days, day)
		if r.URL.Query().Get("customerId") != "CtenantA" || !strings.Contains(r.URL.Query().Get("parameters"), "accounts:drive_used_quota_in_mb") {
			t.Errorf("query=%v", r.URL.Query())
		}
		switch day {
		case "2026-10-12":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"errors":[{"reason":"invalid"}]}}`)
		case "2026-10-11":
			fmt.Fprint(w, `{"warnings":[{"code":"DATA_NOT_AVAILABLE"}]}`)
		default:
			fmt.Fprintf(w, `{"usageReports":[%s]}`, usageReport("CtenantA", "ada@a.example", "1024", "15360"))
		}
	})
	u, err := c.ListDriveUsage(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Date != "2026-10-10" || len(u.Users) != 1 || strings.Join(days, ",") != "2026-10-12,2026-10-11,2026-10-10" {
		t.Fatalf("date=%s users=%v days=%v", u.Date, u.Users, days)
	}
}

func TestDriveUsageWithNoDataIsNotZero(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"usageReports":[]}`) })
	if _, err := c.ListDriveUsage(context.Background(), time.Now()); !errors.Is(err, ErrNoUsageData) {
		t.Fatalf("err=%v", err)
	}
}

func TestDriveUsagePermissionFailureStopsInsteadOfProbingEarlierDays(t *testing.T) {
	calls := 0
	c := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}]}}`)
	})
	_, err := c.ListDriveUsage(context.Background(), time.Now())
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDriveUsageReadsEveryPageKeepsUnknownFiguresUnknownAndChecksCustomer(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "second" {
			fmt.Fprintf(w, `{"usageReports":[%s]}`, usageReport("CtenantA", "grace@a.example", "", "0"))
			return
		}
		fmt.Fprintf(w, `{"usageReports":[%s,%s],"nextPageToken":"second"}`, usageReport("CtenantA", "ada@a.example", "2048", "10240"), usageReport("CtenantA", "bob@a.example", "0", ""))
	})
	u, err := c.ListDriveUsage(context.Background(), time.Now())
	if err != nil || len(u.Users) != 3 || u.Partial {
		t.Fatalf("u=%+v err=%v", u, err)
	}
	ada, bob, grace := u.Users[0], u.Users[1], u.Users[2]
	if p := ada.SharePercent(); p == nil || *p != 20 {
		t.Fatalf("ada share=%v", p)
	}
	if bob.UsedMB == nil || *bob.UsedMB != 0 || bob.SharePercent() != nil {
		t.Fatalf("a reported zero must stay zero and a missing quota unknown: %+v", bob)
	}
	if grace.UsedMB != nil || grace.SharePercent() != nil {
		t.Fatalf("missing usage became a figure: %+v", grace)
	}
	mismatch := reportsClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"usageReports":[%s]}`, usageReport("CtenantB", "x@b.example", "1", "2"))
	})
	if _, err := mismatch.ListDriveUsage(context.Background(), time.Now()); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("other customer's usage shown: %v", err)
	}
}

func TestDriveUsageStopsAtTheCapAndSaysSo(t *testing.T) {
	c := reportsClient(t, func(w http.ResponseWriter, r *http.Request) {
		var rows []string
		for i := 0; i < 1000; i++ {
			rows = append(rows, usageReport("CtenantA", fmt.Sprintf("u%d-%s@a.example", i, r.URL.Query().Get("pageToken")), "1", "2"))
		}
		fmt.Fprintf(w, `{"usageReports":[%s],"nextPageToken":"more%s"}`, strings.Join(rows, ","), r.URL.Query().Get("pageToken"))
	})
	u, err := c.ListDriveUsage(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Users) != MaxUsageUsers || !u.Partial || u.PartialReason == "" {
		t.Fatalf("users=%d partial=%v %q", len(u.Users), u.Partial, u.PartialReason)
	}
}

func TestRankDriveUsage(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	users := []DriveUsageUser{{Email: "unknown@a.example"}, {Email: "small@a.example", UsedMB: n(5)}, {Email: "big@a.example", UsedMB: n(500)}, {Email: "zero@a.example", UsedMB: n(0)}}
	got := RankDriveUsage(users, "", "used")
	order := []string{}
	for _, u := range got {
		order = append(order, strings.Split(u.Email, "@")[0])
	}
	if strings.Join(order, ",") != "big,small,zero,unknown" {
		t.Fatalf("order=%v", order)
	}
	if got := RankDriveUsage(users, " BIG ", "used"); len(got) != 1 || got[0].Email != "big@a.example" {
		t.Fatalf("query=%v", got)
	}
	if got := RankDriveUsage(users, "", "email"); got[0].Email != "big@a.example" || got[3].Email != "zero@a.example" {
		t.Fatalf("email order=%v", got)
	}
}
