package googleworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const gmailUser = "ada@a.example"

// gmailServer answers Gmail settings reads with content that must never reach
// the operation log: a response text, filter criteria and addresses.
func gmailServer(t *testing.T, mutate func(path string, w http.ResponseWriter, r *http.Request) bool) (*GmailClient, *[]string) {
	t.Helper()
	var mu sync.Mutex
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/users/"+gmailUser+"/settings") {
			t.Errorf("request outside the target user: %s", r.URL.Path)
		}
		if mutate != nil && mutate(strings.TrimPrefix(r.URL.Path, "/users/"+gmailUser+"/settings"), w, r) {
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/users/"+gmailUser+"/settings") {
		case "/vacation":
			fmt.Fprint(w, `{"enableAutoReply":true,"responseSubject":"SECRET-SUBJECT","responseBodyPlainText":"SECRET-BODY","startTime":"1760000000000","endTime":"1760600000000","restrictToDomain":true}`)
		case "/autoForwarding":
			fmt.Fprint(w, `{"enabled":true,"emailAddress":"boss@outside.test","disposition":"leaveInInbox"}`)
		case "/forwardingAddresses":
			fmt.Fprint(w, `{"forwardingAddresses":[{"forwardingEmail":"boss@outside.test","verificationStatus":"accepted"},{"forwardingEmail":"mate@a.example","verificationStatus":"pending"}]}`)
		case "/delegates":
			fmt.Fprint(w, `{"delegates":[{"delegateEmail":"deputy@a.example","verificationStatus":"accepted"}]}`)
		case "/filters":
			fmt.Fprint(w, `{"filter":[{"id":"f1","criteria":{"from":"SECRET-CRITERIA@x.test"},"action":{"forward":"leak@outside.test","addLabelIds":["TRASH"],"removeLabelIds":["INBOX"]}},{"id":"f2","criteria":{"query":"invoice"},"action":{"addLabelIds":["Label_1"]}}]}`)
		case "/sendAs":
			fmt.Fprint(w, `{"sendAs":[{"sendAsEmail":"ada@a.example","isPrimary":true,"isDefault":true},{"sendAsEmail":"ceo@outside.test","displayName":"CEO","verificationStatus":"accepted"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	g := NewGmailClientForHTTP(srv.Client(), srv.URL, "Ca", gmailUser)
	g.Domains, g.Reason = []string{"a.example"}, "offboarding check"
	return g, &paths
}

func TestGmailReadIsLoggedBeforeAnyRequestAndKeepsContentOutOfTheLog(t *testing.T) {
	g, paths := gmailServer(t, nil)
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	g.transport.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if len(rec.writes) != 1 {
			t.Error("Gmail request sent before the read was recorded")
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	s, err := g.ReadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.writes) != 1 || rec.writes[0].Action != ActionGmailSettingsRead || rec.writes[0].Method != http.MethodGet {
		t.Fatalf("recorded=%+v", rec.writes)
	}
	if rec.writes[0].Requested["reason"] != "offboarding check" || rec.writes[0].Requested["email"] != gmailUser {
		t.Fatalf("requested=%v", rec.writes[0].Requested)
	}
	if len(*paths) != 6 {
		t.Fatalf("expected one read per section, got %v", *paths)
	}
	raw, _ := json.Marshal([]any{rec.writes, rec.results})
	for _, leaked := range []string{"SECRET", "outside.test", "deputy", "invoice", "ceo@"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("log holds %q: %s", leaked, raw)
		}
	}
	result := rec.results[0]
	if result["filters"] != 2 || result["forwarding_filters"] != 1 || result["delegates"] != 1 || result["external_destination"] != true || result["vacation_enabled"] != true || result["sections_failed"] != 0 {
		t.Fatalf("summary=%v", result)
	}
	if s.Vacation.Subject != "SECRET-SUBJECT" || s.Vacation.StartTime.IsZero() || !s.Vacation.RestrictToDomain {
		t.Fatalf("vacation=%+v", s.Vacation)
	}
	if e := s.AutoForwarding.External; e == nil || !*e {
		t.Fatal("auto-forward to an outside domain not flagged")
	}
	if e := s.ForwardingAddresses.Addresses[1].External; e == nil || *e {
		t.Fatal("in-tenant forwarding address flagged external")
	}
	f := s.Filters.Filters[0]
	if f.ForwardExternal == nil || !*f.ForwardExternal || !f.Deletes || !f.SkipsInbox || s.Filters.Filters[1].Deletes || s.Filters.Filters[1].SkipsInbox {
		t.Fatalf("filters=%+v", s.Filters.Filters)
	}
	if e := s.SendAs.Addresses[1].External; e == nil || !*e {
		t.Fatal("outside send-as not flagged")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGmailNothingIsSentWithoutADurableRecord(t *testing.T) {
	g, paths := gmailServer(t, nil)
	if _, err := g.ReadSettings(context.Background()); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("no recorder: %v", err)
	}
	g.SetWriteRecorder(&testWriteRecorder{beginErr: errors.New("db down")})
	if _, err := g.ReadSettings(context.Background()); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("begin failure: %v", err)
	}
	if _, err := g.UpdateVacation(context.Background(), VacationInput{Enabled: true, Subject: "away"}); !errors.Is(err, ErrLogUnavailable) {
		t.Fatalf("write begin failure: %v", err)
	}
	if len(*paths) != 0 {
		t.Fatalf("requests sent without a record: %v", *paths)
	}
}

func TestGmailOneFailedSectionDoesNotHideTheOthersAndUnknownIsNotOff(t *testing.T) {
	g, _ := gmailServer(t, func(path string, w http.ResponseWriter, _ *http.Request) bool {
		if path == "/delegates" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"errors":[{"reason":"forbidden"}],"message":"SECRET-GOOGLE-MESSAGE"}}`)
			return true
		}
		return false
	})
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	s, err := g.ReadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Delegates.Err == "" || strings.Contains(s.Delegates.Err, "SECRET") || s.Vacation.Err != "" || len(s.Filters.Filters) != 2 {
		t.Fatalf("sections=%+v", s)
	}
	if _, ok := rec.results[0]["delegates"]; ok {
		t.Fatal("an unread section was logged as a count")
	}
	if rec.results[0]["sections_failed"] != 1 {
		t.Fatalf("summary=%v", rec.results[0])
	}
}

