package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/predicate"
	"go.orx.me/apps/neo-box/internal/ent/wasabibucketconfig"
	"go.orx.me/apps/neo-box/internal/ent/wasabidailyusage"
	"go.orx.me/apps/neo-box/internal/ent/wasabisyncstate"
	repo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// upsertBatch keeps each INSERT well under PostgreSQL's parameter limit
// (23 columns per row).
const upsertBatch = 500

type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) UpsertUsage(ctx context.Context, connectionID string, rows []wasabi.Usage) error {
	for start := 0; start < len(rows); start += upsertBatch {
		batch := rows[start:min(start+upsertBatch, len(rows))]
		builders := make([]*ent.WasabiDailyUsageCreate, 0, len(batch))
		for _, u := range batch {
			builders = append(builders, s.client.WasabiDailyUsage.Create().
				SetConnectionID(connectionID).
				SetBucket(u.Bucket).
				SetDay(wasabi.DayOf(u.Day)).
				SetRegion(u.Region).
				SetNumBillableObjects(u.NumBillableObjects).
				SetNumBillableDeletedObjects(u.NumBillableDeletedObjects).
				SetRawStorageSizeBytes(u.RawStorageSizeBytes).
				SetPaddedStorageSizeBytes(u.PaddedStorageSizeBytes).
				SetMetadataStorageSizeBytes(u.MetadataStorageSizeBytes).
				SetDeletedStorageSizeBytes(u.DeletedStorageSizeBytes).
				SetOrphanedStorageSizeBytes(u.OrphanedStorageSizeBytes).
				SetMinStorageChargeBytes(u.MinStorageChargeBytes).
				SetNumAPICalls(u.NumAPICalls).
				SetUploadBytes(u.UploadBytes).
				SetDownloadBytes(u.DownloadBytes).
				SetStorageWroteBytes(u.StorageWroteBytes).
				SetStorageReadBytes(u.StorageReadBytes).
				SetDeleteBytes(u.DeleteBytes).
				SetNumGetCalls(u.NumGETCalls).
				SetNumPutCalls(u.NumPUTCalls).
				SetNumDeleteCalls(u.NumDELETECalls).
				SetNumListCalls(u.NumLISTCalls).
				SetNumHeadCalls(u.NumHEADCalls))
		}
		err := s.client.WasabiDailyUsage.CreateBulk(builders...).
			OnConflictColumns(wasabidailyusage.FieldConnectionID, wasabidailyusage.FieldBucket, wasabidailyusage.FieldDay).
			UpdateNewValues().
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("upsert wasabi usage: %w", err)
		}
	}
	return nil
}

func (s *Store) ListUsage(ctx context.Context, connectionID, bucket string, from, to time.Time) ([]wasabi.Usage, error) {
	rows, err := s.client.WasabiDailyUsage.Query().
		Where(
			wasabidailyusage.ConnectionID(connectionID),
			wasabidailyusage.Bucket(bucket),
			wasabidailyusage.DayGTE(wasabi.DayOf(from)),
			wasabidailyusage.DayLTE(wasabi.DayOf(to)),
		).
		Order(ent.Asc(wasabidailyusage.FieldDay)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list wasabi usage: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) ListBucketUsage(ctx context.Context, connectionID string, from, to time.Time) ([]wasabi.Usage, error) {
	rows, err := s.client.WasabiDailyUsage.Query().
		Where(
			wasabidailyusage.ConnectionID(connectionID),
			wasabidailyusage.BucketNEQ(""),
			wasabidailyusage.DayGTE(wasabi.DayOf(from)),
			wasabidailyusage.DayLTE(wasabi.DayOf(to)),
		).
		Order(ent.Asc(wasabidailyusage.FieldDay), ent.Asc(wasabidailyusage.FieldBucket)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list wasabi bucket usage: %w", err)
	}
	return fromRows(rows), nil
}

func (s *Store) LatestBucketUsage(ctx context.Context, connectionID string) ([]wasabi.Usage, error) {
	var latest []struct {
		Bucket string    `json:"bucket"`
		Max    time.Time `json:"max"`
	}
	err := s.client.WasabiDailyUsage.Query().
		Where(wasabidailyusage.ConnectionID(connectionID), wasabidailyusage.BucketNEQ("")).
		GroupBy(wasabidailyusage.FieldBucket).
		Aggregate(ent.Max(wasabidailyusage.FieldDay)).
		Scan(ctx, &latest)
	if err != nil {
		return nil, fmt.Errorf("find latest wasabi bucket days: %w", err)
	}
	var out []wasabi.Usage
	const chunk = 200
	for start := 0; start < len(latest); start += chunk {
		pairs := make([]predicate.WasabiDailyUsage, 0, chunk)
		for _, l := range latest[start:min(start+chunk, len(latest))] {
			pairs = append(pairs, wasabidailyusage.And(wasabidailyusage.Bucket(l.Bucket), wasabidailyusage.Day(l.Max)))
		}
		rows, err := s.client.WasabiDailyUsage.Query().
			Where(wasabidailyusage.ConnectionID(connectionID), wasabidailyusage.Or(pairs...)).
			Order(ent.Asc(wasabidailyusage.FieldBucket)).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("list latest wasabi bucket usage: %w", err)
		}
		out = append(out, fromRows(rows)...)
	}
	return out, nil
}

func (s *Store) LatestAccountUsage(ctx context.Context, connectionID string) (*wasabi.Usage, error) {
	row, err := s.client.WasabiDailyUsage.Query().
		Where(wasabidailyusage.ConnectionID(connectionID), wasabidailyusage.Bucket("")).
		Order(ent.Desc(wasabidailyusage.FieldDay)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find latest wasabi account usage: %w", err)
	}
	u := fromRow(row)
	return &u, nil
}

