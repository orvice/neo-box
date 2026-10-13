package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type driveAPIFixture struct {
	srv      *GoogleWorkspaceServiceServer
	rows     *workspaceTestRepo
	conn     string
	domains  []string
	opened   [][2]string // subject, scope of each Drive token requested
	reasons  []string
	driveHit []string
	mu       sync.Mutex
}

// newDriveAPIFixture serves a Directory API (tenant A, user "ada" and tenant
// B's "bob") and a Drive API.
func newDriveAPIFixture(t *testing.T, drive http.HandlerFunc) *driveAPIFixture {
	t.Helper()
	srv, rows, ids := observabilityFixture(t)
	f := &driveAPIFixture{srv: srv, rows: rows, conn: ids[0], domains: []string{"a.example"}}
	directory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/ada" || r.URL.Path == "/users/ada@a.example":
			fmt.Fprint(w, `{"id":"ada","customerId":"Ca","primaryEmail":"ada@a.example"}`)
		case r.URL.Path == "/users/bob":
			fmt.Fprint(w, `{"id":"bob","customerId":"Cb","primaryEmail":"bob@b.example"}`)
		case strings.HasSuffix(r.URL.Path, "/domains"):
			if f.domains == nil {
				w.WriteHeader(403)
				return
			}
			rows := []string{}
			for _, d := range f.domains {
				rows = append(rows, fmt.Sprintf(`{"domainName":%q,"verified":true}`, d))
			}
			fmt.Fprintf(w, `{"domains":[%s]}`, strings.Join(rows, ","))
		default:
			t.Errorf("unexpected directory request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(directory.Close)
	if drive == nil {
		drive = func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/drives":
				fmt.Fprint(w, `{"nextPageToken":"p2","drives":[{"id":"d1","name":"SECRET-DRIVE","createdTime":"2025-03-01T10:00:00Z","orgUnitId":"id:ou","restrictions":{"driveMembersOnly":true}}]}`)
			case "/files":
				fmt.Fprint(w, `{"files":[{"id":"f1","name":"SECRET-FILE","mimeType":"application/pdf","modifiedTime":"2026-10-01T09:00:00Z","webViewLink":"https://drive.google.com/file/d/f1/view","permissions":[{"type":"anyone","role":"reader"},{"type":"domain","role":"writer","domain":"partner.test"}]}]}`)
			default:
				w.WriteHeader(404)
			}
		}
	}
	driveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.driveHit = append(f.driveHit, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		drive(w, r)
	}))
	t.Cleanup(driveServer.Close)
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		if len(scopes) != 0 {
			t.Errorf("Drive resolution asked for extra directory scopes: %v", scopes)
		}
		return &googleworkspace.Client{HTTP: directory.Client(), Endpoint: directory.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	srv.newDrive = func(_ context.Context, _ googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, subject, scope string, domains []string, reason string) (driveAPI, error) {
		if sec.ServiceAccountJSON != "KEY-SECRET" || customer != "Ca" {
			t.Errorf("wrong credential or tenant: %q %q", sec.ServiceAccountJSON, customer)
		}
		f.opened = append(f.opened, [2]string{subject, scope})
		f.reasons = append(f.reasons, reason)
		c := googleworkspace.NewDriveClientForHTTP(driveServer.Client(), driveServer.URL, customer, subject, scope)
		c.Domains, c.Reason = domains, reason
		return c, nil
	}
	return f
}

