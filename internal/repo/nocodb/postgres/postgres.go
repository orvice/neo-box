package postgres

import (
	"context"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/nocodbbackuppolicy"
	"go.orx.me/apps/neo-box/internal/ent/nocodbsnapshot"
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
		SetLinkCount(snap.LinkCount)
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
	if _, err := s.client.NocoDBSnapshot.Delete().Where(nocodbsnapshot.ID(id)).Exec(ctx); err != nil {
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

// --- conversions ---

func policyFromRow(r *ent.NocoDBBackupPolicy) *repo.Policy {
	return &repo.Policy{
		ConnectionID: r.ConnectionID, BaseID: r.BaseID, UserID: r.UserID, Enabled: r.Enabled,
		Cron: r.Cron, Retention: r.Retention, UpdatedAt: r.UpdatedAt,
	}
}

func snapshotFromRow(r *ent.NocoDBSnapshot) *repo.Snapshot {
	return &repo.Snapshot{
		ID: r.ID, UserID: r.UserID, ConnectionID: r.ConnectionID, BaseID: r.BaseID, BaseTitle: r.BaseTitle,
		Status: repo.SnapshotStatus(r.Status), Trigger: repo.SnapshotTrigger(r.Trigger), Error: r.Error,
		Progress: r.Progress, ObjectKey: r.ObjectKey, SizeBytes: r.SizeBytes, RecordCount: r.RecordCount,
		LinkCount: r.LinkCount, Tables: r.Tables, CreatedAt: r.CreatedAt,
		StartedAt: zeroIfNil(r.StartedAt), FinishedAt: zeroIfNil(r.FinishedAt),
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
