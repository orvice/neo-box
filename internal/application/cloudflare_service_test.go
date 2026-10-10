package application

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	cfmemory "go.orx.me/apps/neo-box/internal/repo/cloudflare/memory"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	connmemory "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	cfAccA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfAccB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// cfFixture is the Cloudflare provider wired like bootstrap does, against a
// fake Cloudflare API.
//
// Tokens: "full" reaches accounts A and B with every permission; "dns-read"
// only reads Zones and DNS in A; "zones-only" reads Zones and edits DNS in
// A but has no Workers or Pages permission; "acct-owned" is owned by A.
type cfFixture struct {
	cf      *cftest.Server
	ops     *cfmemory.Store
	conns   *connmemory.Store
	svc     *connection.Service
	connSrv *ConnectionServiceServer
	srv     *CloudflareServiceServer
}

func newCFFixture(t *testing.T) *cfFixture {
	t.Helper()
	cf := cftest.New()
	t.Cleanup(cf.Close)
	cf.AddAccount(cfAccA, "Account A")
	cf.AddAccount(cfAccB, "Account B")
	all := []string{
		cftest.PermAccountRead, cftest.PermZoneRead, cftest.PermDNSRead, cftest.PermDNSEdit,
		cftest.PermWorkersRead, cftest.PermRoutesRead, cftest.PermPagesRead,
	}
	cf.AddToken("full", &cftest.Token{Accounts: []string{cfAccA, cfAccB}, Perms: all,
		ZonePermissions: []string{"#zone:read", "#dns_records:read", "#dns_records:edit"}})
	cf.AddToken("dns-read", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermZoneRead, cftest.PermDNSRead},
		ZonePermissions: []string{"#zone:read", "#dns_records:read"}})
	cf.AddToken("zones-only", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermZoneRead, cftest.PermDNSEdit}})
	cf.AddToken("acct-owned", &cftest.Token{OwnerAccount: cfAccA, Accounts: []string{cfAccA}, Perms: []string{cftest.PermPagesRead}})
	cf.AddZone(&cftest.Zone{ID: "za1", Name: "alpha.com", Account: cfAccA})
	cf.AddZone(&cftest.Zone{ID: "za2", Name: "bravo.com", Account: cfAccA})
	cf.AddZone(&cftest.Zone{ID: "za3", Name: "charlie.net", Account: cfAccA})
	cf.AddZone(&cftest.Zone{ID: "zb1", Name: "other-account.com", Account: cfAccB})

	cipher, _ := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	f := &cfFixture{cf: cf, ops: cfmemory.New(), conns: connmemory.New()}
	f.svc = connection.NewService(f.conns, cipher)
	m := cloudflareacct.New(cloudflareacct.Config{Endpoint: cf.URL, RetryDelay: time.Millisecond}, f.ops, f.svc)
	f.svc.Register(m.ConnectionProvider())
	f.connSrv = NewConnectionServiceServer()
	f.connSrv.SetService(f.svc)
	f.srv = NewCloudflareServiceServer()
	f.srv.SetDeps(m, f.ops, f.svc)
	return f
}

func cfCreate(name, account, token string) *connect.Request[neoboxv1.CreateConnectionRequest] {
	return connect.NewRequest(&neoboxv1.CreateConnectionRequest{
		Name: name, Settings: &neoboxv1.CreateConnectionRequest_Cloudflare{Cloudflare: &neoboxv1.CloudflareConnectionSettings{
			AccountId: account, ApiToken: token,
		}},
	})
}

// connect creates a connection for user and returns its ID.
func (f *cfFixture) connect(t *testing.T, user, account, token string) string {
	t.Helper()
	resp, err := f.connSrv.CreateConnection(asUser(user), cfCreate("cf "+token, account, token))
	if err != nil {
		t.Fatalf("CreateConnection(%s): %v", token, err)
	}
	return resp.Msg.GetConnection().GetId()
}

