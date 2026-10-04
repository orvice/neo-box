package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func TestUpsertPolicy(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	p := &repo.Policy{ConnectionID: "c1", BaseID: "p1", UserID: "u1", Enabled: true, Cron: "0 * * * *", Retention: 3, UpdatedAt: t0}
	if err := s.UpsertPolicy(ctx, p); err != nil {
		t.Fatalf("UpsertPolicy insert: %v", err)
	}
	p.Retention, p.Cron, p.UpdatedAt = 7, "0 0 * * *", t0.Add(time.Minute)
	if err := s.UpsertPolicy(ctx, p); err != nil {
		t.Fatalf("UpsertPolicy update: %v", err)
	}
	if err := s.UpsertPolicy(ctx, &repo.Policy{ConnectionID: "c1", BaseID: "p2", UserID: "u1", Enabled: false, UpdatedAt: t0}); err != nil {
		t.Fatalf("UpsertPolicy second base: %v", err)
	}

	list, err := s.ListPolicies(ctx, "u1", "c1")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListPolicies = %+v, %v", list, err)
	}
	enabled, err := s.ListEnabledPolicies(ctx)
	if err != nil || len(enabled) != 1 {
		t.Fatalf("ListEnabledPolicies = %+v, %v", enabled, err)
	}
	if got := enabled[0]; got.BaseID != "p1" || got.Retention != 7 || got.Cron != "0 0 * * *" || !got.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("upsert did not update in place: %+v", got)
	}

	if err := s.DeletePoliciesForConnection(ctx, "c1"); err != nil {
		t.Fatalf("DeletePoliciesForConnection: %v", err)
	}
	if list, _ = s.ListPolicies(ctx, "u1", "c1"); len(list) != 0 {
		t.Fatalf("policies left after delete: %+v", list)
	}
}

func newSnapshot(id, baseID string, trigger repo.SnapshotTrigger, status repo.SnapshotStatus, at time.Time) *repo.Snapshot {
	return &repo.Snapshot{
		ID: id, UserID: "u1", ConnectionID: "c1", BaseID: baseID, BaseTitle: "Base " + baseID,
		Status: status, Trigger: trigger, CreatedAt: at,
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	snap := newSnapshot("s1", "p1", repo.TriggerManual, repo.StatusPending, t0)
	if err := s.CreateSnapshot(ctx, snap); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	got, err := s.GetSnapshot(ctx, "u1", "s1")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if !got.StartedAt.IsZero() || !got.FinishedAt.IsZero() || got.Tables != nil {
		t.Fatalf("unset fields should round-trip as zero: %+v", got)
	}
	if _, err := s.GetSnapshot(ctx, "u2", "s1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetSnapshot(other user) = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSnapshot(ctx, "", "s1"); err != nil {
		t.Fatalf("GetSnapshot(unscoped): %v", err)
	}

	if err := s.UpdateSnapshotProgress(ctx, "s1", "table 1/2"); err != nil {
		t.Fatalf("UpdateSnapshotProgress: %v", err)
	}
	if got, _ = s.GetSnapshot(ctx, "", "s1"); got.Progress != "table 1/2" {
		t.Fatalf("progress = %q", got.Progress)
	}

	snap.Status = repo.StatusSucceeded
	snap.StartedAt, snap.FinishedAt = t0.Add(time.Second), t0.Add(time.Minute)
	snap.ObjectKey, snap.SizeBytes, snap.RecordCount, snap.LinkCount = "k/s1.json.gz", 1024, 10, 2
	snap.Tables = []repo.SnapshotTable{{ID: "m1", Title: "Tasks", RecordCount: 10, FieldCount: 4, LinkCount: 2}}
	if err := s.UpdateSnapshot(ctx, snap); err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	got, _ = s.GetSnapshot(ctx, "", "s1")
	if got.Status != repo.StatusSucceeded || got.Trigger != repo.TriggerManual || got.ObjectKey != "k/s1.json.gz" ||
		got.SizeBytes != 1024 || !got.StartedAt.Equal(snap.StartedAt) || !got.FinishedAt.Equal(snap.FinishedAt) ||
		len(got.Tables) != 1 || got.Tables[0] != snap.Tables[0] {
		t.Fatalf("UpdateSnapshot did not persist: %+v", got)
	}

	if err := s.UpdateSnapshot(ctx, newSnapshot("missing", "p1", repo.TriggerManual, repo.StatusFailed, t0)); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("UpdateSnapshot(missing) = %v, want ErrNotFound", err)
	}
	if err := s.DeleteSnapshot(ctx, "s1"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, err := s.GetSnapshot(ctx, "", "s1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetSnapshot after delete = %v, want ErrNotFound", err)
	}
}

