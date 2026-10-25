package cloudflare_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
)

func emailLogFixture(t *testing.T) (*cftest.Server, time.Time) {
	t.Helper()
	f := cftest.New()
	t.Cleanup(f.Close)
	f.AddAccount(accA, "A")
	f.AddToken("tok", &cftest.Token{Accounts: []string{accA}, Perms: []string{cftest.PermZoneRead, cftest.PermAnalyticsRead}})
	f.AddToken("noanalytics", &cftest.Token{Accounts: []string{accA}, Perms: []string{cftest.PermZoneRead, cftest.PermEmailRulesRead}})
	f.AddZone(&cftest.Zone{ID: "z1", Name: "example.com", Account: accA})
	now := time.Now().UTC().Truncate(time.Second)
	f.AddEmailEvent("z1", cftest.EmailEvent{Time: now.Add(-3 * time.Hour), From: "old@x.org", To: "a@example.com", Status: "delivered", EventType: "forward"})
	f.AddEmailEvent("z1", cftest.EmailEvent{
		Time: now.Add(-time.Hour), From: "bob@x.org", To: "info@example.com", Subject: "Hello", Status: "delivered",
		EventType: "forward", Action: "forward", RuleMatched: "rule-1", MessageID: "<m1@x.org>", SessionID: "s1",
		SPF: "pass", DKIM: "pass", DMARC: "pass", IsLastEvent: true,
	})
	f.AddEmailEvent("z1", cftest.EmailEvent{
		Time: now.Add(-time.Minute), From: "spam@x.org", To: "nobody@example.com", Subject: "Buy", Status: "dropped",
		EventType: "incoming", Action: "drop", ErrorDetail: "no rule", IsSpam: true,
	})
	return f, now
}

func TestListEmailRoutingEvents(t *testing.T) {
	f, now := emailLogFixture(t)
	c := newClient(t, f.URL, "tok")
	ctx := context.Background()

	got, err := c.ListEmailRoutingEvents(ctx, "z1", cloudflare.EmailLogQuery{Start: now.Add(-2 * time.Hour), End: now})
	if err != nil {
		t.Fatalf("ListEmailRoutingEvents: %v", err)
	}
	if len(got) != 2 || got[0].To != "nobody@example.com" || got[1].To != "info@example.com" {
		t.Fatalf("events = %+v, want the two newest, newest first", got)
	}
	if e := got[1]; e.Subject != "Hello" || e.RuleMatched != "rule-1" || e.SPF != "pass" || !bool(e.IsLastEvent) || bool(e.IsSpam) ||
		e.MessageID != "<m1@x.org>" || !e.Time.Equal(now.Add(-time.Hour)) {
		t.Fatalf("event = %+v", e)
	}
	if !bool(got[0].IsSpam) || got[0].ErrorDetail != "no rule" {
		t.Fatalf("spam event = %+v", got[0])
	}

	limited, err := c.ListEmailRoutingEvents(ctx, "z1", cloudflare.EmailLogQuery{Start: now.Add(-4 * time.Hour), End: now, Limit: 1})
	if err != nil || len(limited) != 1 || limited[0].To != "nobody@example.com" {
		t.Fatalf("limit 1 = %+v, %v", limited, err)
	}
	if w := f.Writes(); len(w) != 0 {
		t.Fatalf("a query was counted as a write: %+v", w)
	}
}

func TestListEmailRoutingEventsSendsTheQuery(t *testing.T) {
	f, now := emailLogFixture(t)
	c := newClient(t, f.URL, "tok")
	if _, err := c.ListEmailRoutingEvents(context.Background(), "z1", cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now, Limit: 25}); err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	query, _ := last.Body["query"].(string)
	vars, _ := last.Body["variables"].(map[string]any)
	if last.Method != http.MethodPost || last.Path != "/graphql" ||
		!strings.Contains(query, "emailRoutingAdaptive(") || !strings.Contains(query, "limit: 25") ||
		!strings.Contains(query, "orderBy: [datetime_DESC]") ||
		vars["zoneTag"] != "z1" || vars["start"] != now.Add(-time.Hour).Format(time.RFC3339) || vars["end"] != now.Format(time.RFC3339) {
		t.Fatalf("request = %+v", last)
	}
}

func TestListEmailRoutingEventsPermission(t *testing.T) {
	f, now := emailLogFixture(t)
	c := newClient(t, f.URL, "noanalytics")
	_, err := c.ListEmailRoutingEvents(context.Background(), "z1", cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now})
	if !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("without Analytics Read: err = %v, want permission denied", err)
	}
	bad := newClient(t, f.URL, "revoked")
	_, err = bad.ListEmailRoutingEvents(context.Background(), "z1", cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now})
	if !cloudflare.IsAuthError(err) {
		t.Fatalf("unknown token: err = %v, want auth error", err)
	}
}

func TestListEmailRoutingEventsGraphQLRetriesAndErrors(t *testing.T) {
	f, now := emailLogFixture(t)
	c := newClient(t, f.URL, "tok")
	fails := 1
	f.Intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/graphql" && fails > 0 {
			fails--
			http.Error(w, "boom", http.StatusBadGateway)
			return true
		}
		return false
	}
	got, err := c.ListEmailRoutingEvents(context.Background(), "z1", cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now})
	if err != nil || len(got) != 2 || fails != 0 {
		t.Fatalf("after one 502: %+v, %v (a query is retried like a read)", got, err)
	}

	f.Intercept = func(w http.ResponseWriter, r *http.Request) bool {
		cftest.Fail(w, http.StatusBadRequest, 1004, "bad query")
		return true
	}
	_, err = c.ListEmailRoutingEvents(context.Background(), "z1", cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now})
	if !cloudflare.IsRejected(err) {
		t.Fatalf("400: err = %v, want rejected", err)
	}
}

func TestEmailLogQueryValidate(t *testing.T) {
	now := time.Now()
	ok := cloudflare.EmailLogQuery{Start: now.Add(-time.Hour), End: now, Limit: 10}
	if err := ok.Validate(now); err != nil {
		t.Fatalf("valid query: %v", err)
	}
	for name, q := range map[string]cloudflare.EmailLogQuery{
		"no start":       {End: now, Limit: 10},
		"end before":     {Start: now, End: now.Add(-time.Hour), Limit: 10},
		"older than 31d": {Start: now.Add(-32 * 24 * time.Hour), End: now, Limit: 10},
		"too many":       {Start: now.Add(-time.Hour), End: now, Limit: 501},
		"negative":       {Start: now.Add(-time.Hour), End: now, Limit: -1},
	} {
		var fe *cloudflare.FieldError
		if err := q.Validate(now); !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a FieldError", name, err)
		}
	}
	if got := (cloudflare.EmailLogQuery{}).Normalized().Limit; got != cloudflare.EmailLogDefaultLimit {
		t.Errorf("default limit = %d", got)
	}
}
