package application

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// tunnelRoutes is the stored ingress of a fake Tunnel as hostname → service.
func tunnelRoutes(t *testing.T, f *cfFixture, acc, id string) (routes map[string]string, catchAll string) {
	t.Helper()
	tun := f.cf.Tunnel(acc, id)
	if tun == nil {
		t.Fatalf("tunnel %s is not stored", id)
	}
	routes = map[string]string{}
	ingress, _ := tun.Config["ingress"].([]any)
	for _, r := range ingress {
		rule := r.(map[string]any)
		if h, _ := rule["hostname"].(string); h != "" {
			routes[h] = fmt.Sprint(rule["service"])
		} else {
			catchAll = fmt.Sprint(rule["service"])
		}
	}
	return routes, catchAll
}

func (f *cfFixture) addHostname(conn, tunnel, host, service string, dns bool) (*connect.Response[neoboxv1.AddCloudflareTunnelHostnameResponse], error) {
	return f.srv.AddCloudflareTunnelHostname(asUser("u1"), connect.NewRequest(&neoboxv1.AddCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: tunnel, Hostname: host, Service: service, CreateDns: dns,
	}))
}

func TestCloudflareTunnelsAreListedWithinTheAccount(t *testing.T) {
	f := newCFFixture(t)
	for i := 1; i <= 7; i++ {
		f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: fmt.Sprintf("ta%d", i), Name: fmt.Sprintf("site-%d", i)})
	}
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "tdel", Name: "site-deleted", Deleted: true})
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "tlocal", Name: "legacy", Local: true,
		Connectors: []cftest.TunnelConnector{{ID: "c1", Version: "2026.9.0", Arch: "linux_amd64", Colos: []string{"SJC", "LAX"}}}})
	f.cf.AddTunnel(cfAccB, &cftest.Tunnel{ID: "tb1", Name: "site-b"})
	conn := f.connect(t, "u1", cfAccA, "full") // the token also reaches account B
	ctx := asUser("u1")

	list, err := f.srv.ListCloudflareTunnels(ctx, connect.NewRequest(&neoboxv1.ListCloudflareTunnelsRequest{ConnectionId: conn, PageSize: 5}))
	if err != nil {
		t.Fatalf("ListCloudflareTunnels: %v", err)
	}
	if list.Msg.GetPageInfo().GetTotalCount() != 8 || list.Msg.GetPageInfo().GetTotalPages() != 2 || len(list.Msg.GetTunnels()) != 5 {
		t.Fatalf("page 1 = %d tunnels, %+v", len(list.Msg.GetTunnels()), list.Msg.GetPageInfo())
	}
	if list.Msg.GetTunnels()[0].GetName() != "legacy" {
		t.Fatalf("first tunnel = %q, want name order", list.Msg.GetTunnels()[0].GetName())
	}
	legacy := list.Msg.GetTunnels()[0]
	if legacy.GetManagedRemotely() || legacy.GetActiveConnections() != 2 || legacy.GetStatus() != neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_HEALTHY {
		t.Fatalf("legacy = %+v", legacy)
	}

	// Search covers the whole list, not the page; deleted and foreign
	// tunnels never show.
	found, err := f.srv.ListCloudflareTunnels(ctx, connect.NewRequest(&neoboxv1.ListCloudflareTunnelsRequest{ConnectionId: conn, Query: "SITE-"}))
	if err != nil || found.Msg.GetPageInfo().GetTotalCount() != 7 {
		t.Fatalf("search = %v, %v", found, err)
	}
	for _, tun := range found.Msg.GetTunnels() {
		if tun.GetId() == "tdel" || tun.GetId() == "tb1" {
			t.Fatalf("listed %s", tun.GetId())
		}
	}

	// A tunnel of the other account is not found, even though the token
	// reaches it; a deleted one neither.
	for _, id := range []string{"tb1", "tdel", "nope"} {
		_, err := f.srv.GetCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.GetCloudflareTunnelRequest{ConnectionId: conn, TunnelId: id}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("Get(%s) = %v, want NotFound", id, err)
		}
	}

	// Another user's connection is not found.
	if _, err := f.srv.ListCloudflareTunnels(asUser("u2"), connect.NewRequest(&neoboxv1.ListCloudflareTunnelsRequest{ConnectionId: conn})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("other user = %v, want NotFound", err)
	}
}

