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
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type gmailFixture struct {
	srv       *GoogleWorkspaceServiceServer
	rows      *workspaceTestRepo
	conn      string
	opened    []string // primary emails a Gmail token was requested for
	domains   []string
	reasons   []string
	gmailHits []string
	mu        sync.Mutex
}

// newGmailFixture serves a Directory API for tenant A (Ca) and a Gmail API.
// Users: "ada" (ada@a.example, alias ada.l@a.example) and "bob" in tenant B.
func newGmailFixture(t *testing.T, gmail http.HandlerFunc) *gmailFixture {
	t.Helper()
	srv, rows, ids := observabilityFixture(t)
	f := &gmailFixture{srv: srv, rows: rows, conn: ids[0], domains: []string{"a.example"}}
	directory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/ada" || r.URL.Path == "/users/ada@a.example" || r.URL.Path == "/users/ada.l@a.example":
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
	if gmail == nil {
		gmail = func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path[strings.LastIndex(r.URL.Path, "/settings")+len("/settings"):]
			switch {
			case r.Method == http.MethodPut && path == "/vacation":
				fmt.Fprint(w, `{"enableAutoReply":true,"responseSubject":"Away","responseBodyPlainText":"SECRET-REPLY"}`)
			case path == "/vacation":
				fmt.Fprint(w, `{"enableAutoReply":false}`)
			case path == "/autoForwarding":
				fmt.Fprint(w, `{"enabled":true,"emailAddress":"boss@outside.test","disposition":"archive"}`)
			case path == "/forwardingAddresses":
				fmt.Fprint(w, `{"forwardingAddresses":[{"forwardingEmail":"boss@outside.test","verificationStatus":"accepted"}]}`)
			case path == "/delegates":
				fmt.Fprint(w, `{"delegates":[{"delegateEmail":"deputy@a.example","verificationStatus":"accepted"}]}`)
			case path == "/filters":
				fmt.Fprint(w, `{"filter":[{"id":"f","criteria":{"from":"SECRET-FROM@x.test"},"action":{"forward":"leak@outside.test"}}]}`)
			case path == "/sendAs":
				fmt.Fprint(w, `{"sendAs":[{"sendAsEmail":"ada@a.example","isPrimary":true}]}`)
			default:
				w.WriteHeader(404)
			}
		}
	}
	gmailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.gmailHits = append(f.gmailHits, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		gmail(w, r)
	}))
	t.Cleanup(gmailServer.Close)
	srv.newClient = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, admin string, scopes ...string) (workspaceAPI, error) {
		if len(scopes) != 0 {
			t.Errorf("Gmail resolution asked for extra directory scopes: %v", scopes)
		}
		return &googleworkspace.Client{HTTP: directory.Client(), Endpoint: directory.URL, CustomerID: customer, AdminEmail: admin}, nil
	}
	srv.newGmail = func(_ context.Context, _ googleworkspace.ConnectionConfig, sec googleworkspace.ConnectionSecret, customer, email string, domains []string, reason string) (gmailAPI, error) {
		if sec.ServiceAccountJSON != "KEY-SECRET" || customer != "Ca" {
			t.Errorf("wrong credential or tenant: %q %q", sec.ServiceAccountJSON, customer)
		}
		f.opened = append(f.opened, email)
		f.reasons = append(f.reasons, reason)
		g := googleworkspace.NewGmailClientForHTTP(gmailServer.Client(), gmailServer.URL, customer, email)
		g.Domains, g.Reason = domains, reason
		return g, nil
	}
	return f
}

