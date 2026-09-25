package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	entoauthstate "go.orx.me/apps/neo-box/internal/ent/oauthstate"
	"go.orx.me/apps/neo-box/internal/repo/oauthstate"
)

// Store persists OAuth state in PostgreSQL. Expired rows are ignored by
// Consume and removed by DeleteExpired.
type Store struct {
	client *ent.Client
}

var _ oauthstate.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) Create(ctx context.Context, entry *oauthstate.Entry) error {
	if entry == nil || entry.State == "" {
		return errors.New("oauth state entry invalid")
	}
	err := s.client.OAuthState.Create().
		SetID(entry.State).
		SetProvider(entry.Provider).
		SetRedirectURI(entry.RedirectURI).
		SetCreatedAt(entry.CreatedAt).
		SetExpiresAt(entry.ExpiresAt).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert oauth state: %w", err)
	}
	return nil
}

// Consume reads the entry, then deletes it only if it is still unexpired.
// Of two concurrent callers, only the one whose DELETE removes the row wins.
func (s *Store) Consume(ctx context.Context, state string, now time.Time) (*oauthstate.Entry, error) {
	if state == "" {
		return nil, oauthstate.ErrNotFound
	}
	row, err := s.client.OAuthState.Get(ctx, state)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, oauthstate.ErrNotFound
		}
		return nil, fmt.Errorf("consume oauth state: %w", err)
	}
	n, err := s.client.OAuthState.Delete().
		Where(entoauthstate.ID(state), entoauthstate.ExpiresAtGT(now)).
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("consume oauth state: %w", err)
	}
	if n == 0 {
		return nil, oauthstate.ErrNotFound
	}
	return &oauthstate.Entry{
		State:       row.ID,
		Provider:    row.Provider,
		RedirectURI: row.RedirectURI,
		CreatedAt:   row.CreatedAt,
		ExpiresAt:   row.ExpiresAt,
	}, nil
}

// DeleteExpired removes states that expired before now.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	n, err := s.client.OAuthState.Delete().Where(entoauthstate.ExpiresAtLT(now)).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth states: %w", err)
	}
	return n, nil
}