func TestGmailAllSectionsFailingIsAFailedRead(t *testing.T) {
	g, _ := gmailServer(t, func(_ string, w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(403)
		return true
	})
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	if _, err := g.ReadSettings(context.Background()); err == nil {
		t.Fatal("a read that returned nothing succeeded")
	}
	if len(rec.outcomes) != 1 || rec.outcomes[0] == nil {
		t.Fatalf("outcomes=%v", rec.outcomes)
	}
}

func TestGmailExternalIsUnknownWhenDomainsAreUnknown(t *testing.T) {
	g, _ := gmailServer(t, nil)
	g.Domains = nil
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	s, err := g.ReadSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoForwarding.External != nil || s.Filters.Filters[0].ForwardExternal != nil || s.SendAs.Addresses[1].External != nil {
		t.Fatal("external guessed without the tenant's domains")
	}
	if _, ok := rec.results[0]["external_destination"]; ok {
		t.Fatalf("log claims an answer it does not have: %v", rec.results[0])
	}
	if IsExternal("not an address", []string{"a.example"}) != nil {
		t.Fatal("unparseable address classified")
	}
	if e := IsExternal("Ada <ADA@A.example>", []string{"a.example"}); e == nil || *e {
		t.Fatal("case or display name changed the domain")
	}
}

func TestGmailDelegationFailureStopsTheReadAndIsRecordedAsFailed(t *testing.T) {
	g, _ := gmailServer(t, nil)
	g.transport.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ErrDelegation })}
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	if _, err := g.ReadSettings(context.Background()); !errors.Is(err, ErrDelegation) {
		t.Fatalf("err=%v", err)
	}
	if len(rec.outcomes) != 1 || !errors.Is(rec.outcomes[0], ErrDelegation) {
		t.Fatalf("outcomes=%v", rec.outcomes)
	}
}

