package postgres

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/nocodbbackuppolicy"
	"go.orx.me/apps/neo-box/internal/ent/nocodbfile"
	"go.orx.me/apps/neo-box/internal/ent/nocodbfilesource"
	"go.orx.me/apps/neo-box/internal/ent/nocodbrestore"
	"go.orx.me/apps/neo-box/internal/ent/nocodbsnapshot"
	"go.orx.me/apps/neo-box/internal/ent/nocodbsnapshotfile"
	"go.orx.me/apps/neo-box/internal/ent/predicate"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

// --- policies ---

func (s *Store) UpsertPolicy(ctx context.Context, p *repo.Policy) error {
	err := s.client.NocoDBBackupPolicy.Create().
		SetConnectionID(p.ConnectionID).
		SetBaseID(p.BaseID).
		SetUserID(p.UserID).
		SetEnabled(p.Enabled).
		SetCron(p.Cron).
		SetRetention(p.Retention).
		SetIncludeAttachments(p.IncludeAttachments).
		SetUpdatedAt(p.UpdatedAt).
		OnConflictColumns(nocodbbackuppolicy.FieldConnectionID, nocodbbackuppolicy.FieldBaseID).
		UpdateNewValues().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("upsert policy: %w", err)
	}
	return nil
}

func (s *Store) ListPolicies(ctx context.Context, userID, connectionID string) ([]*repo.Policy, error) {
	return s.findPolicies(ctx, nocodbbackuppolicy.UserID(userID), nocodbbackuppolicy.ConnectionID(connectionID))
}

func (s *Store) ListEnabledPolicies(ctx context.Context) ([]*repo.Policy, error) {
	return s.findPolicies(ctx, nocodbbackuppolicy.Enabled(true))
}

func (s *Store) findPolicies(ctx context.Context, ps ...predicate.NocoDBBackupPolicy) ([]*repo.Policy, error) {
	rows, err := s.client.NocoDBBackupPolicy.Query().Where(ps...).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	out := make([]*repo.Policy, 0, len(rows))
	for _, row := range rows {
		out = append(out, policyFromRow(row))
	}
	return out, nil
}

func (s *Store) DeletePoliciesForConnection(ctx context.Context, connectionID string) error {
	_, err := s.client.NocoDBBackupPolicy.Delete().
		Where(nocodbbackuppolicy.ConnectionID(connectionID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete policies: %w", err)
	}
	return nil
}

// --- snapshots ---

func (s *Store) CreateSnapshot(ctx context.Context, snap *repo.Snapshot) error {
	c := s.client.NocoDBSnapshot.Create().
		SetID(snap.ID).
		SetUserID(snap.UserID).
		SetConnectionID(snap.ConnectionID).
		SetBaseID(snap.BaseID).
		SetBaseTitle(snap.BaseTitle).
		SetStatus(string(snap.Status)).
		SetTrigger(string(snap.Trigger)).
		SetError(snap.Error).
		SetProgress(snap.Progress).
		SetObjectKey(snap.ObjectKey).
		SetSizeBytes(snap.SizeBytes).
		SetRecordCount(snap.RecordCount).
		SetLinkCount(snap.LinkCount).
		SetViewCount(snap.ViewCount).
		SetHookCount(snap.HookCount).
		SetAttachmentsIncluded(snap.AttachmentsIncluded).
		SetFileCount(snap.FileCount).
		SetFileBytes(snap.FileBytes).
		SetFilesMissing(snap.FilesMissing).
		SetCreatedAt(snap.CreatedAt).
		SetNillableStartedAt(nilIfZero(snap.StartedAt)).
		SetNillableFinishedAt(nilIfZero(snap.FinishedAt))
	if snap.Tables != nil {
		c.SetTables(snap.Tables)
	}
	if err := c.Exec(ctx); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

func (s *Store) GetSnapshot(ctx context.Context, userID, id string) (*repo.Snapshot, error) {
	q := s.client.NocoDBSnapshot.Query().Where(nocodbsnapshot.ID(id))
	if userID != "" {
		q = q.Where(nocodbsnapshot.UserID(userID))
	}
	row, err := q.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find snapshot: %w", err)
	}
	return snapshotFromRow(row), nil
}

// UpdateSnapshot overwrites every mutable field. Owner, Connection, Base,
// trigger, and creation time are fixed at CreateSnapshot.
func (s *Store) UpdateSnapshot(ctx context.Context, snap *repo.Snapshot) error {
	u := s.client.NocoDBSnapshot.UpdateOneID(snap.ID).
		SetBaseTitle(snap.BaseTitle).
		SetStatus(string(snap.Status)).
		SetError(snap.Error).
		SetProgress(snap.Progress).
		SetObjectKey(snap.ObjectKey).
		SetSizeBytes(snap.SizeBytes).
		SetRecordCount(snap.RecordCount).
		SetLinkCount(snap.LinkCount).
		SetViewCount(snap.ViewCount).
		SetHookCount(snap.HookCount).
		SetAttachmentsIncluded(snap.AttachmentsIncluded).
		SetFileCount(snap.FileCount).
		SetFileBytes(snap.FileBytes).
		SetFilesMissing(snap.FilesMissing)
	if snap.Tables == nil {
		u.ClearTables()
	} else {
		u.SetTables(snap.Tables)
	}
	if snap.StartedAt.IsZero() {
		u.ClearStartedAt()
	} else {
		u.SetStartedAt(snap.StartedAt)
	}
	if snap.FinishedAt.IsZero() {
		u.ClearFinishedAt()
	} else {
		u.SetFinishedAt(snap.FinishedAt)
	}
	if err := u.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return repo.ErrNotFound
		}
		return fmt.Errorf("update snapshot: %w", err)
	}
	return nil
}

