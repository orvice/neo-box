package cloudflare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// IngressRule is one rule of a Tunnel's ingress: requests for Hostname (and
// Path, a regular expression) go to Service. Everything else a rule holds
// (originRequest and whatever Cloudflare adds later) is kept in Extra and
// written back as it was.
type IngressRule struct {
	Hostname string
	Path     string
	Service  string
	Extra    map[string]json.RawMessage
}

// CatchAll reports whether the rule matches everything that no earlier rule
// does. A Tunnel's ingress must end with one.
func (r IngressRule) CatchAll() bool { return r.Hostname == "" && r.Path == "" }

func (r IngressRule) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(r.Extra)+3)
	for k, v := range r.Extra {
		m[k] = v
	}
	set := func(k, v string) {
		if v == "" {
			delete(m, k)
			return
		}
		m[k], _ = json.Marshal(v)
	}
	set("hostname", r.Hostname)
	set("path", r.Path)
	set("service", r.Service)
	return json.Marshal(m)
}

func (r *IngressRule) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*r = IngressRule{}
	take := func(k string, dst *string) error {
		raw, ok := m[k]
		if !ok {
			return nil
		}
		delete(m, k)
		if string(raw) == "null" {
			return nil
		}
		return json.Unmarshal(raw, dst)
	}
	for k, dst := range map[string]*string{"hostname": &r.Hostname, "path": &r.Path, "service": &r.Service} {
		if err := take(k, dst); err != nil {
			return fmt.Errorf("ingress %s: %w", k, err)
		}
	}
	if len(m) > 0 {
		r.Extra = m
	}
	return nil
}

// HasSettings reports whether the rule carries origin settings Neo Box does
// not edit, such as originRequest.
func (r IngressRule) HasSettings() bool { return len(r.Extra) > 0 }

// TunnelConfig is the config of a remotely managed Tunnel. Only the ingress
// is edited; any other key (originRequest, warp-routing, ...) is kept.
type TunnelConfig struct {
	Ingress []IngressRule
	Extra   map[string]json.RawMessage
}

func (c TunnelConfig) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage, len(c.Extra)+1)
	for k, v := range c.Extra {
		m[k] = v
	}
	ingress := c.Ingress
	if ingress == nil {
		ingress = []IngressRule{}
	}
	raw, err := json.Marshal(ingress)
	if err != nil {
		return nil, err
	}
	m["ingress"] = raw
	return json.Marshal(m)
}

func (c *TunnelConfig) UnmarshalJSON(b []byte) error {
	*c = TunnelConfig{}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if raw, ok := m["ingress"]; ok {
		delete(m, "ingress")
		if err := json.Unmarshal(raw, &c.Ingress); err != nil {
			return fmt.Errorf("ingress: %w", err)
		}
	}
	if len(m) > 0 {
		c.Extra = m
	}
	return nil
}

// Routes are the rules that send a hostname or path somewhere, in order:
// every rule but the catch-all.
func (c TunnelConfig) Routes() []IngressRule {
	var out []IngressRule
	for _, r := range c.Ingress {
		if !r.CatchAll() {
			out = append(out, r)
		}
	}
	return out
}

// CatchAllService is where requests no route matches go, or "" when the
// ingress has no catch-all.
func (c TunnelConfig) CatchAllService() string {
	for _, r := range c.Ingress {
		if r.CatchAll() {
			return r.Service
		}
	}
	return ""
}

// DefaultCatchAll is the catch-all of a Tunnel that has none: Cloudflare
// requires the ingress to end with one.
const DefaultCatchAll = "http_status:404"

// ErrRuleNotFound: no ingress rule has the hostname and path.
var ErrRuleNotFound = errors.New("no such hostname in the tunnel's ingress")

func (c TunnelConfig) index(hostname, path string) int {
	for i, r := range c.Ingress {
		if !r.CatchAll() && strings.EqualFold(r.Hostname, hostname) && r.Path == path {
			return i
		}
	}
	return -1
}

// Has reports whether a route for the hostname and path exists.
func (c TunnelConfig) Has(hostname, path string) bool { return c.index(hostname, path) >= 0 }

// WithRoute returns the config with a new route placed before the
// catch-all, which is added when missing. The same hostname and path can
// only be routed once.
func (c TunnelConfig) WithRoute(r IngressRule) (TunnelConfig, error) {
	if c.Has(r.Hostname, r.Path) {
		return c, invalid("hostname", "%s is already routed by this tunnel", describeRoute(r.Hostname, r.Path))
	}
	out := c.clone()
	last := len(out.Ingress) - 1
	if last >= 0 && out.Ingress[last].CatchAll() {
		out.Ingress = append(out.Ingress[:last], r, out.Ingress[last])
		return out, nil
	}
	out.Ingress = append(out.Ingress, r, IngressRule{Service: DefaultCatchAll})
	return out, nil
}