func (s *Store) GetSyncState(ctx context.Context, connectionID string) (*repo.SyncState, error) {
	row, err := s.client.WasabiSyncState.Get(ctx, connectionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("get wasabi sync state: %w", err)
	}
	return &repo.SyncState{
		ConnectionID:        row.ID,
		LastSyncedDay:       deref(row.LastSyncedDay),
		LastSuccessAt:       deref(row.LastSuccessAt),
		BackfillFrom:        deref(row.BackfillFrom),
		BackfillThrough:     deref(row.BackfillThrough),
		BackfillCompletedAt: deref(row.BackfillCompletedAt),
		ConfigFetchedAt:     deref(row.ConfigFetchedAt),
		ConfigError:         row.ConfigError,
		UpdatedAt:           row.UpdatedAt,
	}, nil
}

func (s *Store) SaveSyncState(ctx context.Context, st *repo.SyncState) error {
	err := s.client.WasabiSyncState.Create().
		SetID(st.ConnectionID).
		SetNillableLastSyncedDay(nilIfZero(st.LastSyncedDay)).
		SetNillableLastSuccessAt(nilIfZero(st.LastSuccessAt)).
		SetNillableBackfillFrom(nilIfZero(st.BackfillFrom)).
		SetNillableBackfillThrough(nilIfZero(st.BackfillThrough)).
		SetNillableBackfillCompletedAt(nilIfZero(st.BackfillCompletedAt)).
		SetNillableConfigFetchedAt(nilIfZero(st.ConfigFetchedAt)).
		SetConfigError(st.ConfigError).
		SetUpdatedAt(st.UpdatedAt).
		OnConflictColumns(wasabisyncstate.FieldID).
		UpdateNewValues().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save wasabi sync state: %w", err)
	}
	return nil
}

func (s *Store) ReplaceBucketConfigs(ctx context.Context, connectionID string, configs []wasabi.BucketConfig) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	names := make([]string, 0, len(configs))
	for _, c := range configs {
		raw, err := json.Marshal(c)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("encode bucket config %s: %w", c.Bucket, err)
		}
		err = tx.WasabiBucketConfig.Create().
			SetConnectionID(connectionID).
			SetBucket(c.Bucket).
			SetConfig(string(raw)).
			SetFetchedAt(c.FetchedAt).
			OnConflictColumns(wasabibucketconfig.FieldConnectionID, wasabibucketconfig.FieldBucket).
			UpdateNewValues().
			Exec(ctx)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upsert bucket config %s: %w", c.Bucket, err)
		}
		names = append(names, c.Bucket)
	}
	_, err = tx.WasabiBucketConfig.Delete().
		Where(wasabibucketconfig.ConnectionID(connectionID), wasabibucketconfig.BucketNotIn(names...)).
		Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("drop old bucket configs: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ListBucketConfigs(ctx context.Context, connectionID string) ([]wasabi.BucketConfig, error) {
	rows, err := s.client.WasabiBucketConfig.Query().
		Where(wasabibucketconfig.ConnectionID(connectionID)).
		Order(ent.Asc(wasabibucketconfig.FieldBucket)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list bucket configs: %w", err)
	}
	out := make([]wasabi.BucketConfig, 0, len(rows))
	for _, r := range rows {
		var c wasabi.BucketConfig
		if err := json.Unmarshal([]byte(r.Config), &c); err != nil {
			return nil, fmt.Errorf("decode bucket config %s: %w", r.Bucket, err)
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) DeleteConnectionData(ctx context.Context, connectionID string) error {
	if _, err := s.client.WasabiDailyUsage.Delete().Where(wasabidailyusage.ConnectionID(connectionID)).Exec(ctx); err != nil {
		return fmt.Errorf("delete wasabi usage: %w", err)
	}
	if _, err := s.client.WasabiBucketConfig.Delete().Where(wasabibucketconfig.ConnectionID(connectionID)).Exec(ctx); err != nil {
		return fmt.Errorf("delete wasabi bucket configs: %w", err)
	}
	if _, err := s.client.WasabiSyncState.Delete().Where(wasabisyncstate.ID(connectionID)).Exec(ctx); err != nil {
		return fmt.Errorf("delete wasabi sync state: %w", err)
	}
	return nil
}

func fromRows(rows []*ent.WasabiDailyUsage) []wasabi.Usage {
	out := make([]wasabi.Usage, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out
}

func fromRow(r *ent.WasabiDailyUsage) wasabi.Usage {
	return wasabi.Usage{
		Day: wasabi.DayOf(r.Day), Bucket: r.Bucket, Region: r.Region,
		NumBillableObjects: r.NumBillableObjects, NumBillableDeletedObjects: r.NumBillableDeletedObjects,
		RawStorageSizeBytes: r.RawStorageSizeBytes, PaddedStorageSizeBytes: r.PaddedStorageSizeBytes,
		MetadataStorageSizeBytes: r.MetadataStorageSizeBytes, DeletedStorageSizeBytes: r.DeletedStorageSizeBytes,
		OrphanedStorageSizeBytes: r.OrphanedStorageSizeBytes, MinStorageChargeBytes: r.MinStorageChargeBytes,
		NumAPICalls: r.NumAPICalls, UploadBytes: r.UploadBytes, DownloadBytes: r.DownloadBytes,
		StorageWroteBytes: r.StorageWroteBytes, StorageReadBytes: r.StorageReadBytes, DeleteBytes: r.DeleteBytes,
		NumGETCalls: r.NumGetCalls, NumPUTCalls: r.NumPutCalls, NumDELETECalls: r.NumDeleteCalls,
		NumLISTCalls: r.NumListCalls, NumHEADCalls: r.NumHeadCalls,
	}
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