func TestListSnapshotsAndFailUnfinished(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	for i, snap := range []*repo.Snapshot{
		newSnapshot("s1", "p1", repo.TriggerScheduled, repo.StatusSucceeded, t0),
		newSnapshot("s2", "p1", repo.TriggerManual, repo.StatusSucceeded, t0.Add(1*time.Second)),
		newSnapshot("s3", "p1", repo.TriggerScheduled, repo.StatusRunning, t0.Add(2*time.Second)),
		newSnapshot("s4", "p2", repo.TriggerScheduled, repo.StatusPending, t0.Add(3*time.Second)),
	} {
		if err := s.CreateSnapshot(ctx, snap); err != nil {
			t.Fatalf("CreateSnapshot #%d: %v", i, err)
		}
	}

	ids := func(f repo.SnapshotFilter) []string {
		t.Helper()
		snaps, err := s.ListSnapshots(ctx, f)
		if err != nil {
			t.Fatalf("ListSnapshots(%+v): %v", f, err)
		}
		out := make([]string, 0, len(snaps))
		for _, sn := range snaps {
			out = append(out, sn.ID)
		}
		return out
	}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if got := ids(repo.SnapshotFilter{UserID: "u1"}); !eq(got, "s4", "s3", "s2", "s1") {
		t.Fatalf("newest first: %v", got)
	}
	if got := ids(repo.SnapshotFilter{BaseID: "p1", Trigger: repo.TriggerScheduled}); !eq(got, "s3", "s1") {
		t.Fatalf("base+trigger: %v", got)
	}
	if got := ids(repo.SnapshotFilter{ConnectionID: "c1", BaseID: "p1", Limit: 1}); !eq(got, "s3") {
		t.Fatalf("limit: %v", got)
	}
	if got := ids(repo.SnapshotFilter{Status: repo.StatusSucceeded}); !eq(got, "s2", "s1") {
		t.Fatalf("status: %v", got)
	}

	at := t0.Add(time.Hour)
	n, err := s.FailUnfinishedSnapshots(ctx, "restarted", at)
	if err != nil || n != 2 {
		t.Fatalf("FailUnfinishedSnapshots = %d, %v; want 2", n, err)
	}
	got, _ := s.GetSnapshot(ctx, "", "s3")
	if got.Status != repo.StatusFailed || got.Error != "restarted" || !got.FinishedAt.Equal(at) {
		t.Fatalf("s3 not failed: %+v", got)
	}
	if got := ids(repo.SnapshotFilter{Status: repo.StatusFailed}); !eq(got, "s4", "s3") {
		t.Fatalf("failed: %v", got)
	}
}

