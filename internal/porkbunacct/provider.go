package porkbunacct

import (
	"context"
	"encoding/json"
	"fmt"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/porkbun"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
)

// ConnectionProvider returns the "porkbun" connection provider.
func (m *Manager) ConnectionProvider() connection.Provider {
	return provider{m: m}
}

type provider struct {
	m *Manager
}

func (provider) Type() string { return porkbun.ProviderType }

// Verify checks without changing anything that Porkbun accepts the API key
// pair via POST /ping.
func (p provider) Verify(ctx context.Context, config, secret json.RawMessage) error {
	var (
		cfg porkbun.ConnectionConfig
		sec porkbun.ConnectionSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	api, err := p.m.newClient(cfg.APIKey, sec.SecretAPIKey, nil)
	if err != nil {
		return err
	}
	if _, err := api.Ping(ctx); err != nil {
		if porkbun.IsAuthError(err) {
			return fmt.Errorf("Porkbun does not accept these API keys (invalid or revoked): %w", err)
		}
		if porkbun.IsPermissionDenied(err) {
			return fmt.Errorf("Porkbun rejected these API keys (check IP restrictions and permissions): %w", err)
		}
		return fmt.Errorf("cannot verify Porkbun API keys: %w", err)
	}
	return nil
}

// Cleanup drops the in-memory rate limiter for this connection. Nothing is
// stored in PostgreSQL or changed at Porkbun.
func (p provider) Cleanup(_ context.Context, c *connrepo.Connection) error {
	p.m.forget(c.ID)
	return nil
}