func TestCloudflareConnectionSettings(t *testing.T) {
	f := newCFFixture(t)
	ctx := asUser("u1")

	for name, req := range map[string]*connect.Request[neoboxv1.CreateConnectionRequest]{
		"no account":        cfCreate("cf", "", "full"),
		"malformed account": cfCreate("cf", "not-an-account", "full"),
		"no token":          cfCreate("cf", cfAccA, ""),
		"invalid token":     cfCreate("cf", cfAccA, "revoked"),
		"other account":     cfCreate("cf", cfAccB, "dns-read"),
		"unknown account":   cfCreate("cf", "cccccccccccccccccccccccccccccccc", "full"),
	} {
		if _, err := f.connSrv.CreateConnection(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
	if len(f.cf.Writes()) != 0 {
		t.Fatalf("verification wrote to Cloudflare: %+v", f.cf.Writes())
	}

	// Partial permissions are enough; so is an account-owned token.
	for _, tok := range []string{"dns-read", "zones-only", "acct-owned"} {
		f.connect(t, "u1", cfAccA, tok)
	}

	created, err := f.connSrv.CreateConnection(ctx, cfCreate("Main", " "+strings.ToUpper(cfAccA)+" ", "full"))
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	c := created.Msg.GetConnection()
	if c.GetProvider() != neoboxv1.Provider_PROVIDER_CLOUDFLARE || c.GetCloudflare().GetAccountId() != cfAccA ||
		c.GetStatus() != neoboxv1.ConnectionStatus_CONNECTION_STATUS_OK {
		t.Fatalf("connection = %+v", c)
	}
	raw, _ := protojson.Marshal(created.Msg)
	if strings.Contains(string(raw), `"full"`) {
		t.Fatalf("response leaks the token: %s", raw)
	}

	// Renaming without a token keeps the stored one, which still verifies.
	upd, err := f.connSrv.UpdateConnection(ctx, connect.NewRequest(&neoboxv1.UpdateConnectionRequest{
		Id: c.GetId(), Name: "Renamed",
		Settings: &neoboxv1.UpdateConnectionRequest_Cloudflare{Cloudflare: &neoboxv1.CloudflareConnectionSettings{AccountId: cfAccA}},
	}))
	if err != nil || upd.Msg.GetConnection().GetName() != "Renamed" {
		t.Fatalf("UpdateConnection = %v, %v", upd, err)
	}
	test, err := f.connSrv.TestConnection(ctx, connect.NewRequest(&neoboxv1.TestConnectionRequest{Id: c.GetId()}))
	if err != nil || test.Msg.GetConnection().GetStatus() != neoboxv1.ConnectionStatus_CONNECTION_STATUS_OK {
		t.Fatalf("TestConnection = %v, %v", test, err)
	}

	list, err := f.connSrv.ListConnections(ctx, connect.NewRequest(&neoboxv1.ListConnectionsRequest{Provider: neoboxv1.Provider_PROVIDER_CLOUDFLARE}))
	if err != nil || len(list.Msg.GetConnections()) != 4 {
		t.Fatalf("ListConnections(cloudflare) = %d, %v", len(list.Msg.GetConnections()), err)
	}
}

func TestCloudflareZonesAreScopedToTheAccount(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full") // the token also reaches account B
	ctx := asUser("u1")

	page, err := f.srv.ListCloudflareZones(ctx, connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: conn, PageSize: 5}))
	if err != nil {
		t.Fatalf("ListCloudflareZones: %v", err)
	}
	var names []string
	for _, z := range page.Msg.GetZones() {
		names = append(names, z.GetName())
	}
	if strings.Join(names, ",") != "alpha.com,bravo.com,charlie.net" || page.Msg.GetPageInfo().GetTotalCount() != 3 {
		t.Fatalf("zones = %v (%+v)", names, page.Msg.GetPageInfo())
	}
	z := page.Msg.GetZones()[0]
	if z.GetId() != "za1" || z.GetStatus() != "active" || len(z.GetNameServers()) != 2 ||
		z.GetDnsAccess() != neoboxv1.CloudflareRecordAccess_CLOUDFLARE_RECORD_ACCESS_UNKNOWN {
		t.Fatalf("zone = %+v", z)
	}

	found, err := f.srv.ListCloudflareZones(ctx, connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: conn, Query: ".com"}))
	if err != nil || len(found.Msg.GetZones()) != 2 || found.Msg.GetPageInfo().GetTotalCount() != 2 {
		t.Fatalf("search .com = %+v, %v", found, err)
	}

	got, err := f.srv.GetCloudflareZone(ctx, connect.NewRequest(&neoboxv1.GetCloudflareZoneRequest{ConnectionId: conn, ZoneId: "za2"}))
	if err != nil || got.Msg.GetZone().GetName() != "bravo.com" {
		t.Fatalf("GetCloudflareZone = %v, %v", got, err)
	}
	// A Zone of account B is out of reach, though the token can see it.
	if _, err := f.srv.GetCloudflareZone(ctx, connect.NewRequest(&neoboxv1.GetCloudflareZoneRequest{ConnectionId: conn, ZoneId: "zb1"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("zone of another account: %v, want NotFound", err)
	}

	wasabiConn := "w-1"
	now := time.Now()
	_ = f.conns.Create(context.Background(), &connrepo.Connection{ID: wasabiConn, UserID: "u1", Provider: "wasabi", Config: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now})
	for what, call := range map[string]func() error{
		"another user's connection": func() error {
			_, err := f.srv.ListCloudflareZones(asUser("u2"), connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: conn}))
			return err
		},
		"a Wasabi connection": func() error {
			_, err := f.srv.ListCloudflareZones(ctx, connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: wasabiConn}))
			return err
		},
	} {
		if err := call(); connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("%s: %v, want NotFound", what, err)
		}
	}

	readOnly := f.connect(t, "u1", cfAccA, "dns-read")
	ro, err := f.srv.GetCloudflareZone(ctx, connect.NewRequest(&neoboxv1.GetCloudflareZoneRequest{ConnectionId: readOnly, ZoneId: "za1"}))
	if err != nil || ro.Msg.GetZone().GetDnsAccess() != neoboxv1.CloudflareRecordAccess_CLOUDFLARE_RECORD_ACCESS_READ {
		t.Fatalf("read-only zone = %v, %v", ro, err)
	}
}

