package wasabisync

import (
	"context"
	"encoding/json"
	"fmt"

	"go.orx.me/apps/neo-box/internal/connection"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// ConnectionProvider returns the "wasabi" connection provider, backed by m.
func (m *Manager) ConnectionProvider() connection.Provider {
	return provider{m: m}
}

type provider struct {
	m *Manager
}

var _ connection.Activator = provider{}

func (provider) Type() string { return wasabi.ProviderType }

// Verify reads the last two days of account usage with the key.
func (p provider) Verify(ctx context.Context, config, secret json.RawMessage) error {
	var (
		cfg wasabi.ConnectionConfig
		sec wasabi.ConnectionSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	api, err := p.m.newClient(cfg.AccessKeyID, sec.SecretKey)
	if err != nil {
		return err
	}
	today := wasabi.DayOf(p.m.now())
	if _, err := api.AccountUsage(ctx, today.AddDate(0, 0, -1), today); err != nil {
		return fmt.Errorf("cannot read Wasabi usage with this key: %w", err)
	}
	return nil
}

// Cleanup deletes the connection's usage and sync state. It refuses while
// the connection's sync is running; a queued one is dropped.
func (p provider) Cleanup(ctx context.Context, c *connrepo.Connection) error {
	if p.m.forget(c.ID) {
		return fmt.Errorf("%w: a sync is in progress; try again when it finishes", connection.ErrBusy)
	}
	return p.m.repo.DeleteConnectionData(ctx, c.ID)
}

// Activate starts a sync, which backfills a new connection.
func (p provider) Activate(_ context.Context, c *connrepo.Connection) {
	p.m.enqueue(job{connectionID: c.ID})
}
