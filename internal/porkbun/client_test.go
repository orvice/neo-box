package porkbun_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/porkbun"
	"go.orx.me/apps/neo-box/internal/porkbun/pbtest"
)

func fastClient(t *testing.T, endpoint, apiKey, secret string) *porkbun.Client {
	t.Helper()
	c, err := porkbun.New(apiKey, secret,
		porkbun.WithEndpoint(endpoint),
		porkbun.WithLimiter(rate.NewLimiter(rate.Inf, 1)),
		porkbun.WithRetryDelay(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("porkbun.New: %v", err)
	}
	return c
}

func TestPingAndSandboxDetection(t *testing.T) {
	srv := pbtest.New()
	defer srv.Close()
	srv.AddCredential(pbtest.Credential{APIKey: "pk1_live", SecretAPIKey: "sk1_live"})
	srv.AddCredential(pbtest.Credential{APIKey: "pk1_sb_demo", SecretAPIKey: "sk1_sb_demo", Sandbox: true})

	live := fastClient(t, srv.URL(), "pk1_live", "sk1_live")
	res, err := live.Ping(context.Background())
	if err != nil {
		t.Fatalf("live Ping: %v", err)
	}
	if res.YourIP != "203.0.113.10" || res.Sandbox {
		t.Fatalf("unexpected live PingResult: %+v", res)
	}

	sb := fastClient(t, srv.URL(), "pk1_sb_demo", "sk1_sb_demo")
	res, err = sb.Ping(context.Background())
	if err != nil {
		t.Fatalf("sandbox Ping: %v", err)
	}
	if !res.Sandbox {
		t.Fatalf("expected Sandbox=true, got %+v", res)
	}

	bad := fastClient(t, srv.URL(), "pk1_live", "sk1_wrong")
	if _, err := bad.Ping(context.Background()); !porkbun.IsAuthError(err) {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestListDomainsPaginationAndFields(t *testing.T) {
	srv := pbtest.New()
	defer srv.Close()
	srv.AddCredential(pbtest.Credential{APIKey: "pk1_k", SecretAPIKey: "sk1_s"})

	// Insert 1005 domains to verify automatic pagination across 1000-item pages.
	for i := 0; i < 1005; i++ {
		srv.AddDomain(&pbtest.Domain{
			Domain:       fmt.Sprintf("site-%04d.com", i),
			Status:       "ACTIVE",
			TLD:          "com",
			CreateDate:   "2024-01-10 00:00:00",
			ExpireDate:   "2027-01-10 00:00:00",
			SecurityLock: 1,
			WhoisPrivacy: 1,
			AutoRenew:    i % 2,
			NotLocal:     0,
			APIAccess:    1,
			Labels:       []pbtest.Label{{ID: "1", Title: "prod", Color: "#00aa00"}},
		})
	}

	c := fastClient(t, srv.URL(), "pk1_k", "sk1_s")
	domains, err := c.ListDomains(context.Background())
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1005 {
		t.Fatalf("expected 1005 domains, got %d", len(domains))
	}
	if domains[0].Domain != "site-0000.com" || !domains[0].SecurityLock || domains[0].AutoRenew {
		t.Fatalf("unexpected first domain: %+v", domains[0])
	}
	if domains[0].ExpiresAt.Year() != 2027 || len(domains[0].Labels) != 1 || domains[0].Labels[0].Title != "prod" {
		t.Fatalf("unexpected dates/labels: %+v", domains[0])
	}
}

func TestDomainReadEndpointsAndShapes(t *testing.T) {
	srv := pbtest.New()
	defer srv.Close()
	srv.AddCredential(pbtest.Credential{APIKey: "pk1_k", SecretAPIKey: "sk1_s"})
	srv.AddDomain(&pbtest.Domain{
		Domain:      "example.com",
		Status:      "ACTIVE",
		TLD:         "com",
		APIAccess:   1,
		Nameservers: []string{"curitiba.ns.porkbun.com", "fortaleza.ns.porkbun.com"},
		DNSRecords: []pbtest.DNSRecord{
			{ID: "101", Name: "example.com", Type: "a", Content: "198.51.100.1", TTL: "600", Prio: "0", Notes: "apex"},
			{ID: "102", Name: "example.com", Type: "MX", Content: "mail.example.com", TTL: "3600", Prio: "10"},
		},
		URLForwards: []pbtest.URLForward{
			{ID: "201", Subdomain: "blog", Location: "https://blog.example.org", Type: "permanent", RedirectType: "301", IncludePath: "1", Wildcard: "0"},
		},
		DNSSEC: []pbtest.DNSSECRecord{
			{KeyTag: "64087", Alg: "13", DigestType: "2", Digest: "ABCDEF"},
			{KeyTag: "12345", Alg: "8", DigestType: "2", Digest: "112233"},
		},
		GlueHosts: []pbtest.GlueHost{
			{Host: "ns2.example.com", V4: []string{"192.0.2.2"}, V6: []string{"2001:db8::2"}},
			{Host: "ns1.example.com", V4: []string{"192.0.2.1"}},
		},
	})
	srv.AddDomain(&pbtest.Domain{
		Domain:    "locked-api.com",
		Status:    "ACTIVE",
		TLD:       "com",
		APIAccess: 0,
	})

	c := fastClient(t, srv.URL(), "pk1_k", "sk1_s")
	ctx := context.Background()

	ns, err := c.GetNameservers(ctx, "example.com")
	if err != nil || len(ns) != 2 || !porkbun.UsesPorkbunNameservers(ns) {
		t.Fatalf("GetNameservers: ns=%v err=%v", ns, err)
	}

	recs, err := c.ListDNSRecords(ctx, "example.com")
	if err != nil || len(recs) != 2 || recs[0].Type != "A" || recs[0].TTL != 600 || recs[0].Notes != "apex" {
		t.Fatalf("ListDNSRecords: recs=%+v err=%v", recs, err)
	}

	fwds, err := c.ListURLForwards(ctx, "example.com")
	if err != nil || len(fwds) != 1 || fwds[0].Type != "301" || !fwds[0].IncludePath || fwds[0].Wildcard {
		t.Fatalf("ListURLForwards: fwds=%+v err=%v", fwds, err)
	}

	// Map-shaped DNSSEC response (default).
	ds, err := c.ListDNSSECRecords(ctx, "example.com")
	if err != nil || len(ds) != 2 || ds[0].KeyTag != "12345" || ds[1].KeyTag != "64087" {
		t.Fatalf("ListDNSSECRecords (map): ds=%+v err=%v", ds, err)
	}
	// Array-shaped DNSSEC response.
	srv.SetArrayDNSSEC(true)
	ds, err = c.ListDNSSECRecords(ctx, "example.com")
	if err != nil || len(ds) != 2 || ds[0].Algorithm != "8" {
		t.Fatalf("ListDNSSECRecords (array): ds=%+v err=%v", ds, err)
	}

	// Object-shaped Glue response (default).
	glue, err := c.ListGlueRecords(ctx, "example.com")
	if err != nil || len(glue) != 2 || glue[0].Host != "ns1.example.com" || glue[1].IPv6[0] != "2001:db8::2" {
		t.Fatalf("ListGlueRecords (object): glue=%+v err=%v", glue, err)
	}
	// Classic tuple-shaped Glue response.
	srv.SetTupleGlue(true)
	glue, err = c.ListGlueRecords(ctx, "example.com")
	if err != nil || len(glue) != 2 || glue[0].Host != "ns1.example.com" || glue[1].IPv4[0] != "192.0.2.2" {
		t.Fatalf("ListGlueRecords (tuple): glue=%+v err=%v", glue, err)
	}

	// Domain with API access disabled.
	if _, err := c.ListDNSRecords(ctx, "locked-api.com"); !porkbun.IsAPIAccessDisabled(err) {
		t.Fatalf("expected APIAccessDisabled error, got %v", err)
	}
}

func TestRateLimitRetryAndErrorClassification(t *testing.T) {
	srv := pbtest.New()
	defer srv.Close()
	srv.AddCredential(pbtest.Credential{
		APIKey:       "pk1_k",
		SecretAPIKey: "sk1_s",
		DeniedPaths:  []string{"/dns/retrieve/"},
	})
	srv.AddDomain(&pbtest.Domain{Domain: "example.com", Status: "ACTIVE", APIAccess: 1})

	// Retry 429 twice and succeed on third attempt.
	srv.FailRateLimit(2)
	c := fastClient(t, srv.URL(), "pk1_k", "sk1_s")
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("expected Ping to recover from 429, got %v", err)
	}

	// 403 scope error should classify as permission denied, not auth error.
	_, err := c.ListDNSRecords(context.Background(), "example.com")
	if !porkbun.IsPermissionDenied(err) || porkbun.IsAuthError(err) {
		t.Fatalf("expected permission denied error, got %v", err)
	}
}

func TestLegacyHTTP400AuthErrorAndStringFlags(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":"ERROR","message":"Invalid API key."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status":"SUCCESS",
			"domains":[{
				"domain":"mixed.dev",
				"status":"ACTIVE",
				"tld":"dev",
				"createDate":"2023-05-01 00:00:00",
				"expireDate":"2026-05-01 00:00:00",
				"securityLock":"1",
				"whoisPrivacy":"0",
				"autoRenew":"1",
				"notLocal":"1",
				"apiAccess":"0"
			}]
		}`))
	}))
	defer ts.Close()

	c := fastClient(t, ts.URL, "pk1_any", "sk1_any")
	if _, err := c.Ping(context.Background()); !porkbun.IsAuthError(err) {
		t.Fatalf("expected HTTP 400 Invalid API key to classify as IsAuthError, got %v", err)
	}

	domains, err := c.ListDomains(context.Background())
	if err != nil || len(domains) != 1 {
		t.Fatalf("ListDomains: %+v err=%v", domains, err)
	}
	d := domains[0]
	if !d.SecurityLock || d.WhoisPrivacy || !d.AutoRenew || !d.NotLocal || d.APIAccess {
		t.Fatalf("unexpected string-flag parsing: %+v", d)
	}
}

func TestUsesPorkbunNameservers(t *testing.T) {
	if !porkbun.UsesPorkbunNameservers([]string{"curitiba.ns.porkbun.com.", "maceio.ns.porkbun.com"}) {
		t.Fatal("expected Porkbun nameservers to match")
	}
	if porkbun.UsesPorkbunNameservers([]string{"ava.ns.cloudflare.com", "bob.ns.cloudflare.com"}) {
		t.Fatal("expected Cloudflare nameservers not to match")
	}
	if porkbun.UsesPorkbunNameservers(nil) {
		t.Fatal("expected empty nameservers to return false")
	}
}