func TestCloudflareRevokedTokenMarksTheConnection(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddToken("short-lived", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermZoneRead}})
	conn := f.connect(t, "u1", cfAccA, "short-lived")
	f.cf.AddToken("short-lived", &cftest.Token{Status: "disabled"})

	_, err := f.srv.ListCloudflareZones(asUser("u1"), connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: conn}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ListCloudflareZones with a revoked token: %v, want FailedPrecondition", err)
	}
	c, _ := f.conns.Get(context.Background(), "u1", conn)
	if c.Status != connrepo.StatusError || c.StatusMessage == "" {
		t.Fatalf("connection status = %q %q", c.Status, c.StatusMessage)
	}
}

func dnsInput(typ, name, content string) *neoboxv1.CloudflareDNSRecordInput {
	return &neoboxv1.CloudflareDNSRecordInput{Type: typ, Name: name, Content: content, Ttl: 1}
}

func TestCloudflareDNSRecordsAndChanges(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := auth.WithAuthenticated(context.Background(), &neoboxv1.User{Id: "u1", Username: "ada", DisplayName: "Ada"}, &auth.Session{ID: "s", UserID: "u1"})
	mxID := f.cf.AddRecord("za1", map[string]any{
		"type": "MX", "name": "alpha.com", "content": "mx1.alpha.com", "priority": 10, "ttl": 3600,
		"comment": "primary mail", "tags": []string{"team:mail"}, "settings": map[string]any{"flatten_cname": false},
	})
	httpsID := f.cf.AddRecord("za1", map[string]any{"type": "HTTPS", "name": "svc.alpha.com", "content": `1 . alpn="h2"`})
	txtID := f.cf.AddRecord("za1", map[string]any{"type": "TXT", "name": "alpha.com", "content": "v=spf1 -all"})
	otherZoneRec := f.cf.AddRecord("za2", map[string]any{"type": "A", "name": "www.bravo.com", "content": "192.0.2.50"})
	otherAccountRec := f.cf.AddRecord("zb1", map[string]any{"type": "A", "name": "www.other-account.com", "content": "192.0.2.60"})

	list, err := f.srv.ListCloudflareDNSRecords(ctx, connect.NewRequest(&neoboxv1.ListCloudflareDNSRecordsRequest{ConnectionId: conn, ZoneId: "za1"}))
	if err != nil || len(list.Msg.GetRecords()) != 3 || list.Msg.GetPageInfo().GetTotalCount() != 3 {
		t.Fatalf("ListCloudflareDNSRecords = %+v, %v", list, err)
	}
	for _, r := range list.Msg.GetRecords() {
		if want := r.GetType() != "HTTPS"; r.GetEditable() != want {
			t.Errorf("%s editable = %v", r.GetType(), r.GetEditable())
		}
		if r.GetType() == "MX" && (r.GetPriority() != 10 || r.GetComment() != "primary mail" || r.GetTtl() != 3600) {
			t.Errorf("MX = %+v", r)
		}
	}
	found, err := f.srv.ListCloudflareDNSRecords(ctx, connect.NewRequest(&neoboxv1.ListCloudflareDNSRecordsRequest{ConnectionId: conn, ZoneId: "za1", Query: "spf"}))
	if err != nil || len(found.Msg.GetRecords()) != 1 || found.Msg.GetRecords()[0].GetId() != txtID {
		t.Fatalf("search spf = %+v, %v", found, err)
	}
	if _, err := f.srv.ListCloudflareDNSRecords(ctx, connect.NewRequest(&neoboxv1.ListCloudflareDNSRecordsRequest{ConnectionId: conn, ZoneId: "zb1"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("records of another account's zone: %v, want NotFound", err)
	}

	// Create.
	a := dnsInput("A", "www.alpha.com", "192.0.2.1")
	a.Proxied = true
	created, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: a}))
	if err != nil {
		t.Fatalf("CreateCloudflareDNSRecord: %v", err)
	}
	newID := created.Msg.GetRecord().GetId()
	op := created.Msg.GetOperation()
	if newID == "" || !created.Msg.GetRecord().GetProxied() || op.GetStatus() != neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_SUCCEEDED ||
		op.GetAction() != neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_CREATE || op.GetRecordId() != newID || op.GetBefore() != nil ||
		op.GetAfter().GetContent() != "192.0.2.1" || op.GetActorId() != "u1" || op.GetActorName() != "Ada" ||
		op.GetAccountId() != cfAccA || op.GetZoneName() != "alpha.com" || created.Msg.GetOperationLogError() != "" {
		t.Fatalf("create = %+v", created.Msg)
	}

	// Update keeps what the form doesn't show.
	mx := dnsInput("MX", "alpha.com", "mx2.alpha.com")
	mx.Priority, mx.Ttl = 20, 300
	updated, err := f.srv.UpdateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.UpdateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: mxID, Record: mx}))
	if err != nil {
		t.Fatalf("UpdateCloudflareDNSRecord: %v", err)
	}
	if r := updated.Msg.GetRecord(); r.GetContent() != "mx2.alpha.com" || r.GetPriority() != 20 || r.GetComment() != "primary mail" || len(r.GetTags()) != 1 {
		t.Fatalf("updated = %+v", r)
	}
	if stored := f.cf.Record("za1", mxID); stored["settings"] == nil {
		t.Fatalf("settings lost: %+v", stored)
	}
	if op := updated.Msg.GetOperation(); op.GetBefore().GetContent() != "mx1.alpha.com" || op.GetAfter().GetContent() != "mx2.alpha.com" {
		t.Fatalf("update operation = %+v", op)
	}

	// Proxy toggle, then delete.
	proxied, err := f.srv.SetCloudflareDNSRecordProxied(ctx, connect.NewRequest(&neoboxv1.SetCloudflareDNSRecordProxiedRequest{ConnectionId: conn, ZoneId: "za1", RecordId: newID, Proxied: false}))
	if err != nil || proxied.Msg.GetRecord().GetProxied() || proxied.Msg.GetRecord().GetContent() != "192.0.2.1" ||
		proxied.Msg.GetOperation().GetAction() != neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_SET_PROXIED {
		t.Fatalf("SetCloudflareDNSRecordProxied = %+v, %v", proxied, err)
	}
	deleted, err := f.srv.DeleteCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: newID}))
	if err != nil || deleted.Msg.GetOperation().GetBefore().GetName() != "www.alpha.com" || deleted.Msg.GetOperation().GetAfter() != nil {
		t.Fatalf("DeleteCloudflareDNSRecord = %+v, %v", deleted, err)
	}
	if f.cf.Record("za1", newID) != nil {
		t.Fatal("record still at Cloudflare")
	}
	writes := len(f.cf.Writes())

	// Refused before anything is sent.
	for what, call := range map[string]func() error{
		"invalid IPv4": func() error {
			_, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: dnsInput("A", "x.alpha.com", "300.1.1.1")}))
			return err
		},
		"unsupported type": func() error {
			_, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: dnsInput("HTTPS", "x.alpha.com", "1 . alpn=h2")}))
			return err
		},
		"bad TTL": func() error {
			in := dnsInput("TXT", "x.alpha.com", "hello")
			in.Ttl = 5
			_, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: in}))
			return err
		},
	} {
		if err := call(); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want InvalidArgument", what, err)
		}
	}
	for what, call := range map[string]func() (connect.Code, error){
		"a record of another zone": func() (connect.Code, error) {
			_, err := f.srv.DeleteCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: otherZoneRec}))
			return connect.CodeNotFound, err
		},
		"a zone of another account": func() (connect.Code, error) {
			_, err := f.srv.DeleteCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "zb1", RecordId: otherAccountRec}))
			return connect.CodeNotFound, err
		},
		"creating in a zone of another account": func() (connect.Code, error) {
			_, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "zb1", Record: dnsInput("A", "x.other-account.com", "192.0.2.7")}))
			return connect.CodeNotFound, err
		},
		"a type Neo Box doesn't edit": func() (connect.Code, error) {
			_, err := f.srv.DeleteCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: httpsID}))
			return connect.CodeFailedPrecondition, err
		},
		"proxying a TXT record": func() (connect.Code, error) {
			_, err := f.srv.SetCloudflareDNSRecordProxied(ctx, connect.NewRequest(&neoboxv1.SetCloudflareDNSRecordProxiedRequest{ConnectionId: conn, ZoneId: "za1", RecordId: txtID, Proxied: true}))
			return connect.CodeFailedPrecondition, err
		},
		"changing the type": func() (connect.Code, error) {
			_, err := f.srv.UpdateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.UpdateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: txtID, Record: dnsInput("CNAME", "alpha.com", "x.example.com")}))
			return connect.CodeInvalidArgument, err
		},
	} {
		if want, err := call(); connect.CodeOf(err) != want {
			t.Errorf("%s: %v, want %v", what, err, want)
		}
	}
	if n := len(f.cf.Writes()); n != writes {
		t.Fatalf("refused changes reached Cloudflare: %+v", f.cf.Writes()[writes:])
	}

	// Cloudflare's own refusal is shown and logged as failed.
	low := dnsInput("TXT", "x.alpha.com", "hello")
	low.Ttl = 45
	_, err = f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: low}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "TTL must be between 60 and 86400") {
		t.Fatalf("Cloudflare refusal: %v", err)
	}

	ops, err := f.srv.ListCloudflareDNSOperations(ctx, connect.NewRequest(&neoboxv1.ListCloudflareDNSOperationsRequest{ConnectionId: conn, ZoneId: "za1", PageSize: 2}))
	if err != nil || ops.Msg.GetPageInfo().GetTotalCount() != 5 || ops.Msg.GetPageInfo().GetTotalPages() != 3 || len(ops.Msg.GetOperations()) != 2 {
		t.Fatalf("ListCloudflareDNSOperations = %+v, %v", ops, err)
	}
	failed := ops.Msg.GetOperations()[0]
	if failed.GetStatus() != neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_FAILED || failed.GetError() == "" ||
		failed.GetAfter() != nil || failed.GetRecordId() != "" || failed.GetRecordName() != "x.alpha.com" {
		t.Fatalf("failed operation = %+v", failed)
	}
	if req := failed.GetRequested().AsMap(); req["ttl"] != float64(45) || req["content"] != "hello" || req["type"] != "TXT" {
		t.Fatalf("failed operation's request = %v", req)
	}
	if ops.Msg.GetOperations()[1].GetAction() != neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_DELETE {
		t.Fatalf("operations are not newest first: %+v", ops.Msg.GetOperations())
	}
	if _, err := f.srv.ListCloudflareDNSOperations(asUser("u2"), connect.NewRequest(&neoboxv1.ListCloudflareDNSOperationsRequest{ConnectionId: conn})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("another user's operations: %v, want NotFound", err)
	}
	raw, _ := protojson.Marshal(ops.Msg)
	if strings.Contains(string(raw), "full") {
		t.Fatalf("operations mention the token: %s", raw)
	}

	// Deleting the connection drops the log and changes nothing at Cloudflare.
	writes = len(f.cf.Writes())
	if _, err := f.connSrv.DeleteConnection(ctx, connect.NewRequest(&neoboxv1.DeleteConnectionRequest{Id: conn})); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if left, total, _ := f.ops.ListDNSOperations(context.Background(), cfrepo.Filter{UserID: "u1", ConnectionID: conn}); total != 0 || len(left) != 0 {
		t.Fatalf("operations left: %d", total)
	}
	if len(f.cf.Writes()) != writes || f.cf.RecordCount("za1") != 3 {
		t.Fatalf("deleting the connection changed Cloudflare: %+v", f.cf.Writes()[writes:])
	}
}