func TestCloudflareTunnelDetail(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{
		ID: "t1", Name: "web",
		Connectors: []cftest.TunnelConnector{{ID: "c1", Version: "2026.9.0", Arch: "linux_arm64", Colos: []string{"SJC", "LAX"}}},
		Config: map[string]any{"ingress": []any{
			map[string]any{"hostname": "app.alpha.com", "service": "http://localhost:3000", "originRequest": map[string]any{"noTLSVerify": true}},
			map[string]any{"service": "http_status:404"},
		}},
		Version: 3,
	})
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t2", Name: "fresh"})
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t3", Name: "local", Local: true})
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := asUser("u1")

	get := func(id string) *neoboxv1.GetCloudflareTunnelResponse {
		t.Helper()
		resp, err := f.srv.GetCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.GetCloudflareTunnelRequest{ConnectionId: conn, TunnelId: id}))
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		return resp.Msg
	}
	d := get("t1")
	if len(d.GetConnectors()) != 1 || len(d.GetConnectors()[0].GetConnections()) != 2 || d.GetConnectors()[0].GetVersion() != "2026.9.0" {
		t.Fatalf("connectors = %+v", d.GetConnectors())
	}
	if len(d.GetHostnames()) != 1 || d.GetHostnames()[0].GetHostname() != "app.alpha.com" || !d.GetHostnames()[0].GetHasOriginSettings() ||
		d.GetCatchAllService() != "http_status:404" || d.GetConfigVersion() != 3 || len(d.GetIssues()) != 0 {
		t.Fatalf("detail = %+v", d)
	}

	// A tunnel never configured has no routes and no catch-all, which is
	// not an error.
	if d := get("t2"); len(d.GetHostnames()) != 0 || d.GetCatchAllService() != "" || len(d.GetIssues()) != 0 || d.GetConfigVersion() != 0 {
		t.Fatalf("fresh = %+v", d)
	}
	// A locally configured tunnel shows no routes; the config is not asked.
	n := len(f.cf.Requests())
	if d := get("t3"); d.GetTunnel().GetManagedRemotely() || len(d.GetHostnames()) != 0 || len(d.GetIssues()) != 0 {
		t.Fatalf("local = %+v", d)
	}
	for _, r := range f.cf.Requests()[n:] {
		if strings.HasSuffix(r.Path, "/configurations") {
			t.Fatalf("read the config of a locally configured tunnel: %+v", r)
		}
	}
}

func TestCloudflareTunnelPermissions(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "web", Config: map[string]any{"ingress": []any{map[string]any{"service": "http_status:404"}}}})
	ctx := asUser("u1")

	// A token without any Tunnel permission is refused, not shown as an
	// empty account.
	noTunnels := f.connect(t, "u1", cfAccA, "dns-read")
	_, err := f.srv.ListCloudflareTunnels(ctx, connect.NewRequest(&neoboxv1.ListCloudflareTunnelsRequest{ConnectionId: noTunnels}))
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "Cloudflare Tunnel Read") {
		t.Fatalf("list without permission = %v", err)
	}

	// A read-only token reads; Cloudflare refuses every change and none is
	// made.
	ro := f.connect(t, "u1", cfAccA, "tunnel-read")
	if _, err := f.srv.GetCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.GetCloudflareTunnelRequest{ConnectionId: ro, TunnelId: "t1"})); err != nil {
		t.Fatalf("read-only Get: %v", err)
	}
	if _, err := f.srv.CreateCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareTunnelRequest{ConnectionId: ro, Name: "x"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("create = %v, want PermissionDenied", err)
	}
	if _, err := f.addHostname(ro, "t1", "a.alpha.com", "http://localhost:1", true); connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "Cloudflare Tunnel Edit") {
		t.Errorf("add hostname = %v, want PermissionDenied naming the permission", err)
	}
	if _, err := f.srv.DeleteCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelRequest{ConnectionId: ro, TunnelId: "t1"})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("delete = %v, want PermissionDenied", err)
	}
	if tun := f.cf.Tunnel(cfAccA, "t1"); tun == nil || tun.Version != 0 || f.cf.TunnelByName(cfAccA, "x") != nil {
		t.Fatalf("a refused change was made: %+v", tun)
	}
}

