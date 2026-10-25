package logtosession

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("Logto session not found")

type Record struct {
	ID, Kind, Ciphertext string
	CreatedAt, ExpiresAt time.Time
}

type Repository interface {
	Create(context.Context, *Record) error
	// TakeFlow atomically consumes a flow, including expired flows.
	TakeFlow(context.Context, string, time.Time) (*Record, error)
	// WithSession serializes refreshes across processes with a database row lock.
	// Only the encrypted payload can change; expiry remains absolute.
	WithSession(context.Context, string, time.Time, func(*Record) error) error
	Delete(context.Context, string) error
	Purge(context.Context, time.Time) error
}
