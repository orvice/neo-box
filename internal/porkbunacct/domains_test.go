package porkbunacct

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/porkbun"
	"go.orx.me/apps/neo-box/internal/porkbun/pbtest"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
)

type stubConnections struct {
	cfg     porkbun.ConnectionConfig
	sec     porkbun.ConnectionSecret
	lastErr error
}

func (s *stubConnections) Open(_ *connrepo.Connection, config, secret any) error {
	rawCfg, _ := json.Marshal(s.cfg)
	rawSec, _ := json.Marshal(s.sec)
	if err := json.Unmarshal(rawCfg, config); err != nil {
		return err
	}
	return json.Unmarshal(rawSec, secret)
}

func (s *stubConnections) RecordStatus(_ context.Context, _ string, err error) {
	s.lastErr = err
}

func TestManagerDomainsFilteringAndSummary(t *testing.T) {
	srv := pbtest.New()
	defer srv.Close()
	srv.AddCredential(pbtest.Credential{APIKey: "pk1_live", SecretAPIKey: "sk1_live"})
	srv.AddDomain(&pbtest.Domain{
		Domain:       "soon.com",
		Status:       "ACTIVE",
		TLD:          "com",
		CreateDate:   "2023-01-01 00:00:00",
		ExpireDate:   "2026-06-15 00:00:00",
		SecurityLock: 1,
		WhoisPrivacy: 1,
		AutoRenew:    0,
		NotLocal:     0,
		APIAccess:    1,
		Labels:       []pbtest.Label{{ID: "1", Title: "critical", Color: "#ff0000"}},
	})
	srv.AddDomain(&pbtest.Domain{
		Domain:       "expired.org",
		Status:       "EXPIRED",
		TLD:          "org",
		CreateDate:   "2022-01-01 00:00:00",
		ExpireDate:   "2026-05-01 00:00:00",
		SecurityLock: 0,
		WhoisPrivacy: 1,
		AutoRenew:    0,
		NotLocal:     1,
		APIAccess:    0,
	})
	srv.AddDomain(&pbtest.Domain{
		Domain:       "healthy.dev",
		Status:       "ACTIVE",
		TLD:          "dev",
		CreateDate:   "2024-01-01 00:00:00",
		ExpireDate:   "2028-01-01 00:00:00",
		SecurityLock: 1,
		WhoisPrivacy: 1,
		AutoRenew:    1,
		NotLocal:     1,
		APIAccess:    1,
		Nameservers:  []string{"ava.ns.cloudflare.com", "bob.ns.cloudflare.com"},
	})

	conns := &stubConnections{
		cfg: porkbun.ConnectionConfig{APIKey: "pk1_live"},
		sec: porkbun.ConnectionSecret{SecretAPIKey: "sk1_live"},
	}
	m := New(Config{
		Endpoint:   srv.URL(),
		RetryDelay: time.Millisecond,
		Limiter:    func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) },
	}, conns)
	m.now = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

	conn := &connrepo.Connection{ID: "c1", Provider: porkbun.ProviderType}
	acct, err := m.Open(conn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	items, info, summary, err := acct.Domains(context.Background(), "", FilterAll, 1, 20)
	if err != nil {
		t.Fatalf("Domains: %v", err)
	}
	if len(items) != 3 || info.TotalCount != 3 {
		t.Fatalf("expected 3 items, got %d (%+v)", len(items), info)
	}
	if summary.TotalDomains != 3 || summary.ExpiringSoonCount != 2 || summary.ExpiredCount != 1 ||
		summary.AutoRenewOffCount != 2 || summary.SecurityLockOffCount != 1 ||
		summary.APIAccessOffCount != 1 || summary.ExternalNameserversCount != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	// Filter expiring soon sorts soonest first.
	soon, _, _, err := acct.Domains(context.Background(), "", FilterExpiringSoon, 1, 20)
	if err != nil || len(soon) != 2 || soon[0].Domain != "expired.org" || soon[1].Domain != "soon.com" {
		t.Fatalf("FilterExpiringSoon: %+v err=%v", soon, err)
	}

	// Query matches label title.
	byLabel, _, _, err := acct.Domains(context.Background(), "critical", FilterAll, 1, 20)
	if err != nil || len(byLabel) != 1 || byLabel[0].Domain != "soon.com" {
		t.Fatalf("label search: %+v err=%v", byLabel, err)
	}

	// Domain with API access off still returns metadata + nameservers issue.
	detail, err := acct.Domain(context.Background(), "expired.org")
	if err != nil {
		t.Fatalf("Domain(expired.org): %v", err)
	}
	if detail.Domain.Domain != "expired.org" || detail.NameserversIssue == "" {
		t.Fatalf("expected non-empty NameserversIssue, got %+v", detail)
	}

	// Revoked credentials mark connection error and return CredentialsRejectedError.
	srv.RevokeCredential("pk1_live")
	_, _, _, err = acct.Domains(context.Background(), "", FilterAll, 1, 20)
	var rejected *CredentialsRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("expected CredentialsRejectedError, got %v", err)
	}
	if conns.lastErr == nil {
		t.Fatal("expected RecordStatus to record error")
	}
}