func TestCloudflareReadOnlyDNSRefusesChanges(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "dns-read")
	ctx := asUser("u1")
	id := f.cf.AddRecord("za1", map[string]any{"type": "A", "name": "www.alpha.com", "content": "192.0.2.1"})

	if list, err := f.srv.ListCloudflareDNSRecords(ctx, connect.NewRequest(&neoboxv1.ListCloudflareDNSRecordsRequest{ConnectionId: conn, ZoneId: "za1"})); err != nil || len(list.Msg.GetRecords()) != 1 {
		t.Fatalf("reading records with DNS Read: %+v, %v", list, err)
	}
	_, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: dnsInput("A", "x.alpha.com", "192.0.2.2")}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create: %v, want PermissionDenied", err)
	}
	_, err = f.srv.DeleteCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", RecordId: id}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("delete: %v, want PermissionDenied", err)
	}
	if len(f.cf.Writes()) != 0 {
		t.Fatalf("a read-only zone was written to: %+v", f.cf.Writes())
	}
	if _, total, _ := f.ops.ListDNSOperations(context.Background(), cfrepo.Filter{UserID: "u1", ConnectionID: conn}); total != 0 {
		t.Fatalf("refused changes were logged: %d", total)
	}

	// When Cloudflare doesn't say, a refusal still comes from Cloudflare.
	unknown := f.connect(t, "u1", cfAccA, "zones-only")
	f.cf.AddToken("zones-only", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermZoneRead, cftest.PermDNSRead}})
	_, err = f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: unknown, ZoneId: "za1", Record: dnsInput("A", "x.alpha.com", "192.0.2.2")}))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "DNS Write") {
		t.Fatalf("create refused by Cloudflare: %v, want PermissionDenied naming DNS Write", err)
	}
	ops, _, _ := f.ops.ListDNSOperations(context.Background(), cfrepo.Filter{UserID: "u1", ConnectionID: unknown})
	if len(ops) != 1 || ops[0].Status != cfrepo.StatusFailed {
		t.Fatalf("refused create log = %+v", ops)
	}
}