func (s *Store) UpdateSnapshotProgress(ctx context.Context, id, progress string) error {
	return s.client.NocoDBSnapshot.Update().
		Where(nocodbsnapshot.ID(id)).
		SetProgress(progress).
		Exec(ctx)
}

func (s *Store) ListSnapshots(ctx context.Context, f repo.SnapshotFilter) ([]*repo.Snapshot, error) {
	q := s.client.NocoDBSnapshot.Query()
	if f.UserID != "" {
		q = q.Where(nocodbsnapshot.UserID(f.UserID))
	}
	if f.ConnectionID != "" {
		q = q.Where(nocodbsnapshot.ConnectionID(f.ConnectionID))
	}
	if f.BaseID != "" {
		q = q.Where(nocodbsnapshot.BaseID(f.BaseID))
	}
	if f.Trigger != "" {
		q = q.Where(nocodbsnapshot.Trigger(string(f.Trigger)))
	}
	if f.Status != "" {
		q = q.Where(nocodbsnapshot.Status(string(f.Status)))
	}
	q = q.Order(ent.Desc(nocodbsnapshot.FieldCreatedAt))
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	out := make([]*repo.Snapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotFromRow(row))
	}
	return out, nil
}

func (s *Store) DeleteSnapshot(ctx context.Context, id string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	if _, err := tx.NocoDBSnapshotFile.Delete().Where(nocodbsnapshotfile.SnapshotID(id)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete snapshot files: %w", err)
	}
	if _, err := tx.NocoDBSnapshot.Delete().Where(nocodbsnapshot.ID(id)).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete snapshot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

func (s *Store) FailUnfinishedSnapshots(ctx context.Context, reason string, at time.Time) (int64, error) {
	n, err := s.client.NocoDBSnapshot.Update().
		Where(nocodbsnapshot.StatusIn(string(repo.StatusPending), string(repo.StatusRunning))).
		SetStatus(string(repo.StatusFailed)).
		SetError(reason).
		SetFinishedAt(at).
		SetProgress("").
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("fail unfinished snapshots: %w", err)
	}
	return int64(n), nil
}

// --- restores ---

