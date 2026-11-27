package application

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/porkbun/pbtest"
	"go.orx.me/apps/neo-box/internal/porkbunacct"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	connmemory "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type pbFixture struct {
	pb      *pbtest.Server
	conns   *connmemory.Store
	svc     *connection.Service
	connSrv *ConnectionServiceServer
	srv     *PorkbunServiceServer
}

func newPBFixture(t *testing.T) *pbFixture {
	t.Helper()
	pb := pbtest.New()
	t.Cleanup(pb.Close)

	pb.AddCredential(pbtest.Credential{APIKey: "pk1_live", SecretAPIKey: "sk1_live"})
	pb.AddCredential(pbtest.Credential{APIKey: "pk1_sb_test", SecretAPIKey: "sk1_sb_test", Sandbox: true})
	pb.AddCredential(pbtest.Credential{
		APIKey:       "pk1_limited",
		SecretAPIKey: "sk1_limited",
		DeniedPaths:  []string{"/dns/retrieve/"},
	})

	pb.AddDomain(&pbtest.Domain{
		Domain:       "alpha.com",
		Status:       "ACTIVE",
		TLD:          "com",
		CreateDate:   "2024-01-15 00:00:00",
		ExpireDate:   "2028-01-15 00:00:00",
		SecurityLock: 1,
		WhoisPrivacy: 1,
		AutoRenew:    1,
		NotLocal:     0,
		APIAccess:    1,
		Labels:       []pbtest.Label{{ID: "1", Title: "main", Color: "#22c55e"}},
		Nameservers:  []string{"curitiba.ns.porkbun.com", "maceio.ns.porkbun.com"},
		DNSRecords: []pbtest.DNSRecord{
			{ID: "1", Name: "alpha.com", Type: "A", Content: "198.51.100.1", TTL: "600", Notes: "web"},
			{ID: "2", Name: "www.alpha.com", Type: "CNAME", Content: "alpha.com", TTL: "600"},
			{ID: "3", Name: "alpha.com", Type: "TXT", Content: "v=spf1 -all", TTL: "3600"},
		},
		URLForwards: []pbtest.URLForward{
			{ID: "f1", Subdomain: "docs", Location: "https://docs.alpha.org", Type: "permanent", RedirectType: "301", IncludePath: "1", Wildcard: "0"},
		},
		DNSSEC: []pbtest.DNSSECRecord{
			{KeyTag: "64087", Alg: "13", DigestType: "2", Digest: "DEADBEEF"},
		},
		GlueHosts: []pbtest.GlueHost{
			{Host: "ns1.alpha.com", V4: []string{"192.0.2.10"}, V6: []string{"2001:db8::10"}},
		},
	})
	pb.AddDomain(&pbtest.Domain{
		Domain:       "bravo.net",
		Status:       "ACTIVE",
		TLD:          "net",
		CreateDate:   "2023-01-01 00:00:00",
		ExpireDate:   "2029-01-01 00:00:00",
		SecurityLock: 1,
		WhoisPrivacy: 1,
		AutoRenew:    0,
		NotLocal:     1,
		APIAccess:    0,
	})

	cipher, _ := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	f := &pbFixture{pb: pb, conns: connmemory.New()}
	f.svc = connection.NewService(f.conns, cipher)
	m := porkbunacct.New(porkbunacct.Config{
		Endpoint:   pb.URL(),
		RetryDelay: time.Millisecond,
		Limiter:    func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) },
	}, f.svc)
	f.svc.Register(m.ConnectionProvider())

	f.connSrv = NewConnectionServiceServer()
	f.connSrv.SetService(f.svc)
	f.srv = NewPorkbunServiceServer()
	f.srv.SetDeps(m, f.svc)
	return f
}