func TestCloudflareUncertainChangesAreNotRepeated(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := asUser("u1")
	id := f.cf.AddRecord("za1", map[string]any{"type": "TXT", "name": "alpha.com", "content": "old"})

	// Cloudflare applies the change, but the answer is lost.
	f.cf.DropAnswer = func(r *http.Request) bool { return r.Method == http.MethodPatch }
	_, err := f.srv.UpdateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.UpdateCloudflareDNSRecordRequest{
		ConnectionId: conn, ZoneId: "za1", RecordId: id, Record: dnsInput("TXT", "alpha.com", "new"),
	}))
	if connect.CodeOf(err) != connect.CodeUnknown || !strings.Contains(err.Error(), "may or may not") {
		t.Fatalf("lost answer: %v, want Unknown", err)
	}
	patches := 0
	for _, w := range f.cf.Writes() {
		if w.Method == http.MethodPatch {
			patches++
		}
	}
	if patches != 1 {
		t.Fatalf("PATCH sent %d times, want once", patches)
	}
	ops, _, _ := f.ops.ListDNSOperations(context.Background(), cfrepo.Filter{UserID: "u1", ConnectionID: conn})
	if len(ops) != 1 || ops[0].Status != cfrepo.StatusUnknown || ops[0].After != nil || ops[0].Before.Content != "old" ||
		ops[0].Requested["content"] != "new" {
		t.Fatalf("operation = %+v", ops[0])
	}
	if f.cf.Record("za1", id)["content"] != "new" {
		t.Fatal("the fake did not apply the change")
	}
	f.cf.DropAnswer = nil

	// No pending entry, nothing sent.
	f.ops.FailCreate = true
	writes := len(f.cf.Writes())
	_, err = f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: dnsInput("TXT", "x.alpha.com", "v")}))
	if connect.CodeOf(err) != connect.CodeUnavailable || len(f.cf.Writes()) != writes {
		t.Fatalf("unloggable change: %v, %d writes", err, len(f.cf.Writes())-writes)
	}
	f.ops.FailCreate = false

	// The change is made but its outcome can't be logged: it is reported as
	// made, and the log keeps it pending.
	f.ops.FailFinish = true
	resp, err := f.srv.CreateCloudflareDNSRecord(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareDNSRecordRequest{ConnectionId: conn, ZoneId: "za1", Record: dnsInput("TXT", "y.alpha.com", "v")}))
	if err != nil || resp.Msg.GetRecord().GetId() == "" || resp.Msg.GetOperationLogError() == "" {
		t.Fatalf("change with unlogged outcome = %+v, %v", resp, err)
	}
	f.ops.FailFinish = false
	ops, _, _ = f.ops.ListDNSOperations(context.Background(), cfrepo.Filter{UserID: "u1", ConnectionID: conn, Limit: 1})
	if ops[0].Status != cfrepo.StatusPending || ops[0].RecordName != "y.alpha.com" {
		t.Fatalf("logged = %+v", ops[0])
	}
}

