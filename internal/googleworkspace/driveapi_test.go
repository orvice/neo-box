package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type driveHit struct {
	Method, Path string
	Query        url.Values
}

func driveServer(t *testing.T, scope string, handler http.HandlerFunc) (*DriveClient, *testWriteRecorder, *[]driveHit) {
	t.Helper()
	var mu sync.Mutex
	hits := []driveHit{}
	rec := &testWriteRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, driveHit{r.Method, r.URL.Path, r.URL.Query()})
		mu.Unlock()
		if len(rec.writes) == 0 {
			t.Error("Drive request sent before the read was recorded")
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	d := NewDriveClientForHTTP(srv.Client(), srv.URL, "Ca", "ada@a.example", scope)
	d.Domains, d.Reason = []string{"a.example"}, "audit"
	d.SetWriteRecorder(rec)
	return d, rec, &hits
}

func TestSharedDriveSearchTerms(t *testing.T) {
	five := 5
	zero := 0
	cases := []struct {
		q    SharedDriveQuery
		want string
	}{
		{SharedDriveQuery{}, ""},
		{SharedDriveQuery{NamePrefix: "Fin"}, "name contains 'Fin'"},
		{SharedDriveQuery{NamePrefix: `O'Neil\x`}, `name contains 'O\'Neil\\x'`},
		{SharedDriveQuery{NoOrganizer: true, MaxMembers: &five, OrgUnitID: "id:03ph8a2z"}, "organizerCount = 0 and memberCount <= 5 and orgUnitId = 'id:03ph8a2z'"},
		{SharedDriveQuery{MaxMembers: &zero}, "memberCount <= 0"},
	}
	for _, c := range cases {
		if got := c.q.Search(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.q, got, c.want)
		}
	}
}