func (f *gmailFixture) confirm(email string) *neoboxv1.GmailAccessConfirmation {
	return &neoboxv1.GmailAccessConfirmation{Reason: "offboarding check", TargetEmail: email}
}
func (f *gmailFixture) read(user string, c *neoboxv1.GmailAccessConfirmation) (*neoboxv1.GetWorkspaceUserGmailSettingsResponse, error) {
	res, err := f.srv.GetWorkspaceUserGmailSettings(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailSettingsRequest{ConnectionId: f.conn, TenantId: "a", UserId: user, Confirmation: c}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func TestGmailSettingsRejectedBeforeAnyTokenIsRequested(t *testing.T) {
	f := newGmailFixture(t, nil)
	long := strings.Repeat("x", 201)
	cases := []struct {
		name string
		user string
		c    *neoboxv1.GmailAccessConfirmation
		code connect.Code
	}{
		{"no confirmation", "ada", nil, connect.CodeInvalidArgument},
		{"empty reason", "ada", &neoboxv1.GmailAccessConfirmation{TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"blank reason", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "  \t ", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"reason too long", "ada", &neoboxv1.GmailAccessConfirmation{Reason: long, TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"multi-line reason", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "a\nb", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
		{"missing target", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r"}, connect.CodeInvalidArgument},
		{"wrong target", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "eve@a.example"}, connect.CodeInvalidArgument},
		{"alias typed instead of primary", "ada", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "ada.l@a.example"}, connect.CodeInvalidArgument},
		{"user of another customer", "bob", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "bob@b.example"}, connect.CodeNotFound},
		{"no user", "", &neoboxv1.GmailAccessConfirmation{Reason: "r", TargetEmail: "ada@a.example"}, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		if _, err := f.read(tc.user, tc.c); connect.CodeOf(err) != tc.code {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
	if len(f.opened) != 0 || len(f.gmailHits) != 0 {
		t.Fatalf("token requested or Gmail reached: %v %v", f.opened, f.gmailHits)
	}
	if len(f.rows.ops) != 0 {
		t.Fatal("a refused request was logged as an access")
	}
	// Another Neo Box user's connection and a foreign tenant stay unreachable.
	if _, err := f.srv.GetWorkspaceUserGmailSettings(asUser("other"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailSettingsRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: f.confirm("ada@a.example")})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other owner=%v", err)
	}
	if _, err := f.srv.GetWorkspaceUserGmailSettings(asUser("owner"), connect.NewRequest(&neoboxv1.GetWorkspaceUserGmailSettingsRequest{ConnectionId: f.conn, TenantId: "foreign", UserId: "ada", Confirmation: f.confirm("ada@a.example")})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign tenant=%v", err)
	}
	if len(f.opened) != 0 {
		t.Fatal("token requested for an unreachable tenant")
	}
}

func TestGmailSettingsReadIsRecordedWithReasonAndNoContent(t *testing.T) {
	f := newGmailFixture(t, nil)
	// The typed email is matched without regard to case, and a Google ID or an
	// alias resolves to the same primary-email subject.
	res, err := f.read("ada.l@a.example", f.confirm(" ADA@a.example "))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 1 || f.opened[0] != "ada@a.example" || f.reasons[0] != "offboarding check" {
		t.Fatalf("opened=%v reasons=%v", f.opened, f.reasons)
	}
	if res.UserEmail != "ada@a.example" || !res.Vacation.State.Ok || res.AutoForwarding.External == nil || !*res.AutoForwarding.External {
		t.Fatalf("response=%v", res)
	}
	if res.Filters.Filters[0].ForwardExternal == nil || !*res.Filters.Filters[0].ForwardExternal || res.Filters.Filters[0].From != "SECRET-FROM@x.test" {
		t.Fatal("live response lost the filter or its external flag")
	}
	if len(f.rows.ops) != 1 {
		t.Fatalf("ops=%d", len(f.rows.ops))
	}
	for _, o := range f.rows.ops {
		if o.Action != googleworkspace.ActionGmailSettingsRead || o.Status != "succeeded" || o.CustomerID != "Ca" || o.ActorID != "owner" || o.Requested["reason"] != "offboarding check" {
			t.Fatalf("operation=%+v", o)
		}
		raw, _ := json.Marshal(o)
		for _, leaked := range []string{"SECRET", "outside.test", "deputy"} {
			if strings.Contains(string(raw), leaked) {
				t.Fatalf("operation holds %q: %s", leaked, raw)
			}
		}
		if o.Result["external_destination"] != true || o.Result["filters"] != float64(1) && o.Result["filters"] != 1 {
			t.Fatalf("result=%v", o.Result)
		}
	}
	// Reads are listed with the tenant's other operations.
	list, err := f.srv.ListWorkspaceOperations(asUser("owner"), connect.NewRequest(&neoboxv1.ListWorkspaceOperationsRequest{ConnectionId: f.conn, TenantId: "a"}))
	if err != nil || len(list.Msg.Operations) != 1 {
		t.Fatalf("list=%v %v", list, err)
	}
}

func TestGmailSettingsKeepExternalUnknownWhenDomainsCannotBeRead(t *testing.T) {
	f := newGmailFixture(t, nil)
	f.domains = nil
	res, err := f.read("ada", f.confirm("ada@a.example"))
	if err != nil {
		t.Fatal(err)
	}
	if res.AutoForwarding.External != nil || res.Filters.Filters[0].ForwardExternal != nil {
		t.Fatal("external guessed without the tenant's domains")
	}
}

func TestGmailSettingsRefusedWhenTheReadCannotBeRecorded(t *testing.T) {
	f := newGmailFixture(t, nil)
	f.srv.SetDeps(&failingOperationRepo{workspaceTestRepo: f.rows, failBegin: true}, f.srv.conns)
	_, err := f.read("ada", f.confirm("ada@a.example"))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err=%v", err)
	}
	if len(f.gmailHits) != 0 {
		t.Fatalf("Gmail reached without a record: %v", f.gmailHits)
	}
}

func TestGmailSettingsMissingAuthorizationIsReportedBesideTheFeature(t *testing.T) {
	f := newGmailFixture(t, nil)
	f.srv.newGmail = func(_ context.Context, _ googleworkspace.ConnectionConfig, _ googleworkspace.ConnectionSecret, customer, email string, _ []string, reason string) (gmailAPI, error) {
		g := googleworkspace.NewGmailClientForHTTP(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, googleworkspace.ErrDelegation })}, "http://invalid", customer, email)
		g.Reason = reason
		return g, nil
	}
	before := f.rows.rows["a"].Status
	_, err := f.read("ada", f.confirm("ada@a.example"))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), googleworkspace.GmailSettingsScope) {
		t.Fatalf("err=%v", err)
	}
	if f.rows.rows["a"].Status != before {
		t.Fatal("a missing Gmail scope changed the tenant's health")
	}
	for _, o := range f.rows.ops {
		if o.Status != "failed" {
			t.Fatalf("operation=%+v", o)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGmailVacationUpdateValidatesThenRecordsAndNeverResends(t *testing.T) {
	puts := 0
	f := newGmailFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
			fmt.Fprint(w, `{"enableAutoReply":true,"responseSubject":"Away","responseBodyPlainText":"SECRET-REPLY","endTime":"1790000000000"}`)
			return
		}
		w.WriteHeader(404)
	})
	update := func(v *neoboxv1.GmailVacationInput, c *neoboxv1.GmailAccessConfirmation) (*neoboxv1.GmailVacation, error) {
		res, err := f.srv.UpdateWorkspaceUserGmailVacation(asUser("owner"), connect.NewRequest(&neoboxv1.UpdateWorkspaceUserGmailVacationRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: c, Vacation: v}))
		if err != nil {
			return nil, err
		}
		return res.Msg.Vacation, nil
	}
	end := timestamppb.New(time.UnixMilli(1790000000000))
	if _, err := update(nil, f.confirm("ada@a.example")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no vacation=%v", err)
	}
	if _, err := update(&neoboxv1.GmailVacationInput{Enabled: true}, f.confirm("ada@a.example")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("enabled without text=%v", err)
	}
	if _, err := update(&neoboxv1.GmailVacationInput{Enabled: true, Subject: "Away"}, &neoboxv1.GmailAccessConfirmation{TargetEmail: "ada@a.example"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no reason=%v", err)
	}
	if _, err := update(&neoboxv1.GmailVacationInput{Enabled: true, Subject: "Away"}, f.confirm("eve@a.example")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("wrong target=%v", err)
	}
	if len(f.opened) != 0 || puts != 0 || len(f.rows.ops) != 0 {
		t.Fatalf("a rejected update reached Google or the log: %v %d %d", f.opened, puts, len(f.rows.ops))
	}
	v, err := update(&neoboxv1.GmailVacationInput{Enabled: true, Subject: "Away", BodyPlainText: "SECRET-REPLY", EndTime: end}, f.confirm("ada@a.example"))
	if err != nil || !v.Enabled || v.EndTime == nil || puts != 1 {
		t.Fatalf("update=%v %v puts=%d", v, err, puts)
	}
	for _, o := range f.rows.ops {
		raw, _ := json.Marshal(o)
		if o.Action != googleworkspace.ActionGmailVacationUpdate || o.Status != "succeeded" || o.Requested["reason"] != "offboarding check" || strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "Away") {
			t.Fatalf("operation=%s", raw)
		}
	}
}

func TestGmailVacationUpdateUnconfirmedIsUnknownAndSentOnce(t *testing.T) {
	puts := 0
	f := newGmailFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
		}
		w.WriteHeader(502)
	})
	_, err := f.srv.UpdateWorkspaceUserGmailVacation(asUser("owner"), connect.NewRequest(&neoboxv1.UpdateWorkspaceUserGmailVacationRequest{ConnectionId: f.conn, TenantId: "a", UserId: "ada", Confirmation: f.confirm("ada@a.example"), Vacation: &neoboxv1.GmailVacationInput{Enabled: true, Subject: "Away"}}))
	if connect.CodeOf(err) != connect.CodeUnknown || puts != 1 {
		t.Fatalf("err=%v puts=%d", err, puts)
	}
	found := false
	for _, o := range f.rows.ops {
		found = found || o.Status == "unknown"
	}
	if !found {
		t.Fatal("unconfirmed change not recorded as unknown")
	}
}
