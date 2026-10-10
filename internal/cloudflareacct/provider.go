package cloudflareacct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/connection"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
)

// ConnectionProvider returns the "cloudflare" connection provider.
func (m *Manager) ConnectionProvider() connection.Provider {
	return provider{m: m}
}

type provider struct {
	m *Manager
}

func (provider) Type() string { return cloudflare.ProviderType }

// Verify checks, without changing anything, that Cloudflare accepts the
// token and that the token reaches the Account: a valid token alone is not
// enough. It does not ask for every permission; one that shows the Account
// will do.
func (p provider) Verify(ctx context.Context, config, secret json.RawMessage) error {
	var (
		cfg cloudflare.ConnectionConfig
		sec cloudflare.ConnectionSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode secret: %w", err)
	}
	api, err := p.m.newClient(sec.APIToken, nil)
	if err != nil {
		return err
	}
	if err := api.VerifyToken(ctx, cfg.AccountID); err != nil {
		if cloudflare.IsAuthError(err) {
			return fmt.Errorf("Cloudflare does not accept this API token (invalid, expired or revoked): %w", err)
		}
		return fmt.Errorf("cannot verify the API token: %w", err)
	}
	return reachAccount(ctx, api, cfg.AccountID)
}

// errAccountUnreachable is when nothing the token may read shows the
// Account.
var errAccountUnreachable = errors.New("the API token cannot reach this account: check the Account ID, " +
	"and that the token's account resources include it with at least one of " +
	"Zone Read, Workers Scripts Read, Cloudflare Pages Read or Account Settings Read")

// reachAccount looks for anything that shows the token reaches the
// Account, in the order the commonest tokens allow. Refusals move on to the
// next check; any other failure stops, since the answer is then unknown.
func reachAccount(ctx context.Context, api *cloudflare.Client, accountID string) error {
	checks := []func() (bool, error){
		func() (bool, error) {
			a, err := api.GetAccount(ctx, accountID)
			return err == nil && a.ID == accountID, err
		},
		func() (bool, error) {
			// An empty list proves nothing: the token may see no Zones of
			// this Account, or not reach it at all.
			zones, _, err := api.ListZones(ctx, cloudflare.ZoneQuery{AccountID: accountID, PerPage: 5})
			for _, z := range zones {
				if z.Account.ID == accountID {
					return true, nil
				}
			}
			return false, err
		},
		func() (bool, error) {
			_, err := api.ListWorkerScripts(ctx, accountID)
			return err == nil, err
		},
		func() (bool, error) {
			_, err := api.ListPagesProjects(ctx, accountID)
			return err == nil, err
		},
	}
	for _, check := range checks {
		ok, err := check()
		if ok {
			return nil
		}
		if err != nil && !refused(err) {
			return fmt.Errorf("cannot check access to the account: %w", err)
		}
	}
	return errAccountUnreachable
}

// refused is a check the token may not make, or an Account it cannot see.
func refused(err error) bool {
	return cloudflare.IsPermissionDenied(err) || cloudflare.IsNotFound(err) || cloudflare.IsAuthError(err) || cloudflare.IsRejected(err)
}

// Cleanup deletes the connection's DNS operation log. Nothing changes at
// Cloudflare.
func (p provider) Cleanup(ctx context.Context, c *connrepo.Connection) error {
	if err := p.m.repo.DeleteConnectionData(ctx, c.ID); err != nil {
		return err
	}
	p.m.forget(c.ID)
	return nil
}
