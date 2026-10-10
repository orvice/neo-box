// Package nocodb persists NocoDB Backup Policies, Snapshot metadata,
// Restores, and the attachment files snapshots use.
// Connections are generic (see internal/repo/connection); policies and
// snapshots refer to one by ConnectionID. Snapshot content lives in the blob
// store under Snapshot.ObjectKey; attachment files are stored once per user
// by sha256 and shared by the snapshots that use them. Every record is
// owned by one user; reads and writes are scoped by UserID.
package nocodb

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
)

// Policy schedules snapshots of one Base.
type Policy struct {
	ConnectionID string
	BaseID       string
	UserID       string
	Enabled      bool
	Cron         string
	Retention    int
	// IncludeAttachments makes snapshots store attachment files.
	IncludeAttachments bool
	UpdatedAt          time.Time
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
	ID           string `json:"id"`
	Title        string `json:"title"`
	RecordCount  int64  `json:"record_count"`
	FieldCount   int    `json:"field_count"`
	LinkCount    int64  `json:"link_count"`
	ViewCount    int    `json:"view_count,omitempty"`
	FileCount    int64  `json:"file_count,omitempty"`
	FileBytes    int64  `json:"file_bytes,omitempty"`
	FilesMissing int64  `json:"files_missing,omitempty"`
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
	ViewCount    int
	// AttachmentsIncluded is set when the snapshot stored attachment
	// files; FileCount and FileBytes count them (each distinct file of a
	// table once), FilesMissing those that could not be downloaded.
	AttachmentsIncluded bool
	FileCount           int64
	FileBytes           int64
	FilesMissing        int64
	Tables              []SnapshotTable
	CreatedAt           time.Time
	StartedAt           time.Time
	FinishedAt          time.Time
}

// File is an attachment file stored once per user under its sha256.
type File struct {
	UserID    string
	SHA256    string
	Size      int64
	CreatedAt time.Time
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

// RestoreStatus is where a Restore is in its life cycle.
type RestoreStatus string

const (
	RestorePending   RestoreStatus = "pending"
	RestoreRunning   RestoreStatus = "running"
	RestoreSucceeded RestoreStatus = "succeeded"
	RestoreFailed    RestoreStatus = "failed"
)

// RestoreWarning groups one kind of loss in one table (and view and
// field).
type RestoreWarning struct {
	Code    string `json:"code"`
	Table   string `json:"table,omitempty"`
	Field   string `json:"field,omitempty"`
	Count   int64  `json:"count"`
	Message string `json:"message"`
	View    string `json:"view,omitempty"`
}

// Restore is one rebuild of a Snapshot into a new Base on a target
// Connection. The source fields are copied from the snapshot.
type Restore struct {
	ID                 string
	UserID             string
	SnapshotID         string
	SourceConnectionID string
	SourceBaseID       string
	SourceBaseTitle    string
	TargetConnectionID string
	// TargetBaseID is empty until the new Base exists.
	TargetBaseID    string
	TargetBaseTitle string
	Status          RestoreStatus
	Error           string
	Progress        string
	TableCount      int
	RecordCount     int64
	LinkCount       int64
	FileCount       int64
	ViewCount       int
	Warnings        []RestoreWarning
	CreatedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
}

// Active reports whether the restore is pending or running.
func (r *Restore) Active() bool {
	return r.Status == RestorePending || r.Status == RestoreRunning
}

// RestoreFilter narrows ListRestores. Zero fields are ignored.
type RestoreFilter struct {
	UserID     string
	SnapshotID string
	// ConnectionID matches the source or the target connection.
	ConnectionID string
	// ActiveOnly keeps pending and running restores.
	ActiveOnly bool
	Limit      int
}

type Repository interface {
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
	// DeleteSnapshot deletes the snapshot and its file references.
	DeleteSnapshot(ctx context.Context, id string) error
	// FailUnfinishedSnapshots marks every pending/running snapshot failed;
	// called at startup because in-flight work does not survive a restart.
	FailUnfinishedSnapshots(ctx context.Context, reason string, at time.Time) (int64, error)

	CreateRestore(ctx context.Context, r *Restore) error
	GetRestore(ctx context.Context, userID, id string) (*Restore, error)
	// UpdateRestore overwrites the mutable fields: target Base ID, status,
	// error, progress, counts, warnings, and start/finish times.
	UpdateRestore(ctx context.Context, r *Restore) error
	// UpdateRestoreProgress writes only the progress text.
	UpdateRestoreProgress(ctx context.Context, id, progress string) error
	// UpdateRestoreTargetBase writes only the new Base's ID.
	UpdateRestoreTargetBase(ctx context.Context, id, baseID string) error
	// ListRestores returns matching restores, newest first.
	ListRestores(ctx context.Context, f RestoreFilter) ([]*Restore, error)
	// DeleteRestoresForConnection deletes restores whose source or target
	// is the connection.
	DeleteRestoresForConnection(ctx context.Context, connectionID string) error
	// FailUnfinishedRestores marks every pending/running restore failed;
	// called at startup, like FailUnfinishedSnapshots.
	FailUnfinishedRestores(ctx context.Context, reason string, at time.Time) (int64, error)

	// FileExists reports whether the user has the file stored.
	FileExists(ctx context.Context, userID, sha256 string) (bool, error)
	// CreateFile records a stored file; recording it again is a no-op.
	CreateFile(ctx context.Context, f *File) error
	// AddSnapshotFile records that a snapshot uses a file; adding it again
	// is a no-op.
	AddSnapshotFile(ctx context.Context, snapshotID, userID, sha256 string) error
	// DeleteSnapshotFiles forgets every file a snapshot uses.
	DeleteSnapshotFiles(ctx context.Context, snapshotID string) error
	// ListUnusedFiles returns the sha256 of each of the user's files that
	// no snapshot uses.
	ListUnusedFiles(ctx context.Context, userID string) ([]string, error)
	// DeleteFile deletes a file's record.
	DeleteFile(ctx context.Context, userID, sha256 string) error
	// FileSource returns the sha256 last stored for a connection's
	// attachment, identified by source and size.
	FileSource(ctx context.Context, connectionID, source string, size int64) (sha256 string, ok bool, err error)
	// PutFileSource records the sha256 of a connection's attachment.
	PutFileSource(ctx context.Context, connectionID, source string, size int64, sha256 string) error
	DeleteFileSourcesForConnection(ctx context.Context, connectionID string) error
}
