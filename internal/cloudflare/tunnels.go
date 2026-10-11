package cloudflare

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// --- Tunnels ---

// TunnelConn is one connection a cloudflared connector holds open to
// Cloudflare's edge.
type TunnelConn struct {
	ID                 string     `json:"id"`
	ColoName           string     `json:"colo_name"`
	OriginIP           string     `json:"origin_ip"`
	IsPendingReconnect bool       `json:"is_pending_reconnect"`
	OpenedAt           *time.Time `json:"opened_at"`
}

// Tunnel is a Cloudflare Tunnel as listed. Connections is the legacy flat
// list of its live connections, which is enough to count them.
type Tunnel struct {
	ID         string `json:"id"`
	AccountTag string `json:"account_tag"`
	Name       string `json:"name"`
	// Status is inactive (never connected), degraded, healthy or down.
	Status  string `json:"status"`
	TunType string `json:"tun_type"`
	// RemoteConfig is true when the tunnel's ingress lives at Cloudflare
	// (created from the dashboard or with config_src cloudflare) and false
	// when cloudflared reads a local config file.
	RemoteConfig    bool         `json:"remote_config"`
	CreatedAt       time.Time    `json:"created_at"`
	DeletedAt       *time.Time   `json:"deleted_at"`
	ConnsActiveAt   *time.Time   `json:"conns_active_at"`
	ConnsInactiveAt *time.Time   `json:"conns_inactive_at"`
	Connections     []TunnelConn `json:"connections"`
}

// TunnelConnector is one running cloudflared instance and its connections.
type TunnelConnector struct {
	ID       string       `json:"id"`
	Version  string       `json:"version"`
	Arch     string       `json:"arch"`
	RunAt    *time.Time   `json:"run_at"`
	Features []string     `json:"features"`
	Conns    []TunnelConn `json:"conns"`
}

// ListTunnels returns every cloudflared Tunnel of the Account that has not
// been deleted.
func (c *Client) ListTunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	var all []Tunnel
	q := url.Values{"is_deleted": {"false"}}
	err := c.eachPageWith(ctx, "/accounts/"+esc(accountID)+"/cfd_tunnel", 100, q, func(raw json.RawMessage) error {
		var page []Tunnel
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

func tunnelPath(accountID, tunnelID string) string {
	return "/accounts/" + esc(accountID) + "/cfd_tunnel/" + esc(tunnelID)
}

func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (*Tunnel, error) {
	var t Tunnel
	if _, err := c.get(ctx, tunnelPath(accountID, tunnelID), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTunnelConnectors returns the connectors of a Tunnel that are
// connected now.
func (c *Client) ListTunnelConnectors(ctx context.Context, accountID, tunnelID string) ([]TunnelConnector, error) {
	var out []TunnelConnector
	_, err := c.get(ctx, tunnelPath(accountID, tunnelID)+"/connections", nil, &out)
	return out, err
}

// CreateTunnel creates a remotely managed Tunnel: its ingress is edited at
// Cloudflare and cloudflared runs with the Tunnel's token. Neo Box makes
// the tunnel secret, so the token is only ever fetched, never derived.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (*Tunnel, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	body := map[string]any{
		"name":          name,
		"config_src":    "cloudflare",
		"tunnel_secret": base64.StdEncoding.EncodeToString(secret),
	}
	var t Tunnel
	if err := c.send(ctx, http.MethodPost, "/accounts/"+esc(accountID)+"/cfd_tunnel", body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTunnel deletes a Tunnel. Cloudflare refuses while it has live
// connections.
func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	return c.send(ctx, http.MethodDelete, tunnelPath(accountID, tunnelID), nil, nil)
}

// CleanupTunnelConnections closes the Tunnel's stale connections.
func (c *Client) CleanupTunnelConnections(ctx context.Context, accountID, tunnelID string) error {
	return c.send(ctx, http.MethodDelete, tunnelPath(accountID, tunnelID)+"/connections", nil, nil)
}

// TunnelToken returns the token cloudflared runs a remotely managed Tunnel
// with. It is a credential: never log it.
func (c *Client) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	if _, err := c.get(ctx, tunnelPath(accountID, tunnelID)+"/token", nil, &token); err != nil {
		return "", err
	}
	return token, nil
}

// TunnelConfiguration is a remotely managed Tunnel's configuration as
// Cloudflare keeps it.
type TunnelConfiguration struct {
	TunnelID  string       `json:"tunnel_id"`
	Version   int          `json:"version"`
	Source    string       `json:"source"`
	CreatedAt time.Time    `json:"created_at"`
	Config    TunnelConfig `json:"config"`
}

// GetTunnelConfiguration reads the Tunnel's configuration. A Tunnel that
// was never configured has none: the result then is empty.
func (c *Client) GetTunnelConfiguration(ctx context.Context, accountID, tunnelID string) (*TunnelConfiguration, error) {
	var cfg TunnelConfiguration
	if _, err := c.get(ctx, tunnelPath(accountID, tunnelID)+"/configurations", nil, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PutTunnelConfiguration replaces the Tunnel's whole configuration. Callers
// read it first and change only what they mean to, since a write replaces
// everything the Tunnel was configured with.
func (c *Client) PutTunnelConfiguration(ctx context.Context, accountID, tunnelID string, cfg TunnelConfig) (*TunnelConfiguration, error) {
	var out TunnelConfiguration
	if err := c.send(ctx, http.MethodPut, tunnelPath(accountID, tunnelID)+"/configurations", map[string]any{"config": cfg}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