func (f *driveAPIFixture) drives(reason string) (*neoboxv1.ListWorkspaceSharedDrivesResponse, error) {
	res, err := f.srv.ListWorkspaceSharedDrives(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceSharedDrivesRequest{ConnectionId: f.conn, TenantId: "a", Reason: reason, NoOrganizer: true}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (f *driveAPIFixture) files(user string, c *neoboxv1.DriveAccessConfirmation) (*neoboxv1.ListWorkspaceUserExposedFilesResponse, error) {
	res, err := f.srv.ListWorkspaceUserExposedFiles(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceUserExposedFilesRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func driveConfirm(email string) *neoboxv1.DriveAccessConfirmation {
	return &neoboxv1.DriveAccessConfirmation{Reason: "quarterly review", TargetEmail: email}
}

func TestSharedDrivesAreListedAsTheAdministratorWithOnlyTheReadonlyScope(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	res, err := f.drives("  quarterly review ")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 1 || f.opened[0] != [2]string{"admin@a.example", googleworkspace.DriveReadonlyScope} || f.reasons[0] != "quarterly review" {
		t.Fatalf("opened=%v reasons=%v", f.opened, f.reasons)
	}
	if len(f.driveHit) != 1 || !strings.HasPrefix(f.driveHit[0], "GET /drives?") || !strings.Contains(f.driveHit[0], "useDomainAdminAccess=true") || !strings.Contains(f.driveHit[0], "organizerCount+%3D+0") {
		t.Fatalf("drive hits=%v", f.driveHit)
	}
	if len(res.Drives) != 1 || res.Drives[0].Name != "SECRET-DRIVE" || res.Drives[0].OrgUnitId != "id:ou" || !res.Drives[0].Restrictions.DriveMembersOnly || res.Drives[0].Restrictions.DomainUsersOnly || res.NextPageToken != "p2" || res.Drives[0].CreatedTime == nil {
		t.Fatalf("res=%v", res)
	}
	if len(f.rows.ops) != 1 {
		t.Fatalf("ops=%d", len(f.rows.ops))
	}
	for _, o := range f.rows.ops {
		if o.Action != googleworkspace.ActionDriveSharedDrivesList || o.Status != "succeeded" || o.CustomerID != "Ca" || o.ActorID != "owner" || o.Requested["reason"] != "quarterly review" || o.Requested["email"] != "admin@a.example" {
			t.Fatalf("operation=%+v", o)
		}
		raw, _ := json.Marshal(o)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatalf("operation holds a drive name: %s", raw)
		}
	}
	list, err := f.srv.ListWorkspaceOperations(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceOperationsRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || len(list.Msg.Operations) != 1 {
		t.Fatalf("list=%v %v", list, err)
	}
}

func TestSharedDriveRequestsAreRejectedBeforeAnyTokenIsRequested(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	long := strings.Repeat("x", 201)
	ten := int32(-1)
	for name, req := range map[string]*neoboxv1.ListWorkspaceSharedDrivesRequest{
		"no reason":       {ConnectionId: f.conn, TenantId: "a"},
		"blank reason":    {ConnectionId: f.conn, TenantId: "a", Reason: " \t "},
		"long reason":     {ConnectionId: f.conn, TenantId: "a", Reason: long},
		"multiline":       {ConnectionId: f.conn, TenantId: "a", Reason: "a\nb"},
		"bad ou":          {ConnectionId: f.conn, TenantId: "a", Reason: "r", OrgUnitId: "x' or 1=1"},
		"negative max":    {ConnectionId: f.conn, TenantId: "a", Reason: "r", MaxMembers: &ten},
		"huge page":       {ConnectionId: f.conn, TenantId: "a", Reason: "r", PageSize: 500},
		"long name query": {ConnectionId: f.conn, TenantId: "a", Reason: "r", NamePrefix: long},
	} {
		if _, err := f.srv.ListWorkspaceSharedDrives(asUser("owner"), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, c := range []struct {
		name   string
		who    string
		tenant string
	}{{"other owner", "other", "a"}, {"foreign tenant", "owner", "foreign"}} {
		_, err := f.srv.ListWorkspaceSharedDrives(asUser(c.who), connect.NewRequest(&neoboxv1.ListWorkspaceSharedDrivesRequest{ConnectionId: f.conn, TenantId: c.tenant, Reason: "r"}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if len(f.opened) != 0 || len(f.driveHit) != 0 || len(f.rows.ops) != 0 {
		t.Fatalf("token requested, Drive reached or operation recorded: %v %v %d", f.opened, f.driveHit, len(f.rows.ops))
	}
}

func TestExposedFilesAreListedAsTheUserWithOnlyTheMetadataScope(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	res, err := f.files("ada", driveConfirm(" ADA@a.example "))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 1 || f.opened[0] != [2]string{"ada@a.example", googleworkspace.DriveMetadataScope} || f.reasons[0] != "quarterly review" {
		t.Fatalf("opened=%v", f.opened)
	}
	if len(f.driveHit) != 1 || !strings.HasPrefix(f.driveHit[0], "GET /files?") || strings.Contains(f.driveHit[0], "alt=media") {
		t.Fatalf("drive hits=%v", f.driveHit)
	}
	if res.UserEmail != "ada@a.example" || len(res.Files) != 1 || res.Files[0].Name != "SECRET-FILE" || res.Files[0].WebViewLink == "" || len(res.Files[0].Exposures) != 2 {
		t.Fatalf("res=%v", res)
	}
	e := res.Files[0].Exposures
	if e[0].Kind != "anyone_with_link" || e[1].Kind != "domain_with_link" || e[1].External == nil || !*e[1].External {
		t.Fatalf("exposures=%v", e)
	}
	for _, o := range f.rows.ops {
		if o.Action != googleworkspace.ActionDriveExposedFilesList || o.Status != "succeeded" || o.Requested["reason"] != "quarterly review" || o.Requested["email"] != "ada@a.example" {
			t.Fatalf("operation=%+v", o)
		}
		raw, _ := json.Marshal(o)
		for _, leaked := range []string{"SECRET", "partner.test", "drive.google.com"} {
			if strings.Contains(string(raw), leaked) {
				t.Fatalf("operation holds %q: %s", leaked, raw)
			}
		}
		if o.Result["with_external_domain"] != float64(1) && o.Result["with_external_domain"] != 1 {
			t.Fatalf("result=%v", o.Result)
		}
	}
	if len(f.rows.ops) != 1 {
		t.Fatalf("ops=%d", len(f.rows.ops))
	}
}

func TestExposedFilesRejectedBeforeAnyTokenIsRequested(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	long := strings.Repeat("x", 201)
	for _, c := range []struct {
		name string
		user string
		conf *neoboxv1.DriveAccessConfirmation
		code connect.Code
	}{
		{"no confirmation", "ada", nil, connect.CodeInvalidArgument},
		{"empty reason", "ada", &neoboxv1.DriveAccessConfirmation{TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"long reason", "ada", &neoboxv1.DriveAccessConfirmation{Reason: long, TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"multi-line reason", "ada", &neoboxv1.DriveAccessConfirmation{Reason: "a\nb", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"missing target", "ada", &neoboxv1.DriveAccessConfirmation{Reason: "r"}, connect.CodeInvalidArgument},
		{"wrong target", "ada", &neoboxv1.DriveAccessConfirmation{Reason: "r", TargetEmail: "eve@a.example"}, connect.CodeInvalidArgument},
		{"user of another customer", "bob", &neoboxv1.DriveAccessConfirmation{Reason: "r", TargetEmail: "bob@b.example"}, connect.CodeNotFound},
		{"no user", "", driveConfirm("ada@a.example"), connect.CodeInvalidArgument},
	} {
		if _, err := f.files(c.user, c.conf); connect.CodeOf(err) != c.code {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for name, req := range map[string]*neoboxv1.ListWorkspaceUserExposedFilesRequest{
		"unknown exposure": {ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: driveConfirm("ada@a.example"), Exposures: []string{"limited"}},
		"huge page":        {ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: driveConfirm("ada@a.example"), PageSize: 1000},
	} {
		if _, err := f.srv.ListWorkspaceUserExposedFiles(asUser("owner"), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.srv.ListWorkspaceUserExposedFiles(asUser("other"), connect.NewRequest(&neoboxv1.ListWorkspaceUserExposedFilesRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: driveConfirm("ada@a.example")})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("other owner: %v", err)
	}
	if len(f.opened) != 0 || len(f.driveHit) != 0 || len(f.rows.ops) != 0 {
		t.Fatalf("token requested, Drive reached or operation recorded: %v %v %d", f.opened, f.driveHit, len(f.rows.ops))
	}
}

func TestExposedFilesKeepExternalUnknownWhenDomainsCannotBeRead(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	f.domains = nil
	res, err := f.files("ada", driveConfirm("ada@a.example"))
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Files[0].Exposures[1]; e.External != nil {
		t.Fatalf("external guessed without the tenant's domains: %v", e)
	}
}

func TestDriveReadsAreRefusedWhenTheyCannotBeRecorded(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	f.srv.SetDeps(&failingOperationRepo{workspaceTestRepo: f.rows, failBegin: true}, f.srv.conns)
	if _, err := f.drives("r"); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("drives: %v", err)
	}
	if _, err := f.files("ada", driveConfirm("ada@a.example")); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("files: %v", err)
	}
	if len(f.driveHit) != 0 {
		t.Fatalf("Drive reached without a record: %v", f.driveHit)
	}
}

func TestDriveMissingAuthorizationIsReportedBesideTheFeatureWithoutChangingTenantHealth(t *testing.T) {
	f := newDriveAPIFixture(t, nil)
	f.srv.newDrive = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, subject, scope string, _ []string, reason string) (driveAPI, error) {
		c := googleworkspace.NewDriveClientForHTTP(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, googleworkspace.ErrDelegation })}, "http://invalid", customer, subject, scope)
		c.Reason = reason
		return c, nil
	}
	before := f.rows.rows["a"].Status
	_, err := f.drives("r")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.DriveReadonlyScope) {
		t.Fatalf("drives: %v", err)
	}
	_, err = f.files("ada", driveConfirm("ada@a.example"))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.DriveMetadataScope) {
		t.Fatalf("files: %v", err)
	}
	if f.rows.rows["a"].Status != before {
		t.Fatal("a missing Drive scope changed the tenant's health")
	}
	for _, o := range f.rows.ops {
		if o.Status != "failed" {
			t.Fatalf("operation=%+v", o)
		}
	}
}

func TestDriveScopesAreTheTwoReadOnlyOnes(t *testing.T) {
	if googleworkspace.DriveReadonlyScope != "https://www.googleapis.com/auth/drive.readonly" || googleworkspace.DriveMetadataScope != "https://www.googleapis.com/auth/drive.metadata.readonly" {
		t.Fatal("Drive scopes changed")
	}
}
