package googleworkspace

import (
	"context"
	"encoding/json"
	"go.orx.me/apps/neo-box/internal/connection"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
)

type Provider struct{ Repo repo.Repository }

var _ connection.Provider = Provider{}

func (Provider) Type() string { return ProviderType }
func (Provider) Verify(ctx context.Context, config, secret json.RawMessage) error {
	var cfg ConnectionConfig
	var sec ConnectionSecret
	if err := json.Unmarshal(config, &cfg); err != nil {
		return err
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return err
	}
	return VerifyKey(ctx, cfg, sec)
}
func (p Provider) Cleanup(ctx context.Context, c *connrepo.Connection) error {
	return p.Repo.DeleteConnectionData(ctx, c.ID)
}