func TestCloudflareTunnelCreateTokenAndDelete(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := asUser("u1")

	for _, name := range []string{"", "   ", strings.Repeat("x", 300)} {
		_, err := f.srv.CreateCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareTunnelRequest{ConnectionId: conn, Name: name}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("create %.10q = %v, want InvalidArgument", name, err)
		}
	}
	created, err := f.srv.CreateCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareTunnelRequest{ConnectionId: conn, Name: "  home  "}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tun := created.Msg.GetTunnel()
	if tun.GetName() != "home" || !tun.GetManagedRemotely() || tun.GetStatus() != neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_INACTIVE {
		t.Fatalf("created = %+v", tun)
	}
	if f.cf.TunnelByName(cfAccA, "home") == nil {
		t.Fatal("the tunnel was not created at Cloudflare")
	}
	// The secret Neo Box sends never comes back; the token is its own call.
	if strings.Contains(strings.ToLower(created.Msg.String()), "token") {
		t.Fatalf("create response carries a credential: %v", created.Msg)
	}
	if _, err := f.srv.CreateCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.CreateCloudflareTunnelRequest{ConnectionId: conn, Name: "home"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("duplicate name = %v, want InvalidArgument", err)
	}

	token, err := f.srv.GetCloudflareTunnelToken(ctx, connect.NewRequest(&neoboxv1.GetCloudflareTunnelTokenRequest{ConnectionId: conn, TunnelId: tun.GetId()}))
	if err != nil || token.Msg.GetToken() != "token-for-"+tun.GetId() {
		t.Fatalf("token = %v, %v", token, err)
	}
	// The token of a tunnel in another account is never asked for.
	f.cf.AddTunnel(cfAccB, &cftest.Tunnel{ID: "tb1", Name: "b"})
	n := len(f.cf.Requests())
	if _, err := f.srv.GetCloudflareTunnelToken(ctx, connect.NewRequest(&neoboxv1.GetCloudflareTunnelTokenRequest{ConnectionId: conn, TunnelId: "tb1"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("foreign token = %v, want NotFound", err)
	}
	for _, r := range f.cf.Requests()[n:] {
		if strings.HasSuffix(r.Path, "/token") {
			t.Fatalf("asked Cloudflare for a foreign tunnel's token: %+v", r)
		}
	}

	// A tunnel with a connector cannot be deleted until its connections
	// are cleaned up.
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "busy", Name: "busy", Connectors: []cftest.TunnelConnector{{ID: "c1", Colos: []string{"SJC"}}}})
	if _, err := f.srv.DeleteCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelRequest{ConnectionId: conn, TunnelId: "busy"})); err == nil ||
		!strings.Contains(err.Error(), "active connections") {
		t.Fatalf("delete busy = %v, want Cloudflare's refusal", err)
	}
	if f.cf.Tunnel(cfAccA, "busy") == nil {
		t.Fatal("the busy tunnel was deleted")
	}
	if _, err := f.srv.CleanupCloudflareTunnelConnections(ctx, connect.NewRequest(&neoboxv1.CleanupCloudflareTunnelConnectionsRequest{ConnectionId: conn, TunnelId: "busy"})); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := f.srv.DeleteCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelRequest{ConnectionId: conn, TunnelId: "busy"})); err != nil {
		t.Fatalf("delete after cleanup: %v", err)
	}
	if f.cf.Tunnel(cfAccA, "busy") != nil {
		t.Fatal("the tunnel was not deleted")
	}

	// cleanup_connections does both in one call.
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "busy2", Name: "busy2", Connectors: []cftest.TunnelConnector{{ID: "c1", Colos: []string{"SJC"}}}})
	if _, err := f.srv.DeleteCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelRequest{ConnectionId: conn, TunnelId: "busy2", CleanupConnections: true})); err != nil {
		t.Fatalf("delete with cleanup: %v", err)
	}
	if f.cf.Tunnel(cfAccA, "busy2") != nil {
		t.Fatal("the tunnel was not deleted")
	}
	// A foreign tunnel cannot be deleted through this connection.
	if _, err := f.srv.DeleteCloudflareTunnel(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelRequest{ConnectionId: conn, TunnelId: "tb1"})); connect.CodeOf(err) != connect.CodeNotFound ||
		f.cf.Tunnel(cfAccB, "tb1") == nil {
		t.Fatalf("foreign delete = %v", err)
	}
}