func (f *pbFixture) createConn(t *testing.T, userID, name, apiKey, secret string) *neoboxv1.Connection {
	t.Helper()
	res, err := f.connSrv.CreateConnection(asUser(userID), &neoboxv1.CreateConnectionRequest{
		Name: name,
		Settings: &neoboxv1.CreateConnectionRequest_Porkbun{
			Porkbun: &neoboxv1.PorkbunConnectionSettings{ApiKey: apiKey, SecretApiKey: secret},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	return res.GetConnection()
}

func TestPorkbunConnectionCreateVerifyAndReadRPCs(t *testing.T) {
	f := newPBFixture(t)
	ctx := asUser("u1")

	// Reject bad credentials during create.
	_, err := f.connSrv.CreateConnection(ctx, &neoboxv1.CreateConnectionRequest{
		Name: "bad",
		Settings: &neoboxv1.CreateConnectionRequest_Porkbun{
			Porkbun: &neoboxv1.PorkbunConnectionSettings{ApiKey: "pk1_live", SecretApiKey: "wrong"},
		},
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument on bad keys, got %v", err)
	}

	// Create live & sandbox connections.
	liveConn := f.createConn(t, "u1", "Porkbun Live", "pk1_live", "sk1_live")
	if liveConn.GetProvider() != neoboxv1.Provider_PROVIDER_PORKBUN ||
		liveConn.GetPorkbun().GetApiKey() != "pk1_live" ||
		liveConn.GetPorkbun().GetIsSandbox() {
		t.Fatalf("unexpected live connection: %+v", liveConn)
	}

	sbConn := f.createConn(t, "u1", "Porkbun Sandbox", "pk1_sb_test", "sk1_sb_test")
	if !sbConn.GetPorkbun().GetIsSandbox() {
		t.Fatalf("expected sandbox flag on pk1_sb_ key: %+v", sbConn)
	}

	// List domains + summary.
	listResp, err := f.srv.ListPorkbunDomains(ctx, &neoboxv1.ListPorkbunDomainsRequest{ConnectionId: liveConn.GetId()})
	if err != nil {
		t.Fatalf("ListPorkbunDomains: %v", err)
	}
	if len(listResp.GetDomains()) != 2 || listResp.GetSummary().GetTotalDomains() != 2 || listResp.GetSummary().GetAutoRenewOffCount() != 1 {
		t.Fatalf("unexpected ListPorkbunDomains: %+v", listResp)
	}

	// Other user cannot access u1's connection.
	_, err = f.srv.ListPorkbunDomains(asUser("u2"), &neoboxv1.ListPorkbunDomainsRequest{ConnectionId: liveConn.GetId()})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound for another user, got %v", err)
	}

	// Get domain detail (API access enabled).
	domResp, err := f.srv.GetPorkbunDomain(ctx, &neoboxv1.GetPorkbunDomainRequest{
		ConnectionId: liveConn.GetId(),
		Domain:       "alpha.com",
	})
	if err != nil {
		t.Fatalf("GetPorkbunDomain: %v", err)
	}
	if !domResp.GetUsesPorkbunNameservers() || len(domResp.GetNameservers()) != 2 || domResp.GetNameserversIssue() != "" {
		t.Fatalf("unexpected GetPorkbunDomain: %+v", domResp)
	}

	// Get domain detail when API access is off: still succeeds with nameservers_issue.
	bravoResp, err := f.srv.GetPorkbunDomain(ctx, &neoboxv1.GetPorkbunDomainRequest{
		ConnectionId: liveConn.GetId(),
		Domain:       "bravo.net",
	})
	if err != nil {
		t.Fatalf("GetPorkbunDomain(bravo.net): %v", err)
	}
	if bravoResp.GetNameserversIssue() == "" {
		t.Fatalf("expected nameservers_issue for domain without API access: %+v", bravoResp)
	}

	// List DNS records with filter.
	dnsResp, err := f.srv.ListPorkbunDNSRecords(ctx, &neoboxv1.ListPorkbunDNSRecordsRequest{
		ConnectionId: liveConn.GetId(),
		Domain:       "alpha.com",
		Type:         "A",
	})
	if err != nil {
		t.Fatalf("ListPorkbunDNSRecords: %v", err)
	}
	if len(dnsResp.GetRecords()) != 1 || len(dnsResp.GetRecordTypes()) != 3 {
		t.Fatalf("unexpected DNS records: %+v", dnsResp)
	}

	// URL forwards, DNSSEC, Glue.
	fwds, err := f.srv.ListPorkbunURLForwards(ctx, &neoboxv1.ListPorkbunURLForwardsRequest{ConnectionId: liveConn.GetId(), Domain: "alpha.com"})
	if err != nil || len(fwds.GetForwards()) != 1 || fwds.GetForwards()[0].GetType() != "301" {
		t.Fatalf("ListPorkbunURLForwards: %+v err=%v", fwds, err)
	}
	ds, err := f.srv.ListPorkbunDNSSECRecords(ctx, &neoboxv1.ListPorkbunDNSSECRecordsRequest{ConnectionId: liveConn.GetId(), Domain: "alpha.com"})
	if err != nil || len(ds.GetRecords()) != 1 || ds.GetRecords()[0].GetKeyTag() != "64087" {
		t.Fatalf("ListPorkbunDNSSECRecords: %+v err=%v", ds, err)
	}
	glue, err := f.srv.ListPorkbunGlueRecords(ctx, &neoboxv1.ListPorkbunGlueRecordsRequest{ConnectionId: liveConn.GetId(), Domain: "alpha.com"})
	if err != nil || len(glue.GetHosts()) != 1 || glue.GetHosts()[0].GetHost() != "ns1.alpha.com" {
		t.Fatalf("ListPorkbunGlueRecords: %+v err=%v", glue, err)
	}

	// Missing permission returns PermissionDenied.
	limitedConn := f.createConn(t, "u1", "Limited", "pk1_limited", "sk1_limited")
	_, err = f.srv.ListPorkbunDNSRecords(ctx, &neoboxv1.ListPorkbunDNSRecordsRequest{
		ConnectionId: limitedConn.GetId(),
		Domain:       "alpha.com",
	})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	// Revoked credentials become FailedPrecondition (never Unauthenticated!) and mark connection error.
	f.pb.RevokeCredential("pk1_live")
	_, err = f.srv.ListPorkbunDomains(ctx, &neoboxv1.ListPorkbunDomainsRequest{ConnectionId: liveConn.GetId()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition on revoked keys, got %v", err)
	}
	stored, err := f.conns.Get(ctx, "u1", liveConn.GetId())
	if err != nil || stored.Status != connrepo.StatusError {
		t.Fatalf("expected connection status error, got %+v err=%v", stored, err)
	}
}