func addresses(as []*neoboxv1.CloudflareAddress) []string {
	var out []string
	for _, a := range as {
		s := strings.ToLower(strings.TrimPrefix(a.GetKind().String(), "CLOUDFLARE_ADDRESS_KIND_")) + " " + a.GetUrl()
		if a.GetStatus() != "" {
			s += " (" + a.GetStatus() + ")"
		}
		out = append(out, s)
	}
	return out
}

func TestCloudflareWorkersAndTheirAddresses(t *testing.T) {
	f := newCFFixture(t)
	f.cf.SetWorkersSubdomain(cfAccA, "acme")
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "site", WorkersDev: true})
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "api", WorkersDev: true})
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "cron"})
	f.cf.AddScript(cfAccA, &cftest.Script{Name: "old-api", RoutesInScript: []string{"charlie.net/v1/*"}})
	f.cf.AddScript(cfAccB, &cftest.Script{Name: "b-worker", WorkersDev: true})
	f.cf.AddWorkerDomain(cfAccA, &cftest.WorkerDomain{Hostname: "api.alpha.com", Service: "api", ZoneID: "za1"})
	f.cf.AddWorkerDomain(cfAccA, &cftest.WorkerDomain{Hostname: "alpha.com", Service: "site", ZoneID: "za1"})
	f.cf.AddRoute(&cftest.Route{ZoneID: "za1", Pattern: "alpha.com/api/*", Script: "api"})
	f.cf.AddRoute(&cftest.Route{ZoneID: "za2", Pattern: "bravo.com/hook", Script: "api"})
	f.cf.AddRoute(&cftest.Route{ZoneID: "za3", Pattern: "charlie.net/v1/*", Script: "old-api"})
	f.cf.AddRoute(&cftest.Route{ZoneID: "zb1", Pattern: "other-account.com/*", Script: "api"})
	ctx := asUser("u1")
	conn := f.connect(t, "u1", cfAccA, "full")

	page, err := f.srv.ListCloudflareWorkers(ctx, connect.NewRequest(&neoboxv1.ListCloudflareWorkersRequest{ConnectionId: conn, PageSize: 2}))
	if err != nil {
		t.Fatalf("ListCloudflareWorkers: %v", err)
	}
	ws := page.Msg.GetWorkers()
	if len(ws) != 2 || ws[0].GetName() != "api" || ws[1].GetName() != "cron" ||
		page.Msg.GetPageInfo().GetTotalCount() != 4 || page.Msg.GetPageInfo().GetTotalPages() != 2 || len(page.Msg.GetAddressIssues()) != 0 {
		t.Fatalf("workers page = %+v", page.Msg)
	}
	want := "workers_dev https://api.acme.workers.dev,custom_domain https://api.alpha.com,route alpha.com/api/*,route bravo.com/hook"
	if got := strings.Join(addresses(ws[0].GetAddresses()), ","); got != want || ws[0].GetAddressesIncomplete() {
		t.Fatalf("api addresses = %s", got)
	}
	if len(ws[1].GetAddresses()) != 0 || ws[1].GetAddressesIncomplete() {
		t.Fatalf("cron = %+v", ws[1])
	}

	found, err := f.srv.ListCloudflareWorkers(ctx, connect.NewRequest(&neoboxv1.ListCloudflareWorkersRequest{ConnectionId: conn, Query: "API"}))
	if err != nil || len(found.Msg.GetWorkers()) != 2 || found.Msg.GetPageInfo().GetTotalCount() != 2 {
		t.Fatalf("search api = %+v, %v", found, err)
	}
	if got := strings.Join(addresses(found.Msg.GetWorkers()[1].GetAddresses()), ","); got != "route charlie.net/v1/*" {
		t.Fatalf("old-api addresses (route listed by both sources once) = %s", got)
	}

	// Without route or Zone permissions, routes can't be read: the rest is
	// shown and the gap is named.
	f.cf.AddToken("workers-only", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermWorkersRead}})
	partial := f.connect(t, "u1", cfAccA, "workers-only")
	resp, err := f.srv.ListCloudflareWorkers(ctx, connect.NewRequest(&neoboxv1.ListCloudflareWorkersRequest{ConnectionId: partial, Query: "site"}))
	if err != nil {
		t.Fatalf("ListCloudflareWorkers(workers-only): %v", err)
	}
	site := resp.Msg.GetWorkers()[0]
	if got := strings.Join(addresses(site.GetAddresses()), ","); got != "workers_dev https://site.acme.workers.dev,custom_domain https://alpha.com" || !site.GetAddressesIncomplete() {
		t.Fatalf("site with unreadable routes = %s, incomplete %v", got, site.GetAddressesIncomplete())
	}
	if issues := resp.Msg.GetAddressIssues(); len(issues) != 1 || !issues[0].GetPermissionDenied() || !strings.Contains(issues[0].GetSource(), "route") {
		t.Fatalf("issues = %+v", issues)
	}

	// No Workers permission at all is the module's own error, not an empty list.
	zonesOnly := f.connect(t, "u1", cfAccA, "zones-only")
	if _, err := f.srv.ListCloudflareWorkers(ctx, connect.NewRequest(&neoboxv1.ListCloudflareWorkersRequest{ConnectionId: zonesOnly})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("workers without permission: %v, want PermissionDenied", err)
	}
	if _, err := f.srv.ListCloudflarePagesProjects(ctx, connect.NewRequest(&neoboxv1.ListCloudflarePagesProjectsRequest{ConnectionId: zonesOnly})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("pages without permission: %v, want PermissionDenied", err)
	}
	if _, err := f.srv.ListCloudflareZones(ctx, connect.NewRequest(&neoboxv1.ListCloudflareZonesRequest{ConnectionId: zonesOnly})); err != nil {
		t.Fatalf("zones still work: %v", err)
	}
}

