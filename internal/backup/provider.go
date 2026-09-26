package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

// ConnectionProvider returns the "nocodb" connection provider, backed by m.
func (m *Manager) ConnectionProvider() connection.Provider {
	return provider{m: m}
}

type provider struct {
	m *Manager
}

func (provider) Type() string { return nocodb.ProviderType }

// Verify checks that the URL and token can list Bases.
func (p provider) Verify(ctx context.Context, config, secret json.RawMessage) error {
	var (
		cfg nocodb.ConnectionConfig
		sec nocodb.ConnectionSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	api, err := p.m.newClient(cfg, sec.APIToken)
	if err != nil {
		return err
	}
	if _, err := api.ListBases(ctx); err != nil {
		return fmt.Errorf("cannot reach NocoDB with this URL and token: %w", err)
	}
	return nil
}

// Cleanup deletes the connection's snapshots (content included) and Backup
// Policies. It refuses while a snapshot is pending or running.
func (p provider) Cleanup(ctx context.Context, c *connrepo.Connection) error {
	m := p.m
	snaps, err := m.repo.ListSnapshots(ctx, repo.SnapshotFilter{UserID: c.UserID, ConnectionID: c.ID})
	if err != nil {
		return err
	}
	for _, snap := range snaps {
		if snap.Status == repo.StatusPending || snap.Status == repo.StatusRunning {
			return fmt.Errorf("%w: a snapshot is in progress; try again when it finishes", connection.ErrBusy)
		}
	}
	for _, snap := range snaps {
		if err := m.DeleteSnapshot(ctx, snap); err != nil {
			return err
		}
	}
	if err := m.repo.DeletePoliciesForConnection(ctx, c.ID); err != nil {
		return err
	}
	if err := m.ReloadSchedules(ctx); err != nil {
		log.FromContext(ctx).Warn("reload backup schedules failed", "err", err)
	}
	return nil
}
