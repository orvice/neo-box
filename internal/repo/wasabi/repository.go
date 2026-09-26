// Package wasabi persists the daily usage of Wasabi connections and how far
// each connection is synced. Rows are keyed by connection; callers scope
// access through the connection's owner.
package wasabi

import (
	"context"
	"errors"
	"time"

	"go.orx.me/apps/neo-box/internal/wasabi"
)

var ErrNotFound = errors.New("not found")

// SyncState tracks one connection's sync. Zero times are unset.
type SyncState struct {
	ConnectionID string
	// LastSyncedDay is the newest day with account data.
	LastSyncedDay time.Time
	LastSuccessAt time.Time
	// The 12-month backfill covers BackfillFrom through BackfillThrough so
	// far; BackfillCompletedAt is set once it reached the present.
	BackfillFrom        time.Time
	BackfillThrough     time.Time
	BackfillCompletedAt time.Time
	UpdatedAt           time.Time
}

type Repository interface {
	// UpsertUsage writes rows, replacing existing rows for the same
	// (connection, bucket, day).
	UpsertUsage(ctx context.Context, connectionID string, rows []wasabi.Usage) error
	// ListUsage returns one bucket's rows (the account's when bucket is
	// empty) with from <= day <= to, oldest first.
	ListUsage(ctx context.Context, connectionID, bucket string, from, to time.Time) ([]wasabi.Usage, error)
	// LatestBucketUsage returns the newest row of every bucket ever seen.
	LatestBucketUsage(ctx context.Context, connectionID string) ([]wasabi.Usage, error)
	// LatestAccountUsage returns the newest account-level row, or
	// ErrNotFound.
	LatestAccountUsage(ctx context.Context, connectionID string) (*wasabi.Usage, error)

	// GetSyncState returns ErrNotFound for a connection never synced.
	GetSyncState(ctx context.Context, connectionID string) (*SyncState, error)
	SaveSyncState(ctx context.Context, s *SyncState) error

	// DeleteConnectionData removes a connection's usage and sync state.
	DeleteConnectionData(ctx context.Context, connectionID string) error
}