func TestCloudflarePagesProjectsAndTheirAddresses(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddProject(cfAccA, &cftest.PagesProject{
		Name: "blog", Subdomain: "blog.pages.dev", Domains: []string{"blog.pages.dev", "blog.alpha.com", "www.blog.alpha.com"},
		DomainStatus: map[string]string{"www.blog.alpha.com": "pending"}, CanonicalURL: "https://abc123.blog.pages.dev", ProductionBranch: "main",
	})
	f.cf.AddProject(cfAccA, &cftest.PagesProject{Name: "shop", Subdomain: "shop.pages.dev", Domains: []string{"shop.pages.dev", "shop.alpha.com"}, FailDomainsRequest: true})
	f.cf.AddProject(cfAccA, &cftest.PagesProject{Name: "bare"})
	for _, n := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"} {
		f.cf.AddProject(cfAccA, &cftest.PagesProject{Name: n, Subdomain: n + ".pages.dev"})
	}
	f.cf.AddProject(cfAccB, &cftest.PagesProject{Name: "b-site", Subdomain: "b-site.pages.dev"})
	ctx := asUser("u1")
	conn := f.connect(t, "u1", cfAccA, "full")

	all, err := f.srv.ListCloudflarePagesProjects(ctx, connect.NewRequest(&neoboxv1.ListCloudflarePagesProjectsRequest{ConnectionId: conn, PageSize: 5}))
	if err != nil || all.Msg.GetPageInfo().GetTotalCount() != 12 || len(all.Msg.GetProjects()) != 5 || all.Msg.GetProjects()[0].GetName() != "bare" {
		t.Fatalf("projects = %+v, %v", all, err)
	}
	bare := all.Msg.GetProjects()[0]
	if len(bare.GetAddresses()) != 0 || bare.GetAddressesIncomplete() {
		t.Fatalf("bare = %+v", bare)
	}
	blog := all.Msg.GetProjects()[1]
	want := "pages_dev https://blog.pages.dev,custom_domain https://blog.alpha.com (active),custom_domain https://www.blog.alpha.com (pending),production https://abc123.blog.pages.dev"
	if got := strings.Join(addresses(blog.GetAddresses()), ","); got != want {
		t.Fatalf("blog addresses = %s", got)
	}

	shop, err := f.srv.ListCloudflarePagesProjects(ctx, connect.NewRequest(&neoboxv1.ListCloudflarePagesProjectsRequest{ConnectionId: conn, Query: "shop"}))
	if err != nil || len(shop.Msg.GetProjects()) != 1 {
		t.Fatalf("search shop = %+v, %v", shop, err)
	}
	p := shop.Msg.GetProjects()[0]
	if got := strings.Join(addresses(p.GetAddresses()), ","); got != "pages_dev https://shop.pages.dev,custom_domain https://shop.alpha.com (unknown)" || !p.GetAddressesIncomplete() {
		t.Fatalf("shop with unreadable domains = %s, incomplete %v", got, p.GetAddressesIncomplete())
	}
	if issues := shop.Msg.GetAddressIssues(); len(issues) != 1 || issues[0].GetPermissionDenied() {
		t.Fatalf("issues = %+v", issues)
	}
}