func TestGmailVacationUpdateSendsOneFullReplacementWithoutLoggingText(t *testing.T) {
	var bodies []map[string]any
	g, _ := gmailServer(t, func(path string, w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPut || path != "/vacation" {
			return false
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		fmt.Fprint(w, `{"enableAutoReply":true,"responseSubject":"Away","responseBodyPlainText":"SECRET-REPLY","startTime":"1760000000000"}`)
		return true
	})
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	start := time.UnixMilli(1760000000000).UTC()
	v, err := g.UpdateVacation(context.Background(), VacationInput{Enabled: true, Subject: "Away", BodyPlainText: "SECRET-REPLY", RestrictToDomain: true, StartTime: start})
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("sent %d times", len(bodies))
	}
	b := bodies[0]
	if b["responseBodyHtml"] != "" || b["responseBodyPlainText"] != "SECRET-REPLY" || b["restrictToDomain"] != true || b["startTime"] != "1760000000000" {
		t.Fatalf("body=%v", b)
	}
	if _, ok := b["endTime"]; ok {
		t.Fatal("unset end time not omitted, so it would keep an old value")
	}
	if !v.Enabled || v.BodyPlainText != "SECRET-REPLY" {
		t.Fatalf("vacation=%+v", v)
	}
	raw, _ := json.Marshal([]any{rec.writes, rec.results})
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "Away") {
		t.Fatalf("log holds the reply text: %s", raw)
	}
	if rec.writes[0].Action != ActionGmailVacationUpdate || rec.writes[0].Requested["enabled"] != true {
		t.Fatalf("recorded=%+v", rec.writes[0])
	}
}

func TestGmailVacationUpdateIsNeverResentWhenUnconfirmed(t *testing.T) {
	puts := 0
	g, _ := gmailServer(t, func(_ string, w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut {
			puts++
			w.WriteHeader(503)
			return true
		}
		return false
	})
	rec := &testWriteRecorder{}
	g.SetWriteRecorder(rec)
	_, err := g.UpdateVacation(context.Background(), VacationInput{Enabled: false})
	if !errors.Is(err, ErrUncertain) || puts != 1 {
		t.Fatalf("err=%v puts=%d", err, puts)
	}
	if len(rec.outcomes) != 1 || !errors.Is(rec.outcomes[0], ErrUncertain) {
		t.Fatalf("outcomes=%v", rec.outcomes)
	}
}

func TestGmailVacationValidation(t *testing.T) {
	now := time.Now()
	cases := map[string]VacationInput{
		"on without text":  {Enabled: true},
		"end before start": {Enabled: true, Subject: "x", StartTime: now, EndTime: now.Add(-time.Hour)},
		"subject too long": {Subject: strings.Repeat("x", 501)},
		"body too long":    {BodyPlainText: strings.Repeat("x", 20001)},
		"end equals start": {Enabled: true, Subject: "x", StartTime: now, EndTime: now},
	}
	for name, in := range cases {
		var fe *FieldError
		if err := in.Validate(); !errors.As(err, &fe) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if err := (VacationInput{Enabled: false}).Validate(); err != nil {
		t.Errorf("turning off needs no text: %v", err)
	}
	g, paths := gmailServer(t, nil)
	g.SetWriteRecorder(&testWriteRecorder{})
	if _, err := g.UpdateVacation(context.Background(), VacationInput{Enabled: true}); err == nil || len(*paths) != 0 {
		t.Fatalf("invalid input reached Google: %v %v", err, *paths)
	}
}

func TestGmailRequestsOnlyUseTheSettingsScope(t *testing.T) {
	if GmailSettingsScope != "https://www.googleapis.com/auth/gmail.settings.basic" {
		t.Fatal("scope changed")
	}
	for _, mail := range []string{"gmail.readonly", "gmail.modify", "mail.google.com", "gmail.settings.sharing"} {
		if strings.Contains(GmailSettingsScope, mail) {
			t.Fatalf("scope includes %s", mail)
		}
	}
}