func (s *Store) CreateRestore(ctx context.Context, r *repo.Restore) error {
	c := s.client.NocoDBRestore.Create().
		SetID(r.ID).
		SetUserID(r.UserID).
		SetSnapshotID(r.SnapshotID).
		SetSourceConnectionID(r.SourceConnectionID).
		SetSourceBaseID(r.SourceBaseID).
		SetSourceBaseTitle(r.SourceBaseTitle).
		SetTargetConnectionID(r.TargetConnectionID).
		SetTargetBaseID(r.TargetBaseID).
		SetTargetBaseTitle(r.TargetBaseTitle).
		SetStatus(string(r.Status)).
		SetError(r.Error).
		SetProgress(r.Progress).
		SetTableCount(r.TableCount).
		SetRecordCount(r.RecordCount).
		SetLinkCount(r.LinkCount).
		SetFileCount(r.FileCount).
		SetViewCount(r.ViewCount).
		SetHookCount(r.HookCount).
		SetCreatedAt(r.CreatedAt).
		SetNillableStartedAt(nilIfZero(r.StartedAt)).
		SetNillableFinishedAt(nilIfZero(r.FinishedAt))
	if r.Warnings != nil {
		c.SetWarnings(r.Warnings)
	}
	if err := c.Exec(ctx); err != nil {
		return fmt.Errorf("insert restore: %w", err)
	}
	return nil
}

func (s *Store) GetRestore(ctx context.Context, userID, id string) (*repo.Restore, error) {
	q := s.client.NocoDBRestore.Query().Where(nocodbrestore.ID(id))
	if userID != "" {
		q = q.Where(nocodbrestore.UserID(userID))
	}
	row, err := q.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find restore: %w", err)
	}
	return restoreFromRow(row), nil
}

func (s *Store) UpdateRestore(ctx context.Context, r *repo.Restore) error {
	u := s.client.NocoDBRestore.UpdateOneID(r.ID).
		SetTargetBaseID(r.TargetBaseID).
		SetStatus(string(r.Status)).
		SetError(r.Error).
		SetProgress(r.Progress).
		SetTableCount(r.TableCount).
		SetRecordCount(r.RecordCount).
		SetLinkCount(r.LinkCount).
		SetFileCount(r.FileCount).
		SetViewCount(r.ViewCount).
		SetHookCount(r.HookCount)
	if r.Warnings == nil {
		u.ClearWarnings()
	} else {
		u.SetWarnings(r.Warnings)
	}
	if r.StartedAt.IsZero() {
		u.ClearStartedAt()
	} else {
		u.SetStartedAt(r.StartedAt)
	}
	if r.FinishedAt.IsZero() {
		u.ClearFinishedAt()
	} else {
		u.SetFinishedAt(r.FinishedAt)
	}
	if err := u.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return repo.ErrNotFound
		}
		return fmt.Errorf("update restore: %w", err)
	}
	return nil
}

func (s *Store) UpdateRestoreProgress(ctx context.Context, id, progress string) error {
	return s.client.NocoDBRestore.Update().
		Where(nocodbrestore.ID(id)).
		SetProgress(progress).
		Exec(ctx)
}

func (s *Store) UpdateRestoreTargetBase(ctx context.Context, id, baseID string) error {
	return s.client.NocoDBRestore.Update().
		Where(nocodbrestore.ID(id)).
		SetTargetBaseID(baseID).
		Exec(ctx)
}

func (s *Store) ListRestores(ctx context.Context, f repo.RestoreFilter) ([]*repo.Restore, error) {
	q := s.client.NocoDBRestore.Query()
	if f.UserID != "" {
		q = q.Where(nocodbrestore.UserID(f.UserID))
	}
	if f.SnapshotID != "" {
		q = q.Where(nocodbrestore.SnapshotID(f.SnapshotID))
	}
	if f.ConnectionID != "" {
		q = q.Where(nocodbrestore.Or(
			nocodbrestore.SourceConnectionID(f.ConnectionID),
			nocodbrestore.TargetConnectionID(f.ConnectionID),
		))
	}
	if f.ActiveOnly {
		q = q.Where(nocodbrestore.StatusIn(string(repo.RestorePending), string(repo.RestoreRunning)))
	}
	q = q.Order(ent.Desc(nocodbrestore.FieldCreatedAt))
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list restores: %w", err)
	}
	out := make([]*repo.Restore, 0, len(rows))
	for _, row := range rows {
		out = append(out, restoreFromRow(row))
	}
	return out, nil
}