// WithRouteChanged returns the config with the route for hostname and path
// given a new path and service, keeping the rule's other settings.
func (c TunnelConfig) WithRouteChanged(hostname, path, newPath, service string) (TunnelConfig, error) {
	i := c.index(hostname, path)
	if i < 0 {
		return c, ErrRuleNotFound
	}
	if newPath != path && c.Has(hostname, newPath) {
		return c, invalid("path", "%s is already routed", describeRoute(hostname, newPath))
	}
	out := c.clone()
	out.Ingress[i].Path = newPath
	out.Ingress[i].Service = service
	return out, nil
}

// WithoutRoute returns the config without the route for hostname and path.
func (c TunnelConfig) WithoutRoute(hostname, path string) (TunnelConfig, error) {
	i := c.index(hostname, path)
	if i < 0 {
		return c, ErrRuleNotFound
	}
	out := c.clone()
	out.Ingress = append(out.Ingress[:i], out.Ingress[i+1:]...)
	// Without its last route a Tunnel is left with the catch-all alone; a
	// Tunnel with no routes at all is valid, so it is kept.
	return out, nil
}

func (c TunnelConfig) clone() TunnelConfig {
	out := TunnelConfig{Extra: c.Extra}
	out.Ingress = append(make([]IngressRule, 0, len(c.Ingress)+2), c.Ingress...)
	return out
}

func describeRoute(hostname, path string) string {
	if path == "" {
		return hostname
	}
	return hostname + " (path " + path + ")"
}

// --- validation ---

const maxPathLen = 500

var hostLabel = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$`)

// NormalizeHostname trims and lower-cases a public hostname.
func NormalizeHostname(h string) string { return strings.ToLower(strings.TrimSpace(h)) }

// ValidateTunnelHostname checks a public hostname: a DNS name of at least
// two labels, whose first may be a wildcard.
func ValidateTunnelHostname(h string) error {
	if h == "" {
		return invalid("hostname", "is required")
	}
	if len(h) > 253 {
		return invalid("hostname", "must be at most 253 characters")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return invalid("hostname", "must be a full domain name such as app.example.com")
	}
	for i, l := range labels {
		if i == 0 && l == "*" {
			continue
		}
		if len(l) > 63 || !hostLabel.MatchString(l) {
			return invalid("hostname", "must be a domain name such as app.example.com, without a scheme, port or path")
		}
	}
	return nil
}

// ValidateTunnelPath checks an optional path: a regular expression that
// cloudflared matches against the request path.
func ValidateTunnelPath(p string) error {
	if len(p) > maxPathLen {
		return invalid("path", "must be at most %d characters", maxPathLen)
	}
	if _, err := regexp.Compile(p); err != nil {
		return invalid("path", "must be a valid regular expression")
	}
	return nil
}

// originSchemes are the schemes cloudflared reaches an origin with.
var originSchemes = map[string]bool{
	"http": true, "https": true, "ws": true, "wss": true,
	"tcp": true, "ssh": true, "rdp": true, "smb": true,
}

// ValidateTunnelService checks where a route sends requests: an origin URL
// (http://localhost:8080, ssh://host:22, unix:/run/app.sock), or one of
// cloudflared's built-in services.
func ValidateTunnelService(s string) error {
	switch {
	case s == "":
		return invalid("service", "is required")
	case s == "hello_world" || s == "bastion":
		return nil
	case strings.HasPrefix(s, "http_status:"):
		n, err := strconv.Atoi(strings.TrimPrefix(s, "http_status:"))
		if err != nil || n < 100 || n > 599 {
			return invalid("service", "http_status needs a status code between 100 and 599")
		}
		return nil
	case strings.HasPrefix(s, "unix:") || strings.HasPrefix(s, "unix+tls:"):
		if _, rest, _ := strings.Cut(s, ":"); rest == "" || rest == "/" {
			return invalid("service", "unix needs a socket path, e.g. unix:/run/app.sock")
		}
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return invalid("service", "must be an origin URL such as http://localhost:8080")
	}
	if !originSchemes[u.Scheme] {
		return invalid("service", "scheme must be one of http, https, ws, wss, tcp, ssh, rdp, smb")
	}
	if u.Hostname() == "" {
		return invalid("service", "needs a host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return invalid("service", "port must be between 1 and 65535")
		}
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return invalid("service", "must be only scheme, host and port; put the path in the route's path")
	}
	return nil
}

// TunnelCNAMETarget is the DNS name a hostname points to for a Tunnel.
func TunnelCNAMETarget(tunnelID string) string { return tunnelID + ".cfargotunnel.com" }