func TestCloudflareTunnelHostnames(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{
		ID: "t1", Name: "web",
		Config: map[string]any{
			"ingress": []any{
				map[string]any{"hostname": "old.alpha.com", "service": "http://localhost:1", "originRequest": map[string]any{"noTLSVerify": true}},
				map[string]any{"service": "http_status:503"},
			},
			"warp-routing": map[string]any{"enabled": true},
		},
		Version: 1,
	})
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := asUser("u1")

	// Invalid input never reaches Cloudflare.
	for name, c := range map[string][2]string{
		"no domain":    {"localhost", "http://localhost:1"},
		"url":          {"https://app.alpha.com", "http://localhost:1"},
		"bad service":  {"app.alpha.com", "localhost:80"},
		"service path": {"app.alpha.com", "http://localhost:80/app"},
	} {
		if _, err := f.addHostname(conn, "t1", c[0], c[1], true); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s = %v, want InvalidArgument", name, err)
		}
	}
	if len(f.cf.Writes()) != 0 {
		t.Fatalf("invalid input was written: %+v", f.cf.Writes())
	}

	// Adding a hostname with DNS: the route goes in before the catch-all,
	// the rest of the configuration is kept, and a proxied CNAME to the
	// tunnel is created.
	added, err := f.addHostname(conn, "t1", " App.Alpha.com ", "http://localhost:3000", true)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if added.Msg.GetHostname().GetHostname() != "app.alpha.com" || added.Msg.GetConfigVersion() != 2 ||
		added.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CREATED {
		t.Fatalf("add response = %+v", added.Msg)
	}
	routes, catchAll := tunnelRoutes(t, f, cfAccA, "t1")
	if routes["app.alpha.com"] != "http://localhost:3000" || routes["old.alpha.com"] != "http://localhost:1" || catchAll != "http_status:503" {
		t.Fatalf("ingress = %v, catch-all %q", routes, catchAll)
	}
	stored := f.cf.Tunnel(cfAccA, "t1")
	rules := stored.Config["ingress"].([]any)
	if rules[0].(map[string]any)["originRequest"] == nil || stored.Config["warp-routing"] == nil ||
		rules[len(rules)-1].(map[string]any)["hostname"] != nil {
		t.Fatalf("stored config = %v", stored.Config)
	}
	rec := f.cf.Record("za1", added.Msg.GetRecord().GetRecordId())
	if rec == nil || rec["type"] != "CNAME" || rec["name"] != "app.alpha.com" || rec["content"] != "t1.cfargotunnel.com" || rec["proxied"] != true {
		t.Fatalf("dns record = %v", rec)
	}

	// The same hostname cannot be routed twice, and nothing is written.
	writes := len(f.cf.Writes())
	if _, err := f.addHostname(conn, "t1", "app.alpha.com", "http://localhost:9", true); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("duplicate = %v, want InvalidArgument", err)
	}
	if len(f.cf.Writes()) != writes {
		t.Fatalf("a duplicate was written: %+v", f.cf.Writes()[writes:])
	}

	// Update changes path and service and keeps the rule's settings.
	upd, err := f.srv.UpdateCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.UpdateCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "old.alpha.com", Path: "^/v2", Service: "https://localhost:8443",
	}))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if h := upd.Msg.GetHostname(); h.GetPath() != "^/v2" || h.GetService() != "https://localhost:8443" || !h.GetHasOriginSettings() {
		t.Fatalf("updated = %+v", h)
	}
	if _, err := f.srv.UpdateCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.UpdateCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "ghost.alpha.com", Service: "http://x:1",
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("update unknown = %v, want NotFound", err)
	}

	// Delete removes the route and, on request, the CNAME to this tunnel.
	del, err := f.srv.DeleteCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "app.alpha.com", DeleteDns: true,
	}))
	if err != nil || del.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_DELETED {
		t.Fatalf("delete = %v, %v", del, err)
	}
	if routes, _ := tunnelRoutes(t, f, cfAccA, "t1"); routes["app.alpha.com"] != "" || len(routes) != 1 {
		t.Fatalf("ingress after delete = %v", routes)
	}
	if f.cf.Record("za1", added.Msg.GetRecord().GetRecordId()) != nil {
		t.Fatal("the CNAME was not deleted")
	}
	// The catch-all and the other settings survive the last-but-one delete.
	if _, catchAll := tunnelRoutes(t, f, cfAccA, "t1"); catchAll != "http_status:503" || f.cf.Tunnel(cfAccA, "t1").Config["warp-routing"] == nil {
		t.Fatalf("config after delete = %v", f.cf.Tunnel(cfAccA, "t1").Config)
	}
}

