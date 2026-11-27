// Package telegram persists user links to the server-wide Telegram bot and
// short-lived single-use link tokens.
package telegram

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound means no binding or link token matched.
	ErrNotFound = errors.New("telegram record not found")
	// ErrTelegramUserAlreadyBound means the Telegram user ID is already
	// linked to a different Neo Box user.
	ErrTelegramUserAlreadyBound = errors.New("telegram account is already linked to another user")
)

// Binding links one Neo Box user to one Telegram private chat.
type Binding struct {
	ID               string
	UserID           string
	TelegramUserID   int64
	ChatID           int64
	TelegramUsername string
	DisplayName      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LinkToken is a single-use token issued from the dashboard so a user can
// bind their Telegram private chat via /start <token>. TokenHash is the
// sha256 hex digest of the raw token; the raw token is never stored.
type LinkToken struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Repository interface {
	GetBindingByUser(ctx context.Context, userID string) (*Binding, error)
	GetBindingByTelegramUser(ctx context.Context, telegramUserID int64) (*Binding, error)
	// SaveBinding creates or replaces the binding for b.UserID. Returns
	// ErrTelegramUserAlreadyBound if b.TelegramUserID is bound to another user.
	SaveBinding(ctx context.Context, b *Binding) error
	DeleteBindingByUser(ctx context.Context, userID string) error
	DeleteBindingByTelegramUser(ctx context.Context, telegramUserID int64) error

	CreateLinkToken(ctx context.Context, tok *LinkToken) error
	// ConsumeLinkToken atomically consumes a single unexpired token and
	// returns it. Returns ErrNotFound if missing, expired, or already used.
	ConsumeLinkToken(ctx context.Context, tokenHash string, now time.Time) (*LinkToken, error)
	DeleteExpiredLinkTokens(ctx context.Context, now time.Time) (int, error)
}