func newRestore(id, snapshotID, source, target string, status repo.RestoreStatus, at time.Time) *repo.Restore {
	return &repo.Restore{
		ID: id, UserID: "u1", SnapshotID: snapshotID,
		SourceConnectionID: source, SourceBaseID: "p1", SourceBaseTitle: "CRM",
		TargetConnectionID: target, TargetBaseTitle: "CRM (restored)",
		Status: status, CreatedAt: at,
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	r := newRestore("r1", "s1", "c1", "c2", repo.RestorePending, t0)
	if err := s.CreateRestore(ctx, r); err != nil {
		t.Fatalf("CreateRestore: %v", err)
	}
	if _, err := s.GetRestore(ctx, "u2", "r1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetRestore other user = %v, want ErrNotFound", err)
	}

	r.Status, r.StartedAt = repo.RestoreRunning, t0.Add(time.Second)
	if err := s.UpdateRestore(ctx, r); err != nil {
		t.Fatalf("UpdateRestore running: %v", err)
	}
	if err := s.UpdateRestoreProgress(ctx, "r1", "[1/2] Orders: 10/30 records"); err != nil {
		t.Fatalf("UpdateRestoreProgress: %v", err)
	}
	if err := s.UpdateRestoreTargetBase(ctx, "r1", "pNew"); err != nil {
		t.Fatalf("UpdateRestoreTargetBase: %v", err)
	}
	r.TargetBaseID = "pNew"
	got, err := s.GetRestore(ctx, "u1", "r1")
	if err != nil || got.Progress != "[1/2] Orders: 10/30 records" || got.TargetBaseID != "pNew" || !got.Active() {
		t.Fatalf("GetRestore = %+v, %v", got, err)
	}

	r.Status, r.Progress, r.FinishedAt = repo.RestoreSucceeded, "", t0.Add(time.Minute)
	r.TableCount, r.RecordCount, r.LinkCount = 2, 30, 12
	r.Warnings = []repo.RestoreWarning{{Code: "attachments_skipped", Table: "Orders", Field: "Files", Count: 3, Message: "attachments are not restored"}}
	if err := s.UpdateRestore(ctx, r); err != nil {
		t.Fatalf("UpdateRestore done: %v", err)
	}
	got, err = s.GetRestore(ctx, "", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != repo.RestoreSucceeded || got.Active() || got.TableCount != 2 || got.RecordCount != 30 || got.LinkCount != 12 ||
		len(got.Warnings) != 1 || got.Warnings[0] != r.Warnings[0] ||
		got.SourceBaseTitle != "CRM" || got.TargetBaseTitle != "CRM (restored)" ||
		!got.StartedAt.Equal(r.StartedAt) || !got.FinishedAt.Equal(r.FinishedAt) {
		t.Fatalf("restore after update = %+v", got)
	}
	if err := s.UpdateRestore(ctx, &repo.Restore{ID: "nope", Status: repo.RestoreFailed}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("UpdateRestore missing = %v, want ErrNotFound", err)
	}
}

func TestListRestoresAndFailUnfinished(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	for _, r := range []*repo.Restore{
		newRestore("r1", "s1", "c1", "c1", repo.RestoreSucceeded, t0),
		newRestore("r2", "s1", "c1", "c2", repo.RestoreRunning, t0.Add(time.Minute)),
		newRestore("r3", "s2", "c3", "c3", repo.RestorePending, t0.Add(2*time.Minute)),
	} {
		if err := s.CreateRestore(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(f repo.RestoreFilter) []string {
		t.Helper()
		list, err := s.ListRestores(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range list {
			out = append(out, r.ID)
		}
		return out
	}
	check := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	check("by snapshot", ids(repo.RestoreFilter{UserID: "u1", SnapshotID: "s1"}), "r2", "r1")
	check("by target connection", ids(repo.RestoreFilter{UserID: "u1", ConnectionID: "c2"}), "r2")
	check("by source connection", ids(repo.RestoreFilter{ConnectionID: "c1"}), "r2", "r1")
	check("active", ids(repo.RestoreFilter{ActiveOnly: true}), "r3", "r2")
	check("limit", ids(repo.RestoreFilter{Limit: 1}), "r3")
	check("other user", ids(repo.RestoreFilter{UserID: "u2"}))

	n, err := s.FailUnfinishedRestores(ctx, "interrupted", t0.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("FailUnfinishedRestores = %d, %v", n, err)
	}
	r2, _ := s.GetRestore(ctx, "", "r2")
	if r2.Status != repo.RestoreFailed || r2.Error != "interrupted" || !r2.FinishedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("r2 after fail = %+v", r2)
	}

	if err := s.DeleteRestoresForConnection(ctx, "c2"); err != nil {
		t.Fatal(err)
	}
	check("after deleting c2", ids(repo.RestoreFilter{}), "r3", "r1")
}
