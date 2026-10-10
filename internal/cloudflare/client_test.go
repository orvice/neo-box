package cloudflare_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
)

const (
	accA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	accB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newClient(t *testing.T, endpoint, token string) *cloudflare.Client {
	t.Helper()
	c, err := cloudflare.New(token, cloudflare.WithEndpoint(endpoint), cloudflare.WithRetryDelay(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeWithZones(t *testing.T) *cftest.Server {
	t.Helper()
	f := cftest.New()
	t.Cleanup(f.Close)
	f.AddAccount(accA, "A")
	f.AddAccount(accB, "B")
	f.AddToken("tok", &cftest.Token{Accounts: []string{accA, accB}, Perms: []string{cftest.PermZoneRead, cftest.PermDNSEdit}})
	for _, n := range []string{"alpha.com", "beta.com", "gamma.net", "delta.org", "epsilon.com", "zeta.com", "eta.io", "iota.com", "kappa.com", "lambda.com"} {
		f.AddZone(&cftest.Zone{ID: "z-" + n, Name: n, Account: accA})
	}
	f.AddZone(&cftest.Zone{ID: "z-other", Name: "other.com", Account: accB})
	return f
}

func TestListZonesFiltersByAccountSearchesAndPages(t *testing.T) {
	f := fakeWithZones(t)
	c := newClient(t, f.URL, "tok")
	ctx := context.Background()

	zones, info, err := c.ListZones(ctx, cloudflare.ZoneQuery{AccountID: accA, Name: "COM", Page: 2, PerPage: 5})
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	// .com zones of account A by name: alpha, beta, epsilon, iota, kappa |
	// lambda, zeta.
	if len(zones) != 2 || zones[0].Name != "lambda.com" || zones[1].Name != "zeta.com" {
		t.Fatalf("page 2 = %+v", zones)
	}
	if info.Page != 2 || info.TotalCount != 7 || info.TotalPages != 2 {
		t.Fatalf("info = %+v", info)
	}
	if zones[0].Account.ID != accA {
		t.Fatalf("account = %+v", zones[0].Account)
	}
	all, _, err := c.ListZones(ctx, cloudflare.ZoneQuery{AccountID: accA, PerPage: 50})
	if err != nil || len(all) != 10 {
		t.Fatalf("all of account A = %d zones, %v", len(all), err)
	}
	for _, z := range all {
		if z.Name == "other.com" {
			t.Fatal("zone of another account listed")
		}
	}
}

func TestDNSRecordStructuredFieldsAndEditKeepsOtherFields(t *testing.T) {
	f := fakeWithZones(t)
	c := newClient(t, f.URL, "tok")
	ctx := context.Background()
	zone := "z-alpha.com"

	srv, err := c.CreateDNSRecord(ctx, zone, cloudflare.RecordInput{
		Type: "SRV", Name: "_sip._tcp.alpha.com", TTL: 300,
		SRV: cloudflare.SRVData{Priority: 10, Weight: 5, Port: 5060, Target: "sip.alpha.com"},
	})
	if err != nil {
		t.Fatalf("create SRV: %v", err)
	}
	if srv.Data == nil || *srv.Data.Port != 5060 || *srv.Data.Weight != 5 || *srv.Data.Priority != 10 || srv.Data.Target != "sip.alpha.com" {
		t.Fatalf("SRV data = %+v", srv.Data)
	}
	caa, err := c.CreateDNSRecord(ctx, zone, cloudflare.RecordInput{
		Type: "CAA", Name: "alpha.com", TTL: 1, CAA: cloudflare.CAAData{Flags: 0, Tag: "issue", Value: "letsencrypt.org"},
	})
	if err != nil || caa.Data == nil || *caa.Data.Flags != 0 || caa.Data.Tag != "issue" || caa.Data.Value != "letsencrypt.org" {
		t.Fatalf("create CAA = %+v, %v", caa, err)
	}
	mx, err := c.CreateDNSRecord(ctx, zone, cloudflare.RecordInput{Type: "MX", Name: "alpha.com", Content: "mx.alpha.com", TTL: 1, Priority: 20})
	if err != nil || mx.Priority == nil || *mx.Priority != 20 {
		t.Fatalf("create MX = %+v, %v", mx, err)
	}
	for _, w := range f.Writes() {
		if w.Body["type"] == "SRV" || w.Body["type"] == "CAA" {
			if _, hasContent := w.Body["content"]; hasContent {
				t.Errorf("%v create sent content; Cloudflare derives it from data: %v", w.Body["type"], w.Body)
			}
		}
	}

	// A record with attributes the form doesn't show.
	id := f.AddRecord(zone, map[string]any{
		"type": "A", "name": "www.alpha.com", "content": "192.0.2.1", "ttl": 3600, "proxied": true,
		"comment": "managed by hand", "tags": []string{"env:prod"}, "settings": map[string]any{"ipv4_only": true},
	})
	before, err := c.GetDNSRecord(ctx, zone, id)
	if err != nil || !before.Proxiable || !before.Proxied {
		t.Fatalf("GetDNSRecord = %+v, %v", before, err)
	}
	after, err := c.EditDNSRecord(ctx, zone, id, cloudflare.RecordInput{Type: "A", Name: "www.alpha.com", Content: "192.0.2.2", TTL: 1, Proxied: true}, before)
	if err != nil {
		t.Fatalf("EditDNSRecord: %v", err)
	}
	if after.Content != "192.0.2.2" || after.Comment != "managed by hand" || len(after.Tags) != 1 {
		t.Fatalf("after edit = %+v", after)
	}
	stored := f.Record(zone, id)
	if s, _ := stored["settings"].(map[string]any); s["ipv4_only"] != true {
		t.Fatalf("settings lost: %+v", stored)
	}
	last := f.Writes()[len(f.Writes())-1]
	if last.Method != http.MethodPatch {
		t.Fatalf("edit used %s, want PATCH", last.Method)
	}
	for _, k := range []string{"comment", "tags", "settings", "type"} {
		if _, sent := last.Body[k]; sent {
			t.Errorf("edit sent %q, which the form doesn't show: %v", k, last.Body)
		}
	}

	proxied, err := c.SetDNSRecordProxied(ctx, zone, id, false)
	if err != nil || proxied.Proxied || proxied.Content != "192.0.2.2" || proxied.TTL != 1 {
		t.Fatalf("SetDNSRecordProxied = %+v, %v", proxied, err)
	}
	if body := f.Writes()[len(f.Writes())-1].Body; len(body) != 1 {
		t.Fatalf("proxy toggle sent %v, want only proxied", body)
	}

	if err := c.DeleteDNSRecord(ctx, zone, mx.ID); err != nil {
		t.Fatalf("DeleteDNSRecord: %v", err)
	}
	if _, err := c.GetDNSRecord(ctx, zone, mx.ID); !cloudflare.IsNotFound(err) {
		t.Fatalf("deleted record: %v, want not found", err)
	}
}

func TestErrorsAreConverted(t *testing.T) {
	f := fakeWithZones(t)
	f.AddToken("dns-read", &cftest.Token{Accounts: []string{accA}, Perms: []string{cftest.PermDNSRead}})
	ctx := context.Background()

	_, err := newClient(t, f.URL, "nope").GetZone(ctx, "z-alpha.com")
	if !cloudflare.IsAuthError(err) || cloudflare.IsPermissionDenied(err) {
		t.Fatalf("invalid token: %v", err)
	}
	_, err = newClient(t, f.URL, "dns-read").CreateDNSRecord(ctx, "z-alpha.com", cloudflare.RecordInput{Type: "A", Name: "x.alpha.com", Content: "192.0.2.9", TTL: 1})
	if !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("write without permission: %v", err)
	}
	_, err = newClient(t, f.URL, "tok").CreateDNSRecord(ctx, "z-alpha.com", cloudflare.RecordInput{Type: "A", Name: "x.alpha.com", Content: "192.0.2.9", TTL: 10})
	var apiErr *cloudflare.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || !strings.Contains(apiErr.Message(), "TTL") {
		t.Fatalf("rejected record: %v", err)
	}
	if strings.Contains(err.Error(), "tok") {
		t.Fatalf("error mentions the token: %v", err)
	}
	if _, err := newClient(t, f.URL, "tok").GetZone(ctx, "z-missing"); !cloudflare.IsNotFound(err) {
		t.Fatalf("missing zone: %v", err)
	}
}

func TestVerifyTokenAcceptsUserAndAccountOwnedTokens(t *testing.T) {
	f := fakeWithZones(t)
	f.AddToken("acct-tok", &cftest.Token{OwnerAccount: accA, Accounts: []string{accA}, Perms: []string{cftest.PermZoneRead}})
	f.AddToken("expired", &cftest.Token{Status: "expired", Accounts: []string{accA}})
	ctx := context.Background()

	if err := newClient(t, f.URL, "tok").VerifyToken(ctx, accA); err != nil {
		t.Fatalf("user token: %v", err)
	}
	if err := newClient(t, f.URL, "acct-tok").VerifyToken(ctx, accA); err != nil {
		t.Fatalf("account-owned token: %v", err)
	}
	if err := newClient(t, f.URL, "acct-tok").VerifyToken(ctx, accB); err == nil {
		t.Fatal("account-owned token of another account verified")
	}
	if err := newClient(t, f.URL, "expired").VerifyToken(ctx, accA); err == nil {
		t.Fatal("expired token verified")
	}
}

// flaky answers each request with the next status, then 200.
func flaky(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		if n < len(statuses) {
			if statuses[n] == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "0")
			}
			cftest.Fail(w, statuses[n], 10000, http.StatusText(statuses[n]))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"messages":[],"result":{"id":"r1","type":"A","name":"a","content":"192.0.2.1","ttl":1}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestReadsRetryButUncertainWritesDoNot(t *testing.T) {
	ctx := context.Background()
	in := cloudflare.RecordInput{Type: "A", Name: "a", Content: "192.0.2.1", TTL: 1}

	srv, calls := flaky(t, http.StatusServiceUnavailable, http.StatusBadGateway)
	if _, err := newClient(t, srv.URL, "tok").GetDNSRecord(ctx, "z", "r1"); err != nil || calls.Load() != 3 {
		t.Fatalf("read after two 5xx: %v after %d calls", err, calls.Load())
	}

	srv, calls = flaky(t, http.StatusServiceUnavailable)
	_, err := newClient(t, srv.URL, "tok").CreateDNSRecord(ctx, "z", in)
	if !cloudflare.IsUncertain(err) || calls.Load() != 1 {
		t.Fatalf("write after 5xx: %v after %d calls, want uncertain after 1", err, calls.Load())
	}

	srv, calls = flaky(t, http.StatusTooManyRequests)
	if _, err := newClient(t, srv.URL, "tok").CreateDNSRecord(ctx, "z", in); err != nil || calls.Load() != 2 {
		t.Fatalf("write after 429: %v after %d calls, want retried once", err, calls.Load())
	}

	// The write reaches Cloudflare but the answer never arrives.
	var hits atomic.Int32
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(cut.Close)
	if _, err := newClient(t, cut.URL, "tok").EditDNSRecord(ctx, "z", "r1", in, &cloudflare.DNSRecord{Type: "A"}); !cloudflare.IsUncertain(err) || hits.Load() != 1 {
		t.Fatalf("write with lost answer: %v after %d calls, want uncertain after 1", err, hits.Load())
	}
	if _, err := newClient(t, cut.URL, "tok").GetZone(ctx, "z"); err == nil || cloudflare.IsUncertain(err) || hits.Load() != 1+4 {
		t.Fatalf("read with lost answers: %v after %d calls, want error after 4 tries", err, hits.Load()-1)
	}
}