func TestSharedDriveQueryValidation(t *testing.T) {
	big, neg := 1000000, -1
	for name, q := range map[string]SharedDriveQuery{
		"long name":     {NamePrefix: strings.Repeat("a", 101)},
		"newline name":  {NamePrefix: "a\nb"},
		"quote in ou":   {OrgUnitID: "x' or name contains 'y"},
		"member upper":  {MaxMembers: &big},
		"member lower":  {MaxMembers: &neg},
		"page too big":  {PageSize: 101},
		"page negative": {PageSize: -1},
	} {
		q := q
		var f *FieldError
		if err := q.Validate(); !errors.As(err, &f) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	q := SharedDriveQuery{}
	if err := q.Validate(); err != nil || q.PageSize != 50 {
		t.Fatalf("defaults: %v %d", err, q.PageSize)
	}
}

func TestListSharedDrivesIsLoggedFirstAsTheAdministratorAndKeepsNamesOutOfTheLog(t *testing.T) {
	d, rec, hits := driveServer(t, DriveReadonlyScope, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"nextPageToken":"more","drives":[{"id":"d1","name":"SECRET-DRIVE-NAME","createdTime":"2025-03-01T10:00:00.000Z","orgUnitId":"id:ou1","restrictions":{"domainUsersOnly":true,"copyRequiresWriterPermission":true}},{"id":"d2","name":"Plain"}]}`)
	})
	five := 5
	page, err := d.ListSharedDrives(context.Background(), SharedDriveQuery{NamePrefix: "SECRET", NoOrganizer: true, MaxMembers: &five, PageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	h := (*hits)[0]
	if len(*hits) != 1 || h.Method != "GET" || h.Path != "/drives" || h.Query.Get("useDomainAdminAccess") != "true" || h.Query.Get("pageSize") != "25" || h.Query.Get("q") != "name contains 'SECRET' and organizerCount = 0 and memberCount <= 5" {
		t.Fatalf("hits=%+v", *hits)
	}
	if !strings.Contains(h.Query.Get("fields"), "restrictions(") || strings.Contains(h.Query.Get("fields"), "capabilities") {
		t.Fatalf("fields=%s", h.Query.Get("fields"))
	}
	if len(page.Drives) != 2 || page.NextPageToken != "more" || page.Drives[0].CreatedTime.IsZero() || page.Drives[0].OrgUnitID != "id:ou1" {
		t.Fatalf("page=%+v", page)
	}
	r := page.Drives[0].Restrictions
	if !r.DomainUsersOnly || !r.CopyRequiresWriterPermission || r.DriveMembersOnly || r.AdminManaged {
		t.Fatalf("restrictions=%+v", r)
	}
	w := rec.writes[0]
	if w.Action != ActionDriveSharedDrivesList || w.Method != "GET" || w.Requested["reason"] != "audit" || w.Requested["email"] != "ada@a.example" || w.Requested["name_prefix"] != true || w.Requested["max_members"] != 5 {
		t.Fatalf("recorded=%+v", w)
	}
	raw, _ := json.Marshal([]any{rec.writes, rec.results})
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "Plain") {
		t.Fatalf("log holds names or filter text: %s", raw)
	}
	if rec.results[0]["returned"] != 2 || rec.results[0]["has_more"] != true {
		t.Fatalf("summary=%v", rec.results[0])
	}
}

func TestDriveReadsNeedADurableRecordAndTheRightScope(t *testing.T) {
	d, _, hits := driveServer(t, DriveReadonlyScope, func(http.ResponseWriter, *http.Request) {})
	d.SetWriteRecorder(nil)
	if _, err := d.ListSharedDrives(context.Background(), SharedDriveQuery{}); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("no recorder: %v", err)
	}
	d.SetWriteRecorder(&testWriteRecorder{beginErr: errors.New("db down")})
	if _, err := d.ListSharedDrives(context.Background(), SharedDriveQuery{}); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("begin failure: %v", err)
	}
	if len(*hits) != 0 {
		t.Fatalf("sent without a record: %v", *hits)
	}
	// A read-only client never lists files, and a metadata client never lists drives.
	rec := &testWriteRecorder{}
	d.SetWriteRecorder(rec)
	if _, err := d.ListExposedFiles(context.Background(), ExposedFilesQuery{}); err == nil {
		t.Fatal("drive.readonly client listed files")
	}
	m, _, _ := driveServer(t, DriveMetadataScope, func(http.ResponseWriter, *http.Request) {})
	if _, err := m.ListSharedDrives(context.Background(), SharedDriveQuery{}); err == nil {
		t.Fatal("metadata client listed shared drives")
	}
	if len(rec.writes) != 0 || len(*hits) != 0 {
		t.Fatal("scope refusal was recorded or sent")
	}
	if _, err := NewDriveClient(context.Background(), ConnectionConfig{}, ConnectionSecret{}, "Ca", "ada@a.example", "https://www.googleapis.com/auth/drive"); err == nil {
		t.Fatal("full Drive scope accepted")
	}
}

func TestDriveOutcomeIsRecordedEvenOnFailure(t *testing.T) {
	d, rec, _ := driveServer(t, DriveReadonlyScope, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}]}}`)
	})
	_, err := d.ListSharedDrives(context.Background(), SharedDriveQuery{})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 403 || len(rec.outcomes) != 1 || rec.outcomes[0] == nil {
		t.Fatalf("err=%v outcomes=%v", err, rec.outcomes)
	}
	rec.finishErr = errors.New("db down")
	if _, err := d.ListSharedDrives(context.Background(), SharedDriveQuery{}); !errors.Is(err, ErrOutcomeLogUnavailable) {
		t.Fatalf("outcome write failure: %v", err)
	}
}

