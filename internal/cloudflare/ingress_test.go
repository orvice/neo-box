package cloudflare

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const storedConfig = `{
  "ingress": [
    {"hostname": "app.example.com", "service": "http://localhost:3000", "originRequest": {"noTLSVerify": true}},
    {"hostname": "api.example.com", "path": "^/v1/", "service": "http://localhost:4000"},
    {"service": "http_status:404"}
  ],
  "warp-routing": {"enabled": true},
  "originRequest": {"connectTimeout": 30}
}`

func parseConfig(t *testing.T, raw string) TunnelConfig {
	t.Helper()
	var c TunnelConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c
}

func asJSON(t *testing.T, c TunnelConfig) map[string]any {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func TestTunnelConfigKeepsWhatItDoesNotEdit(t *testing.T) {
	c := parseConfig(t, storedConfig)
	next, err := c.WithRoute(IngressRule{Hostname: "new.example.com", Service: "http://localhost:5000"})
	if err != nil {
		t.Fatalf("WithRoute: %v", err)
	}
	m := asJSON(t, next)
	if m["warp-routing"] == nil || m["originRequest"] == nil {
		t.Fatalf("top-level settings were dropped: %v", m)
	}
	rules := m["ingress"].([]any)
	if len(rules) != 4 {
		t.Fatalf("ingress = %v", rules)
	}
	first := rules[0].(map[string]any)
	if first["originRequest"] == nil || first["hostname"] != "app.example.com" {
		t.Fatalf("a rule lost its origin settings: %v", first)
	}
	// The new route goes before the catch-all, which stays last.
	if rules[2].(map[string]any)["hostname"] != "new.example.com" {
		t.Fatalf("new route is not before the catch-all: %v", rules)
	}
	last := rules[3].(map[string]any)
	if last["hostname"] != nil || last["service"] != "http_status:404" {
		t.Fatalf("catch-all = %v", last)
	}
	// The original is not modified.
	if len(c.Ingress) != 3 {
		t.Fatalf("WithRoute changed its receiver: %d rules", len(c.Ingress))
	}
}

func TestTunnelConfigWithoutIngressGetsACatchAll(t *testing.T) {
	var c TunnelConfig
	if err := json.Unmarshal([]byte(`null`), &c); err != nil {
		t.Fatal(err)
	}
	next, err := c.WithRoute(IngressRule{Hostname: "a.example.com", Service: "http://localhost:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := next.CatchAllService(); got != DefaultCatchAll || len(next.Routes()) != 1 {
		t.Fatalf("catch-all = %q, routes = %d", got, len(next.Routes()))
	}
	if len(next.Ingress) != 2 || !next.Ingress[1].CatchAll() {
		t.Fatalf("ingress = %+v", next.Ingress)
	}
}

func TestTunnelConfigRoutes(t *testing.T) {
	c := parseConfig(t, storedConfig)
	if r := c.Routes(); len(r) != 2 || r[0].Hostname != "app.example.com" || !r[0].HasSettings() || r[1].HasSettings() {
		t.Fatalf("routes = %+v", r)
	}
	if c.CatchAllService() != "http_status:404" {
		t.Fatalf("catch-all = %q", c.CatchAllService())
	}

	if _, err := c.WithRoute(IngressRule{Hostname: "APP.example.com", Service: "http://x:1"}); err == nil {
		t.Fatal("the same hostname and path was routed twice")
	}
	// The same hostname with another path is another route.
	if _, err := c.WithRoute(IngressRule{Hostname: "app.example.com", Path: "^/admin", Service: "http://x:1"}); err != nil {
		t.Fatalf("same hostname, other path: %v", err)
	}

	changed, err := c.WithRouteChanged("app.example.com", "", "^/web", "https://localhost:8443")
	if err != nil {
		t.Fatalf("WithRouteChanged: %v", err)
	}
	r := changed.Routes()[0]
	if r.Path != "^/web" || r.Service != "https://localhost:8443" || !r.HasSettings() {
		t.Fatalf("changed route = %+v", r)
	}
	if _, err := c.WithRouteChanged("api.example.com", "^/v1/", "", "http://x:1"); err != nil {
		t.Fatalf("clearing a path: %v", err)
	}
	if _, err := c.WithRouteChanged("nope.example.com", "", "", "http://x:1"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("unknown route: %v", err)
	}

	removed, err := c.WithoutRoute("api.example.com", "^/v1/")
	if err != nil || len(removed.Routes()) != 1 || removed.CatchAllService() != "http_status:404" {
		t.Fatalf("WithoutRoute = %+v, %v", removed, err)
	}
	if _, err := c.WithoutRoute("api.example.com", ""); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("removing with the wrong path: %v", err)
	}
	// The catch-all is not a route that can be removed.
	if _, err := c.WithoutRoute("", ""); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("removing the catch-all: %v", err)
	}
}

func TestValidateTunnelInput(t *testing.T) {
	for _, h := range []string{"app.example.com", "*.example.com", "a-b.c_d.example.co.uk"} {
		if err := ValidateTunnelHostname(h); err != nil {
			t.Errorf("hostname %q: %v", h, err)
		}
	}
	for _, h := range []string{"", "localhost", "https://app.example.com", "app.example.com/x", "app.example.com:8080", "-a.example.com", "a..example.com", "app.*.com", "a b.example.com"} {
		if err := ValidateTunnelHostname(h); err == nil {
			t.Errorf("hostname %q was accepted", h)
		}
	}

	for _, s := range []string{
		"http://localhost:8080", "https://10.0.0.5", "tcp://db:5432", "ssh://host:22", "rdp://win:3389", "ws://x:1",
		"unix:/run/app.sock", "unix+tls:/run/app.sock", "http_status:404", "hello_world", "bastion", "http://[::1]:3000",
	} {
		if err := ValidateTunnelService(s); err != nil {
			t.Errorf("service %q: %v", s, err)
		}
	}
	for _, s := range []string{
		"", "localhost:8080", "ftp://x", "http://", "http://x:0", "http://x:99999", "http://x/path", "http://u:p@x",
		"http_status:99", "http_status:abc", "unix:", "unix:/",
	} {
		if err := ValidateTunnelService(s); err == nil {
			t.Errorf("service %q was accepted", s)
		}
	}

	if err := ValidateTunnelPath(`^/api/.*\.png$`); err != nil {
		t.Errorf("path: %v", err)
	}
	for _, p := range []string{"(unclosed", strings.Repeat("a", maxPathLen+1)} {
		var fe *FieldError
		if err := ValidateTunnelPath(p); !errors.As(err, &fe) {
			t.Errorf("path %.20q: %v", p, err)
		}
	}
}