func TestCloudflareTunnelHostnameDNSOutcomes(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "web"}) // never configured
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t2", Name: "other"})
	conn := f.connect(t, "u1", cfAccA, "full")
	ctx := asUser("u1")

	// The first route of a tunnel never configured gets a catch-all.
	if _, err := f.addHostname(conn, "t1", "first.alpha.com", "http://localhost:1", false); err != nil {
		t.Fatalf("first route: %v", err)
	}
	if routes, catchAll := tunnelRoutes(t, f, cfAccA, "t1"); len(routes) != 1 || catchAll != "http_status:404" {
		t.Fatalf("ingress = %v, catch-all %q", routes, catchAll)
	}
	if n := f.cf.RecordCount("za1"); n != 0 {
		t.Fatalf("DNS was changed without being asked: %d records", n)
	}

	// A hostname in no zone of the account still gets its route.
	r, err := f.addHostname(conn, "t1", "x.nowhere.org", "http://localhost:1", true)
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_NO_ZONE ||
		!strings.Contains(r.Msg.GetRecord().GetMessage(), "t1.cfargotunnel.com") {
		t.Fatalf("no zone = %+v, %v", r, err)
	}
	// A zone of another account is not used even though the token sees it.
	r, err = f.addHostname(conn, "t1", "x.other-account.com", "http://localhost:1", true)
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_NO_ZONE || f.cf.RecordCount("zb1") != 0 {
		t.Fatalf("foreign zone = %+v, %v", r, err)
	}

	// An existing record of another kind is never replaced.
	other := f.cf.AddRecord("za1", map[string]any{"type": "A", "name": "web.alpha.com", "content": "192.0.2.1"})
	r, err = f.addHostname(conn, "t1", "web.alpha.com", "http://localhost:1", true)
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CONFLICT {
		t.Fatalf("conflict = %+v, %v", r, err)
	}
	if rec := f.cf.Record("za1", other); rec == nil || rec["content"] != "192.0.2.1" {
		t.Fatalf("the existing record was changed: %v", rec)
	}
	if routes, _ := tunnelRoutes(t, f, cfAccA, "t1"); routes["web.alpha.com"] == "" {
		t.Fatal("the route was not saved")
	}
	// Deleting it with deleteDns leaves a record that is not a CNAME to
	// this tunnel.
	d, err := f.srv.DeleteCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "web.alpha.com", DeleteDns: true,
	}))
	if err != nil || d.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_KEPT || f.cf.Record("za1", other) == nil {
		t.Fatalf("delete over an A record = %+v, %v", d, err)
	}

	// A CNAME to the tunnel already there is reused; one to another tunnel
	// is a conflict and is kept on delete.
	own := f.cf.AddRecord("za1", map[string]any{"type": "CNAME", "name": "mine.alpha.com", "content": "t1.cfargotunnel.com", "proxied": true})
	f.cf.AddRecord("za1", map[string]any{"type": "CNAME", "name": "theirs.alpha.com", "content": "t2.cfargotunnel.com", "proxied": true})
	writes := len(f.cf.Writes())
	r, err = f.addHostname(conn, "t1", "mine.alpha.com", "http://localhost:1", true)
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_EXISTS || r.Msg.GetRecord().GetRecordId() != own {
		t.Fatalf("existing cname = %+v, %v", r, err)
	}
	for _, w := range f.cf.Writes()[writes:] {
		if strings.Contains(w.Path, "dns_records") {
			t.Fatalf("an existing record was written: %+v", w)
		}
	}
	r, err = f.addHostname(conn, "t1", "theirs.alpha.com", "http://localhost:1", true)
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CONFLICT {
		t.Fatalf("other tunnel's cname = %+v, %v", r, err)
	}
	if _, err := f.srv.DeleteCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "theirs.alpha.com", DeleteDns: true,
	})); err != nil {
		t.Fatal(err)
	}
	if n := f.cf.RecordCount("za1"); n != 3 { // A, mine, theirs
		t.Fatalf("records = %d, a CNAME of another tunnel was deleted", n)
	}

	// Another route on the same hostname keeps its DNS record.
	if _, err := f.srv.AddCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.AddCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "mine.alpha.com", Path: "^/admin", Service: "http://localhost:2",
	})); err != nil {
		t.Fatal(err)
	}
	d, err = f.srv.DeleteCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.DeleteCloudflareTunnelHostnameRequest{
		ConnectionId: conn, TunnelId: "t1", Hostname: "mine.alpha.com", Path: "^/admin", DeleteDns: true,
	}))
	if err != nil || d.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_KEPT || f.cf.Record("za1", own) == nil {
		t.Fatalf("delete one of two routes = %+v, %v", d, err)
	}

	// Without DNS permission the route is saved and the problem reported.
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t3", Name: "nodns"})
	noDNS := f.connect(t, "u1", cfAccA, "tunnel-no-dns")
	r, err = f.srv.AddCloudflareTunnelHostname(ctx, connect.NewRequest(&neoboxv1.AddCloudflareTunnelHostnameRequest{
		ConnectionId: noDNS, TunnelId: "t3", Hostname: "n.alpha.com", Service: "http://localhost:1", CreateDns: true,
	}))
	if err != nil || r.Msg.GetRecord().GetStatus() != neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_FAILED ||
		!strings.Contains(r.Msg.GetRecord().GetMessage(), "DNS Write") {
		t.Fatalf("no dns permission = %+v, %v", r, err)
	}
	if routes, _ := tunnelRoutes(t, f, cfAccA, "t3"); routes["n.alpha.com"] == "" {
		t.Fatal("the route was not saved")
	}
}

