// Package nocodb persists NocoDB Connections, Backup Policies, and Snapshot
// metadata. Snapshot content lives in the blob store under Snapshot.ObjectKey.
// Every record is owned by one user; reads and writes are scoped by UserID.
package nocodb

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
)

// Connection is one NocoDB instance + API token owned by a user.
type Connection struct {
	ID      string
	UserID  string
	Name    string
	BaseURL string
	// TokenCiphertext is the secretbox-encrypted API token.
	TokenCiphertext string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Policy schedules snapshots of one Base.
type Policy struct {
	ConnectionID string
	BaseID       string
	UserID       string
	Enabled      bool
	Cron         string
	Retention    int
	UpdatedAt    time.Time
}

type SnapshotStatus string

const (
	StatusPending   SnapshotStatus = "pending"
	StatusRunning   SnapshotStatus = "running"
	StatusSucceeded SnapshotStatus = "succeeded"
	StatusFailed    SnapshotStatus = "failed"
)

type SnapshotTrigger string

const (
	TriggerManual    SnapshotTrigger = "manual"
	TriggerScheduled SnapshotTrigger = "scheduled"
)

type SnapshotTable struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	RecordCount int64  `json:"record_count"`
	FieldCount  int    `json:"field_count"`
	LinkCount   int64  `json:"link_count"`
}

// Snapshot is the metadata of one capture of a Base.
type Snapshot struct {
	ID           string
	UserID       string
	ConnectionID string
	BaseID       string
	BaseTitle    string
	Status       SnapshotStatus
	Trigger      SnapshotTrigger
	Error        string
	Progress     string
	ObjectKey    string
	SizeBytes    int64
	RecordCount  int64
	LinkCount    int64
	Tables       []SnapshotTable
	CreatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
}

// SnapshotFilter narrows ListSnapshots. Zero fields are ignored.
type SnapshotFilter struct {
	UserID       string
	ConnectionID string
	BaseID       string
	Trigger      SnapshotTrigger
	Status       SnapshotStatus
	Limit        int
}

type Repository interface {
	CreateConnection(ctx context.Context, c *Connection) error
	GetConnection(ctx context.Context, userID, id string) (*Connection, error)
	// GetConnectionByID bypasses owner scoping; used by the scheduler.
	GetConnectionByID(ctx context.Context, id string) (*Connection, error)
	ListConnections(ctx context.Context, userID string) ([]*Connection, error)
	UpdateConnection(ctx context.Context, c *Connection) error
	DeleteConnection(ctx context.Context, userID, id string) error

	UpsertPolicy(ctx context.Context, p *Policy) error
	ListPolicies(ctx context.Context, userID, connectionID string) ([]*Policy, error)
	// ListEnabledPolicies returns every enabled policy across users.
	ListEnabledPolicies(ctx context.Context) ([]*Policy, error)
	DeletePoliciesForConnection(ctx context.Context, connectionID string) error

	CreateSnapshot(ctx context.Context, s *Snapshot) error
	GetSnapshot(ctx context.Context, userID, id string) (*Snapshot, error)
	UpdateSnapshot(ctx context.Context, s *Snapshot) error
	// UpdateSnapshotProgress writes only the progress text.
	UpdateSnapshotProgress(ctx context.Context, id, progress string) error
	ListSnapshots(ctx context.Context, f SnapshotFilter) ([]*Snapshot, error)
	DeleteSnapshot(ctx context.Context, id string) error
	// FailUnfinishedSnapshots marks every pending/running snapshot failed;
	// called at startup because in-flight work does not survive a restart.
	FailUnfinishedSnapshots(ctx context.Context, reason string, at time.Time) (int64, error)
}