func (s *Store) DeleteRestoresForConnection(ctx context.Context, connectionID string) error {
	_, err := s.client.NocoDBRestore.Delete().
		Where(nocodbrestore.Or(
			nocodbrestore.SourceConnectionID(connectionID),
			nocodbrestore.TargetConnectionID(connectionID),
		)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete restores: %w", err)
	}
	return nil
}

func (s *Store) FailUnfinishedRestores(ctx context.Context, reason string, at time.Time) (int64, error) {
	n, err := s.client.NocoDBRestore.Update().
		Where(nocodbrestore.StatusIn(string(repo.RestorePending), string(repo.RestoreRunning))).
		SetStatus(string(repo.RestoreFailed)).
		SetError(reason).
		SetFinishedAt(at).
		SetProgress("").
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("fail unfinished restores: %w", err)
	}
	return int64(n), nil
}

// --- conversions ---

// --- files ---

func (s *Store) FileExists(ctx context.Context, userID, sha256 string) (bool, error) {
	ok, err := s.client.NocoDBFile.Query().
		Where(nocodbfile.UserID(userID), nocodbfile.Sha256(sha256)).
		Exist(ctx)
	if err != nil {
		return false, fmt.Errorf("find file: %w", err)
	}
	return ok, nil
}

func (s *Store) CreateFile(ctx context.Context, f *repo.File) error {
	err := s.client.NocoDBFile.Create().
		SetUserID(f.UserID).
		SetSha256(f.SHA256).
		SetSize(f.Size).
		SetCreatedAt(f.CreatedAt).
		OnConflictColumns(nocodbfile.FieldUserID, nocodbfile.FieldSha256).
		DoNothing().
		Exec(ctx)
	// DoNothing on a conflict leaves no row to return.
	if err != nil && !errors.Is(err, stdsql.ErrNoRows) {
		return fmt.Errorf("insert file: %w", err)
	}
	return nil
}

func (s *Store) AddSnapshotFile(ctx context.Context, snapshotID, userID, sha256 string) error {
	err := s.client.NocoDBSnapshotFile.Create().
		SetSnapshotID(snapshotID).
		SetUserID(userID).
		SetSha256(sha256).
		OnConflictColumns(nocodbsnapshotfile.FieldSnapshotID, nocodbsnapshotfile.FieldSha256).
		DoNothing().
		Exec(ctx)
	if err != nil && !errors.Is(err, stdsql.ErrNoRows) {
		return fmt.Errorf("insert snapshot file: %w", err)
	}
	return nil
}

func (s *Store) DeleteSnapshotFiles(ctx context.Context, snapshotID string) error {
	if _, err := s.client.NocoDBSnapshotFile.Delete().Where(nocodbsnapshotfile.SnapshotID(snapshotID)).Exec(ctx); err != nil {
		return fmt.Errorf("delete snapshot files: %w", err)
	}
	return nil
}

func (s *Store) ListUnusedFiles(ctx context.Context, userID string) ([]string, error) {
	unused := func(sel *sql.Selector) {
		refs := sql.Table(nocodbsnapshotfile.Table)
		sel.Where(sql.Not(sql.Exists(
			sql.Select(refs.C(nocodbsnapshotfile.FieldID)).From(refs).Where(sql.And(
				sql.ColumnsEQ(refs.C(nocodbsnapshotfile.FieldUserID), sel.C(nocodbfile.FieldUserID)),
				sql.ColumnsEQ(refs.C(nocodbsnapshotfile.FieldSha256), sel.C(nocodbfile.FieldSha256)),
			)),
		)))
	}
	shas, err := s.client.NocoDBFile.Query().
		Where(nocodbfile.UserID(userID), unused).
		Select(nocodbfile.FieldSha256).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unused files: %w", err)
	}
	return shas, nil
}

