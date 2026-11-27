package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/telegrambinding"
	"go.orx.me/apps/neo-box/internal/ent/telegramlinktoken"
	repo "go.orx.me/apps/neo-box/internal/repo/telegram"
)

// Store persists Telegram bindings and single-use link tokens in PostgreSQL.
type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) GetBindingByUser(ctx context.Context, userID string) (*repo.Binding, error) {
	if userID == "" {
		return nil, repo.ErrNotFound
	}
	row, err := s.client.TelegramBinding.Query().
		Where(telegrambinding.UserID(userID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("get telegram binding by user: %w", err)
	}
	return bindingFromEnt(row), nil
}

func (s *Store) GetBindingByTelegramUser(ctx context.Context, telegramUserID int64) (*repo.Binding, error) {
	if telegramUserID == 0 {
		return nil, repo.ErrNotFound
	}
	row, err := s.client.TelegramBinding.Query().
		Where(telegrambinding.TelegramUserID(telegramUserID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("get telegram binding by telegram user: %w", err)
	}
	return bindingFromEnt(row), nil
}

func (s *Store) SaveBinding(ctx context.Context, b *repo.Binding) error {
	if b == nil || b.ID == "" || b.UserID == "" || b.TelegramUserID == 0 {
		return errors.New("telegram binding invalid")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin save telegram binding tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	conflict, err := tx.TelegramBinding.Query().
		Where(
			telegrambinding.TelegramUserID(b.TelegramUserID),
			telegrambinding.UserIDNEQ(b.UserID),
		).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("check telegram binding conflict: %w", err)
	}
	if conflict {
		return repo.ErrTelegramUserAlreadyBound
	}

	existing, err := tx.TelegramBinding.Query().
		Where(telegrambinding.UserID(b.UserID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("load existing telegram binding: %w", err)
	}

	if existing != nil {
		if existing.TelegramUserID == b.TelegramUserID {
			if err := tx.TelegramBinding.UpdateOneID(existing.ID).
				SetChatID(b.ChatID).
				SetTelegramUsername(b.TelegramUsername).
				SetDisplayName(b.DisplayName).
				SetUpdatedAt(b.UpdatedAt).
				Exec(ctx); err != nil {
				return fmt.Errorf("update telegram binding: %w", err)
			}
			b.ID = existing.ID
			b.CreatedAt = existing.CreatedAt
			return tx.Commit()
		}
		if err := tx.TelegramBinding.DeleteOneID(existing.ID).Exec(ctx); err != nil {
			return fmt.Errorf("replace telegram binding: %w", err)
		}
	}

	err = tx.TelegramBinding.Create().
		SetID(b.ID).
		SetUserID(b.UserID).
		SetTelegramUserID(b.TelegramUserID).
		SetChatID(b.ChatID).
		SetTelegramUsername(b.TelegramUsername).
		SetDisplayName(b.DisplayName).
		SetCreatedAt(b.CreatedAt).
		SetUpdatedAt(b.UpdatedAt).
		Exec(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return repo.ErrTelegramUserAlreadyBound
		}
		return fmt.Errorf("insert telegram binding: %w", err)
	}
	return tx.Commit()
}

func (s *Store) DeleteBindingByUser(ctx context.Context, userID string) error {
	if userID == "" {
		return repo.ErrNotFound
	}
	n, err := s.client.TelegramBinding.Delete().
		Where(telegrambinding.UserID(userID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete telegram binding by user: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteBindingByTelegramUser(ctx context.Context, telegramUserID int64) error {
	if telegramUserID == 0 {
		return repo.ErrNotFound
	}
	n, err := s.client.TelegramBinding.Delete().
		Where(telegrambinding.TelegramUserID(telegramUserID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete telegram binding by telegram user: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) CreateLinkToken(ctx context.Context, tok *repo.LinkToken) error {
	if tok == nil || tok.TokenHash == "" || tok.UserID == "" {
		return errors.New("telegram link token invalid")
	}
	err := s.client.TelegramLinkToken.Create().
		SetID(tok.TokenHash).
		SetUserID(tok.UserID).
		SetCreatedAt(tok.CreatedAt).
		SetExpiresAt(tok.ExpiresAt).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert telegram link token: %w", err)
	}
	return nil
}

// ConsumeLinkToken reads the token, then deletes it only if unexpired.
// Across multiple pods, only the caller whose DELETE removes the row wins.
func (s *Store) ConsumeLinkToken(ctx context.Context, tokenHash string, now time.Time) (*repo.LinkToken, error) {
	if tokenHash == "" {
		return nil, repo.ErrNotFound
	}
	row, err := s.client.TelegramLinkToken.Get(ctx, tokenHash)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("consume telegram link token: %w", err)
	}
	n, err := s.client.TelegramLinkToken.Delete().
		Where(telegramlinktoken.ID(tokenHash), telegramlinktoken.ExpiresAtGT(now)).
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("consume telegram link token: %w", err)
	}
	if n == 0 {
		return nil, repo.ErrNotFound
	}
	return &repo.LinkToken{
		TokenHash: row.ID,
		UserID:    row.UserID,
		CreatedAt: row.CreatedAt,
		ExpiresAt: row.ExpiresAt,
	}, nil
}

func (s *Store) DeleteExpiredLinkTokens(ctx context.Context, now time.Time) (int, error) {
	n, err := s.client.TelegramLinkToken.Delete().
		Where(telegramlinktoken.ExpiresAtLT(now)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired telegram link tokens: %w", err)
	}
	return n, nil
}

func bindingFromEnt(r *ent.TelegramBinding) *repo.Binding {
	return &repo.Binding{
		ID:               r.ID,
		UserID:           r.UserID,
		TelegramUserID:   r.TelegramUserID,
		ChatID:           r.ChatID,
		TelegramUsername: r.TelegramUsername,
		DisplayName:      r.DisplayName,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}