func TestCloudflareTunnelLocallyConfiguredHostnamesAreNotChanged(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "local", Local: true})
	conn := f.connect(t, "u1", cfAccA, "full")

	if _, err := f.addHostname(conn, "t1", "a.alpha.com", "http://localhost:1", true); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("add = %v, want FailedPrecondition", err)
	}
	if len(f.cf.Writes()) != 0 {
		t.Fatalf("a locally configured tunnel was written: %+v", f.cf.Writes())
	}
}

func TestCloudflareTunnelChangesAreSerialized(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "web"})
	conn := f.connect(t, "u1", cfAccA, "full")

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.addHostname(conn, "t1", fmt.Sprintf("h%d.alpha.com", i), "http://localhost:1", false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// A read-modify-write that overlapped would lose routes.
	if routes, _ := tunnelRoutes(t, f, cfAccA, "t1"); len(routes) != n {
		t.Fatalf("%d of %d routes survived: %v", len(routes), n, routes)
	}
}

func TestCloudflareTunnelUncertainChangeIsNotRepeated(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "web"})
	conn := f.connect(t, "u1", cfAccA, "full")
	f.cf.DropAnswer = func(r *http.Request) bool { return r.Method == http.MethodPut }

	_, err := f.addHostname(conn, "t1", "a.alpha.com", "http://localhost:1", true)
	if connect.CodeOf(err) != connect.CodeUnknown {
		t.Fatalf("lost answer = %v, want Unknown", err)
	}
	puts := 0
	for _, w := range f.cf.Writes() {
		if w.Method == http.MethodPut {
			puts++
		}
	}
	if puts != 1 {
		t.Fatalf("the change was sent %d times", puts)
	}
	// With the route's outcome unknown, no DNS is changed on its behalf.
	if n := f.cf.RecordCount("za1"); n != 0 {
		t.Fatalf("DNS was changed after an unconfirmed route: %d records", n)
	}
}

func TestCloudflareConnectionWithOnlyTunnelPermission(t *testing.T) {
	f := newCFFixture(t)
	f.cf.AddToken("tunnels-only", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{cftest.PermTunnelRead}})
	f.cf.AddTunnel(cfAccA, &cftest.Tunnel{ID: "t1", Name: "web"})

	// A token that only reads Tunnels reaches the account through them.
	conn := f.connect(t, "u1", cfAccA, "tunnels-only")
	list, err := f.srv.ListCloudflareTunnels(asUser("u1"), connect.NewRequest(&neoboxv1.ListCloudflareTunnelsRequest{ConnectionId: conn}))
	if err != nil || len(list.Msg.GetTunnels()) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	// ...but not an account it does not reach.
	if _, err := f.connSrv.CreateConnection(asUser("u1"), cfCreate("cf", cfAccB, "tunnels-only")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("other account = %v, want InvalidArgument", err)
	}
}
