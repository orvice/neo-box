package cftest

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// TunnelConnector is a cloudflared instance connected to a Tunnel.
type TunnelConnector struct {
	ID, Version, Arch string
	Colos             []string
}

// Tunnel is a cloudflared Tunnel. A Tunnel with Connectors is healthy and
// cannot be deleted.
type Tunnel struct {
	ID, Name string
	// Local makes it configured by a file: no remote config exists.
	Local      bool
	Connectors []TunnelConnector
	// Config is the stored configuration, as Cloudflare would return it
	// under "config"; nil until one is written.
	Config  map[string]any
	Version int
	Deleted bool
}

// AddTunnel adds a Tunnel to an Account.
func (s *Server) AddTunnel(account string, t *Tunnel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tunnels[account] = append(s.tunnels[account], t)
}

// Tunnel returns a copy of a stored Tunnel's state, or nil.
func (s *Server) Tunnel(account, id string) *Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findTunnel(account, id)
	if t == nil {
		return nil
	}
	cp := *t
	cp.Config = copyMap(t.Config)
	cp.Connectors = append([]TunnelConnector(nil), t.Connectors...)
	return &cp
}

// TunnelByName returns a copy of the live Tunnel with the name, or nil.
func (s *Server) TunnelByName(account, name string) *Tunnel {
	s.mu.Lock()
	var id string
	for _, t := range s.tunnels[account] {
		if t.Name == name && !t.Deleted {
			id = t.ID
		}
	}
	s.mu.Unlock()
	if id == "" {
		return nil
	}
	return s.Tunnel(account, id)
}

func (s *Server) findTunnel(account, id string) *Tunnel {
	for _, t := range s.tunnels[account] {
		if t.ID == id && !t.Deleted {
			return t
		}
	}
	return nil
}

func tunnelJSON(account string, t *Tunnel) map[string]any {
	status := "inactive"
	if len(t.Connectors) > 0 {
		status = "healthy"
	}
	conns := []map[string]any{}
	for _, c := range t.Connectors {
		for i, colo := range c.Colos {
			conns = append(conns, connJSON(c.ID, i, colo))
		}
	}
	return map[string]any{
		"id": t.ID, "account_tag": account, "name": t.Name, "status": status,
		"tun_type": "cfd_tunnel", "remote_config": !t.Local,
		"created_at": "2026-09-01T00:00:00Z", "deleted_at": nil, "connections": conns,
	}
}

func connJSON(connector string, i int, colo string) map[string]any {
	return map[string]any{
		"id": fmt.Sprintf("%s-c%d", connector, i), "colo_name": colo, "origin_ip": "203.0.113.7",
		"is_pending_reconnect": false, "opened_at": "2026-10-10T00:00:00Z",
	}
}

func (s *Server) tunnelRoutes(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, body map[string]any) {
	acc := parts[1]
	write := r.Method != http.MethodGet
	if !tok.has(PermTunnelEdit, acc) && (write || !tok.has(PermTunnelRead, acc)) {
		Fail(w, http.StatusForbidden, 10000, "Authentication error")
		return
	}
	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			s.listTunnels(w, r, acc)
		case http.MethodPost:
			s.createTunnel(w, acc, body)
		default:
			notFound(w, strings.Join(parts, "/"))
		}
		return
	}
	t := s.findTunnel(acc, parts[3])
	if t == nil {
		Fail(w, http.StatusNotFound, 1003, "Tunnel not found")
		return
	}
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		ok(w, tunnelJSON(acc, t), nil)
	case len(parts) == 4 && r.Method == http.MethodDelete:
		if len(t.Connectors) > 0 {
			Fail(w, http.StatusBadRequest, 1022, "Cannot delete a tunnel with active connections; clean them up first")
			return
		}
		t.Deleted = true
		ok(w, tunnelJSON(acc, t), nil)
	case len(parts) == 5 && parts[4] == "token" && r.Method == http.MethodGet:
		ok(w, "token-for-"+t.ID, nil)
	case len(parts) == 5 && parts[4] == "connections" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, c := range t.Connectors {
			conns := []map[string]any{}
			for i, colo := range c.Colos {
				conns = append(conns, connJSON(c.ID, i, colo))
			}
			out = append(out, map[string]any{
				"id": c.ID, "version": c.Version, "arch": c.Arch, "run_at": "2026-10-10T00:00:00Z",
				"features": []string{"serialized_headers"}, "conns": conns,
			})
		}
		ok(w, out, nil)
	case len(parts) == 5 && parts[4] == "connections" && r.Method == http.MethodDelete:
		t.Connectors = nil
		ok(w, nil, nil)
	case len(parts) == 5 && parts[4] == "configurations":
		s.tunnelConfig(w, r, acc, t, body)
	default:
		notFound(w, strings.Join(parts, "/"))
	}
}

func (s *Server) listTunnels(w http.ResponseWriter, r *http.Request, acc string) {
	var live []*Tunnel
	for _, t := range s.tunnels[acc] {
		if !t.Deleted {
			live = append(live, t)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].Name < live[j].Name })
	page, perPage := pageParams(r.URL.Query(), 20, 1, 1000)
	items, info := paginate(len(live), page, perPage)
	out := []map[string]any{}
	for _, i := range items {
		out = append(out, tunnelJSON(acc, live[i]))
	}
	ok(w, out, info)
}

func (s *Server) createTunnel(w http.ResponseWriter, acc string, body map[string]any) {
	name, _ := body["name"].(string)
	if name == "" {
		Fail(w, http.StatusBadRequest, 1001, "name is required")
		return
	}
	if secret, _ := body["tunnel_secret"].(string); len(secret) < 40 {
		Fail(w, http.StatusBadRequest, 1002, "tunnel_secret must be at least 32 bytes, base64 encoded")
		return
	}
	for _, t := range s.tunnels[acc] {
		if t.Name == name && !t.Deleted {
			Fail(w, http.StatusConflict, 1013, "A tunnel with this name already exists")
			return
		}
	}
	s.nextID++
	t := &Tunnel{ID: fmt.Sprintf("tun%03d", s.nextID), Name: name, Local: body["config_src"] == "local"}
	s.tunnels[acc] = append(s.tunnels[acc], t)
	ok(w, tunnelJSON(acc, t), nil)
}

func (s *Server) tunnelConfig(w http.ResponseWriter, r *http.Request, acc string, t *Tunnel, body map[string]any) {
	if t.Local {
		Fail(w, http.StatusBadRequest, 1017, "This tunnel is configured locally")
		return
	}
	switch r.Method {
	case http.MethodGet:
		ok(w, map[string]any{
			"tunnel_id": t.ID, "version": t.Version, "source": "cloudflare",
			"created_at": "2026-10-10T00:00:00Z", "config": copyMap(t.Config),
		}, nil)
	case http.MethodPut:
		cfg, _ := body["config"].(map[string]any)
		ingress, _ := cfg["ingress"].([]any)
		if len(ingress) == 0 {
			Fail(w, http.StatusBadRequest, 1056, "The ingress must have at least one rule")
			return
		}
		last, _ := ingress[len(ingress)-1].(map[string]any)
		if last["hostname"] != nil || last["path"] != nil {
			Fail(w, http.StatusBadRequest, 1056, "The last ingress rule must match all URLs (no hostname or path filter)")
			return
		}
		t.Config = copyMap(cfg)
		t.Version++
		ok(w, map[string]any{
			"tunnel_id": t.ID, "version": t.Version, "source": "cloudflare",
			"created_at": "2026-10-10T00:00:00Z", "config": copyMap(t.Config),
		}, nil)
	default:
		notFound(w, "configurations")
	}
}