func (s *Store) DeleteFile(ctx context.Context, userID, sha256 string) error {
	if _, err := s.client.NocoDBFile.Delete().Where(nocodbfile.UserID(userID), nocodbfile.Sha256(sha256)).Exec(ctx); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

func (s *Store) FileSource(ctx context.Context, connectionID, source string, size int64) (string, bool, error) {
	row, err := s.client.NocoDBFileSource.Query().
		Where(nocodbfilesource.ConnectionID(connectionID), nocodbfilesource.Source(source), nocodbfilesource.Size(size)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("find file source: %w", err)
	}
	return row.Sha256, true, nil
}

func (s *Store) PutFileSource(ctx context.Context, connectionID, source string, size int64, sha256 string) error {
	err := s.client.NocoDBFileSource.Create().
		SetConnectionID(connectionID).
		SetSource(source).
		SetSize(size).
		SetSha256(sha256).
		OnConflictColumns(nocodbfilesource.FieldConnectionID, nocodbfilesource.FieldSource, nocodbfilesource.FieldSize).
		UpdateSha256().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("upsert file source: %w", err)
	}
	return nil
}

func (s *Store) DeleteFileSourcesForConnection(ctx context.Context, connectionID string) error {
	if _, err := s.client.NocoDBFileSource.Delete().Where(nocodbfilesource.ConnectionID(connectionID)).Exec(ctx); err != nil {
		return fmt.Errorf("delete file sources: %w", err)
	}
	return nil
}

func policyFromRow(r *ent.NocoDBBackupPolicy) *repo.Policy {
	return &repo.Policy{
		ConnectionID: r.ConnectionID, BaseID: r.BaseID, UserID: r.UserID, Enabled: r.Enabled,
		Cron: r.Cron, Retention: r.Retention, IncludeAttachments: r.IncludeAttachments, UpdatedAt: r.UpdatedAt,
	}
}

func snapshotFromRow(r *ent.NocoDBSnapshot) *repo.Snapshot {
	return &repo.Snapshot{
		ID: r.ID, UserID: r.UserID, ConnectionID: r.ConnectionID, BaseID: r.BaseID, BaseTitle: r.BaseTitle,
		Status: repo.SnapshotStatus(r.Status), Trigger: repo.SnapshotTrigger(r.Trigger), Error: r.Error,
		Progress: r.Progress, ObjectKey: r.ObjectKey, SizeBytes: r.SizeBytes, RecordCount: r.RecordCount,
		LinkCount: r.LinkCount, ViewCount: r.ViewCount, HookCount: r.HookCount, AttachmentsIncluded: r.AttachmentsIncluded, FileCount: r.FileCount,
		FileBytes: r.FileBytes, FilesMissing: r.FilesMissing, Tables: r.Tables, CreatedAt: r.CreatedAt,
		StartedAt: zeroIfNil(r.StartedAt), FinishedAt: zeroIfNil(r.FinishedAt),
	}
}

func restoreFromRow(r *ent.NocoDBRestore) *repo.Restore {
	return &repo.Restore{
		ID: r.ID, UserID: r.UserID, SnapshotID: r.SnapshotID,
		SourceConnectionID: r.SourceConnectionID, SourceBaseID: r.SourceBaseID, SourceBaseTitle: r.SourceBaseTitle,
		TargetConnectionID: r.TargetConnectionID, TargetBaseID: r.TargetBaseID, TargetBaseTitle: r.TargetBaseTitle,
		Status: repo.RestoreStatus(r.Status), Error: r.Error, Progress: r.Progress,
		TableCount: r.TableCount, RecordCount: r.RecordCount, LinkCount: r.LinkCount, FileCount: r.FileCount,
		ViewCount: r.ViewCount, HookCount: r.HookCount,
		Warnings:  r.Warnings,
		CreatedAt: r.CreatedAt, StartedAt: zeroIfNil(r.StartedAt), FinishedAt: zeroIfNil(r.FinishedAt),
	}
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func zeroIfNil(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