func TestExposedFilesQuery(t *testing.T) {
	q := ExposedFilesQuery{}
	if err := q.Validate(); err != nil || len(q.Exposures) != 4 || q.PageSize != 50 {
		t.Fatalf("defaults: %v %+v", err, q)
	}
	want := "'me' in owners and trashed = false and (visibility = 'anyoneCanFind' or visibility = 'anyoneWithLink' or visibility = 'domainCanFind' or visibility = 'domainWithLink')"
	if q.Search() != want {
		t.Fatalf("search=%s", q.Search())
	}
	q = ExposedFilesQuery{Exposures: []string{"domain_with_link", "anyone_with_link", "anyone_with_link"}}
	if err := q.Validate(); err != nil || strings.Join(q.Exposures, ",") != "anyone_with_link,domain_with_link" {
		t.Fatalf("order/dedupe: %v %v", err, q.Exposures)
	}
	if q.Search() != "'me' in owners and trashed = false and (visibility = 'anyoneWithLink' or visibility = 'domainWithLink')" {
		t.Fatalf("search=%s", q.Search())
	}
	for _, bad := range []ExposedFilesQuery{{Exposures: []string{"limited"}}, {Exposures: []string{"anyoneCanFind' or 1=1"}}, {PageSize: 1000}} {
		bad := bad
		var f *FieldError
		if err := bad.Validate(); !errors.As(err, &f) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
}

func TestListExposedFilesDerivesExposureFromPermissionsAndKeepsContentOutOfTheLog(t *testing.T) {
	d, rec, hits := driveServer(t, DriveMetadataScope, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"nextPageToken":"n2","incompleteSearch":true,"files":[
			{"id":"f1","name":"SECRET-PLAN.docx","mimeType":"application/vnd.google-apps.document","modifiedTime":"2026-10-01T09:00:00.000Z","webViewLink":"https://docs.google.com/document/d/f1/edit","permissions":[{"type":"user","role":"owner"},{"type":"anyone","role":"reader","allowFileDiscovery":true}]},
			{"id":"f2","name":"Budget","mimeType":"application/pdf","modifiedTime":"2026-09-01T09:00:00.000Z","webViewLink":"javascript:alert(1)","permissions":[{"type":"domain","role":"writer","domain":"a.example"},{"type":"domain","role":"reader","domain":"partner.test","allowFileDiscovery":true},{"type":"anyone","role":"commenter"}]},
			{"id":"f3","name":"NoPerms","mimeType":"text/plain"}]}`)
	})
	page, err := d.ListExposedFiles(context.Background(), ExposedFilesQuery{Exposures: []string{"anyone_can_find", "anyone_with_link"}, PageSize: 30})
	if err != nil {
		t.Fatal(err)
	}
	h := (*hits)[0]
	if h.Path != "/files" || h.Query.Get("q") != "'me' in owners and trashed = false and (visibility = 'anyoneCanFind' or visibility = 'anyoneWithLink')" || h.Query.Get("pageSize") != "30" || h.Query.Get("corpora") != "" {
		t.Fatalf("hit=%+v", h)
	}
	if strings.Contains(h.Query.Get("fields"), "emailAddress") || strings.Contains(h.Query.Get("fields"), "exportLinks") {
		t.Fatalf("fields=%s", h.Query.Get("fields"))
	}
	if !page.IncompleteSearch || page.NextPageToken != "n2" || len(page.Files) != 3 {
		t.Fatalf("page=%+v", page)
	}
	f1, f2, f3 := page.Files[0], page.Files[1], page.Files[2]
	if len(f1.Exposures) != 1 || f1.Exposures[0].Kind != "anyone_can_find" || f1.Exposures[0].Role != "reader" || f1.WebViewLink == "" || f1.ModifiedTime.IsZero() {
		t.Fatalf("f1=%+v", f1)
	}
	if len(f2.Exposures) != 3 || f2.WebViewLink != "" {
		t.Fatalf("f2=%+v", f2)
	}
	if e := f2.Exposures[0]; e.Kind != "domain_with_link" || e.External == nil || *e.External {
		t.Fatalf("own domain flagged external: %+v", e)
	}
	if e := f2.Exposures[1]; e.Kind != "domain_can_find" || e.External == nil || !*e.External {
		t.Fatalf("partner domain not flagged external: %+v", e)
	}
	if e := f2.Exposures[2]; e.Kind != "anyone_with_link" || e.External != nil {
		t.Fatalf("anyone exposure=%+v", e)
	}
	if len(f3.Exposures) != 0 {
		t.Fatalf("f3=%+v", f3)
	}
	w := rec.writes[0]
	if w.Action != ActionDriveExposedFilesList || w.Requested["reason"] != "audit" || w.Requested["email"] != "ada@a.example" {
		t.Fatalf("recorded=%+v", w)
	}
	raw, _ := json.Marshal([]any{rec.writes, rec.results})
	for _, leaked := range []string{"SECRET", "Budget", "docs.google.com", "partner.test", "NoPerms"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("log holds %q: %s", leaked, raw)
		}
	}
	res := rec.results[0]
	if res["returned"] != 3 || res["with_anyone"] != 2 || res["with_domain"] != 1 || res["with_external_domain"] != 1 || res["without_permission_info"] != 1 || res["has_more"] != true || res["incomplete_search"] != true {
		t.Fatalf("summary=%v", res)
	}
}

func TestExposedFilesDomainsUnknownIsNotExternal(t *testing.T) {
	d, _, _ := driveServer(t, DriveMetadataScope, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"files":[{"id":"f","permissions":[{"type":"domain","role":"reader","domain":"x.test"}]}]}`)
	})
	d.Domains = nil
	page, err := d.ListExposedFiles(context.Background(), ExposedFilesQuery{})
	if err != nil || page.Files[0].Exposures[0].External != nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestValidAccessReason(t *testing.T) {
	if r, err := ValidAccessReason("  offboarding  "); err != nil || r != "offboarding" {
		t.Fatalf("%q %v", r, err)
	}
	for _, bad := range []string{"", "  ", "a\nb", strings.Repeat("x", 201)} {
		if _, err := ValidAccessReason(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
