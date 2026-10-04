package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	repo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

func day(s string) time.Time {
	t, err := time.Parse(wasabi.DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func usage(d, bucket string, padded int64) wasabi.Usage {
	u := wasabi.Usage{Day: day(d), Bucket: bucket, PaddedStorageSizeBytes: padded, NumGETCalls: 3, DownloadBytes: 9}
	if bucket != "" {
		u.Region = "eu-central-1"
	}
	return u
}

func TestUpsertAndListUsage(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	rows := []wasabi.Usage{
		usage("2026-09-01", "", 100), usage("2026-09-02", "", 200), usage("2026-09-03", "", 300),
		usage("2026-09-01", "logs", 10), usage("2026-09-02", "logs", 20),
	}
	if err := s.UpsertUsage(ctx, "c1", rows); err != nil {
		t.Fatalf("UpsertUsage: %v", err)
	}
	// Re-syncing a day replaces it instead of adding a row.
	if err := s.UpsertUsage(ctx, "c1", []wasabi.Usage{usage("2026-09-02", "", 250)}); err != nil {
		t.Fatalf("UpsertUsage again: %v", err)
	}
	// Another connection's rows stay separate.
	if err := s.UpsertUsage(ctx, "c2", []wasabi.Usage{usage("2026-09-02", "", 999)}); err != nil {
		t.Fatal(err)
	}

	account, err := s.ListUsage(ctx, "c1", "", day("2026-09-02"), day("2026-09-30"))
	if err != nil {
		t.Fatalf("ListUsage: %v", err)
	}
	if len(account) != 2 || account[0].PaddedStorageSizeBytes != 250 || account[1].PaddedStorageSizeBytes != 300 {
		t.Fatalf("account usage = %+v", account)
	}
	// date columns come back as the UTC day at midnight.
	if !account[0].Day.Equal(day("2026-09-02")) || account[0].Day.Location() != time.UTC {
		t.Fatalf("day = %v (%v)", account[0].Day, account[0].Day.Location())
	}
	if account[0].NumGETCalls != 3 || account[0].DownloadBytes != 9 {
		t.Fatalf("counters not stored: %+v", account[0])
	}

	logs, _ := s.ListUsage(ctx, "c1", "logs", day("2026-09-01"), day("2026-09-30"))
	if len(logs) != 2 || logs[0].Region != "eu-central-1" {
		t.Fatalf("bucket usage = %+v", logs)
	}
	if err := s.UpsertUsage(ctx, "c1", []wasabi.Usage{usage("2026-09-02", "archive", 5)}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListBucketUsage(ctx, "c1", day("2026-09-02"), day("2026-09-30"))
	if err != nil || len(all) != 2 || all[0].Bucket != "archive" || all[1].Bucket != "logs" || !all[1].Day.Equal(day("2026-09-02")) {
		t.Fatalf("ListBucketUsage = %+v, %v", all, err)
	}

	latest, err := s.LatestAccountUsage(ctx, "c1")
	if err != nil || !latest.Day.Equal(day("2026-09-03")) {
		t.Fatalf("LatestAccountUsage = %+v, %v", latest, err)
	}
	if _, err := s.LatestAccountUsage(ctx, "nope"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("LatestAccountUsage(unknown) = %v, want ErrNotFound", err)
	}
}

func TestLatestBucketUsage(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	rows := []wasabi.Usage{
		usage("2026-09-01", "", 1), usage("2026-09-03", "", 1),
		usage("2026-09-01", "media", 10), usage("2026-09-03", "media", 30),
		// "old" disappeared after 09-01.
		usage("2026-08-31", "old", 5), usage("2026-09-01", "old", 6),
	}
	if err := s.UpsertUsage(ctx, "c1", rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestBucketUsage(ctx, "c1")
	if err != nil {
		t.Fatalf("LatestBucketUsage: %v", err)
	}
	if len(got) != 2 || got[0].Bucket != "media" || got[0].PaddedStorageSizeBytes != 30 ||
		got[1].Bucket != "old" || !got[1].Day.Equal(day("2026-09-01")) {
		t.Fatalf("latest bucket usage = %+v", got)
	}
}

func TestSyncStateAndDelete(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	if _, err := s.GetSyncState(ctx, "c1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetSyncState(new) = %v, want ErrNotFound", err)
	}
	st := &repo.SyncState{ConnectionID: "c1", BackfillFrom: day("2025-09-01"), BackfillThrough: day("2025-10-01"), UpdatedAt: now}
	if err := s.SaveSyncState(ctx, st); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}
	st.BackfillThrough, st.BackfillCompletedAt, st.LastSyncedDay, st.LastSuccessAt = day("2026-09-30"), now, day("2026-09-29"), now
	if err := s.SaveSyncState(ctx, st); err != nil {
		t.Fatalf("SaveSyncState again: %v", err)
	}
	got, err := s.GetSyncState(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.BackfillFrom.Equal(day("2025-09-01")) || !got.BackfillThrough.Equal(day("2026-09-30")) ||
		!got.LastSyncedDay.Equal(day("2026-09-29")) || !got.BackfillCompletedAt.Equal(now) || !got.LastSuccessAt.Equal(now) {
		t.Fatalf("sync state = %+v", got)
	}

	_ = s.UpsertUsage(ctx, "c1", []wasabi.Usage{usage("2026-09-01", "", 1)})
	_ = s.UpsertUsage(ctx, "c2", []wasabi.Usage{usage("2026-09-01", "", 1)})
	if err := s.DeleteConnectionData(ctx, "c1"); err != nil {
		t.Fatalf("DeleteConnectionData: %v", err)
	}
	if _, err := s.GetSyncState(ctx, "c1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("sync state left after delete: %v", err)
	}
	if left, _ := s.ListUsage(ctx, "c1", "", day("2026-01-01"), day("2026-12-31")); len(left) != 0 {
		t.Fatalf("usage left after delete: %d", len(left))
	}
	if other, _ := s.ListUsage(ctx, "c2", "", day("2026-01-01"), day("2026-12-31")); len(other) != 1 {
		t.Fatalf("another connection's usage was deleted")
	}
}

func TestBucketConfigs(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	media := wasabi.BucketConfig{
		Bucket: "media", Region: "eu-central-1", FetchedAt: at,
		Versioning: &wasabi.Versioning{Status: "Enabled"},
		Lifecycle:  []wasabi.LifecycleRule{{ID: "r1", Enabled: true, NoncurrentDays: 7}},
		Errors:     map[string]string{wasabi.SettingACL: wasabi.ErrAccessDenied},
	}
	old := wasabi.BucketConfig{Bucket: "old", FetchedAt: at}
	if err := s.ReplaceBucketConfigs(ctx, "c1", []wasabi.BucketConfig{media, old}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceBucketConfigs(ctx, "c2", []wasabi.BucketConfig{{Bucket: "other", FetchedAt: at}}); err != nil {
		t.Fatal(err)
	}
	// The next read no longer lists "old".
	media.Versioning.Status = "Suspended"
	if err := s.ReplaceBucketConfigs(ctx, "c1", []wasabi.BucketConfig{media}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListBucketConfigs(ctx, "c1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListBucketConfigs = %+v, %v", got, err)
	}
	c := got[0]
	if c.Bucket != "media" || c.Region != "eu-central-1" || c.Versioning.Status != "Suspended" || len(c.Lifecycle) != 1 ||
		c.Errors[wasabi.SettingACL] != wasabi.ErrAccessDenied || !c.FetchedAt.Equal(at) {
		t.Fatalf("config = %+v", c)
	}
	if other, _ := s.ListBucketConfigs(ctx, "c2"); len(other) != 1 {
		t.Fatalf("c2 configs = %+v", other)
	}

	st := &repo.SyncState{ConnectionID: "c1", ConfigFetchedAt: at, ConfigError: "access_denied", UpdatedAt: at}
	if err := s.SaveSyncState(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSyncState(ctx, "c1"); !got.ConfigFetchedAt.Equal(at) || got.ConfigError != "access_denied" {
		t.Fatalf("sync state = %+v", got)
	}

	if err := s.DeleteConnectionData(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ListBucketConfigs(ctx, "c1"); len(left) != 0 {
		t.Fatalf("configs left = %+v", left)
	}
}
