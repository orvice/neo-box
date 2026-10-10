package backup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/notify"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

// target adds a second NocoDB connection, c2, to restore into.
func (h *harness) target() *connrepo.Connection {
	c := *h.conn
	c.ID = "c2"
	h.conns.mu.Lock()
	h.conns.conns["c2"] = &c
	h.conns.mu.Unlock()
	return &c
}

// snapshot takes a manual snapshot of p1 and waits for it.
func (h *harness) snapshot(t *testing.T) *repo.Snapshot {
	t.Helper()
	snap, err := h.m.Enqueue(context.Background(), h.conn, "p1", "CRM", repo.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	return h.waitDone(t, snap.ID)
}

func (h *harness) waitRestore(t *testing.T, id string) *repo.Restore {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := h.repo.GetRestore(context.Background(), "", id)
		if err == nil && !r.Active() {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("restore %s did not finish", id)
	return nil
}

func TestRestoreSucceeds(t *testing.T) {
	h := newHarness(t)
	h.api.hooks = true
	snap := h.snapshot(t)
	target := h.target()

	r, err := h.m.EnqueueRestore(context.Background(), snap, target, "CRM copy")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != repo.RestorePending || r.SourceConnectionID != "c1" || r.SourceBaseTitle != "CRM" || r.TargetConnectionID != "c2" {
		t.Fatalf("queued = %+v", r)
	}
	done := h.waitRestore(t, r.ID)
	if done.Status != repo.RestoreSucceeded || done.Error != "" || done.TargetBaseID != "pR" ||
		done.TableCount != 1 || done.RecordCount != 1 || done.HookCount != 1 || done.StartedAt.IsZero() || done.FinishedAt.IsZero() {
		t.Fatalf("done = %+v", done)
	}
	if len(done.Warnings) != 1 || done.Warnings[0].Code != "hooks_disabled" || done.Warnings[0].Count != 1 {
		t.Fatalf("warnings = %+v", done.Warnings)
	}
	if got := h.conns.recorded("c2"); len(got) != 1 || got[0] != nil {
		t.Fatalf("target health recorded %v, want [nil]", got)
	}
}

func TestRestoreNeedsSucceededSnapshot(t *testing.T) {
	h := newHarness(t)
	h.api.fail = true
	snap := h.snapshot(t)
	if _, err := h.m.EnqueueRestore(context.Background(), snap, h.conn, "x"); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("err = %v, want ErrNotRestorable", err)
	}
}

func TestRestoreFailureKeepsPartialBase(t *testing.T) {
	h := newHarness(t)
	snap := h.snapshot(t)
	target := h.target()

	h.api.insertErr = &nocodb.APIError{StatusCode: 500, Method: "POST", Path: "/records", Message: "boom"}
	r, err := h.m.EnqueueRestore(context.Background(), snap, target, "x")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitRestore(t, r.ID)
	if done.Status != repo.RestoreFailed || !strings.Contains(done.Error, "boom") || done.TargetBaseID != "pR" || done.TableCount != 1 {
		t.Fatalf("done = %+v", done)
	}
	if got := h.conns.recorded("c2"); len(got) != 0 {
		t.Fatalf("a 500 should not change health; recorded %v", got)
	}

	h.api.insertErr = nil
	h.api.writeErr = &nocodb.APIError{StatusCode: 401, Method: "POST", Path: "/bases", Message: "unauthorized"}
	r, err = h.m.EnqueueRestore(context.Background(), snap, target, "x")
	if err != nil {
		t.Fatal(err)
	}
	done = h.waitRestore(t, r.ID)
	if done.Status != repo.RestoreFailed || done.TargetBaseID != "" {
		t.Fatalf("done = %+v", done)
	}
	if got := h.conns.recorded("c2"); len(got) != 1 || !nocodb.IsAuthError(got[0]) {
		t.Fatalf("target health recorded %v, want an auth error", got)
	}
}

func TestRestoreBlocksDeletes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	snap := h.snapshot(t)
	target := h.target()
	p := h.m.ConnectionProvider()

	h.api.writeBlock = make(chan struct{})
	r, err := h.m.EnqueueRestore(ctx, snap, target, "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.DeleteSnapshot(ctx, snap); !errors.Is(err, ErrRestoreInProgress) {
		t.Fatalf("DeleteSnapshot while restoring = %v, want ErrRestoreInProgress", err)
	}
	for _, c := range []*connrepo.Connection{h.conn, target} {
		if err := p.Cleanup(ctx, c); !errors.Is(err, connection.ErrBusy) {
			t.Fatalf("Cleanup %s while restoring = %v, want ErrBusy", c.ID, err)
		}
	}
	close(h.api.writeBlock)
	h.waitRestore(t, r.ID)

	if err := p.Cleanup(ctx, target); err != nil {
		t.Fatalf("Cleanup target: %v", err)
	}
	if left, _ := h.repo.ListRestores(ctx, repo.RestoreFilter{}); len(left) != 0 {
		t.Fatalf("restores left after target cleanup: %+v", left)
	}
	if err := h.m.DeleteSnapshot(ctx, snap); err != nil {
		t.Fatalf("DeleteSnapshot after restore: %v", err)
	}
}

func TestRetentionSkipsSnapshotBeingRestored(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_ = h.repo.UpsertPolicy(ctx, &repo.Policy{ConnectionID: "c1", BaseID: "p1", UserID: "u1", Retention: 1})
	var scheduled []string
	for i := 0; i < 3; i++ {
		s := &repo.Snapshot{
			ID: string(rune('a' + i)), UserID: "u1", ConnectionID: "c1", BaseID: "p1",
			Status: repo.StatusSucceeded, Trigger: repo.TriggerScheduled, CreatedAt: time.Date(2026, 9, 1+i, 0, 0, 0, 0, time.UTC),
		}
		_ = h.repo.CreateSnapshot(ctx, s)
		scheduled = append(scheduled, s.ID)
	}
	// The oldest is being restored.
	_ = h.repo.CreateRestore(ctx, &repo.Restore{ID: "r1", UserID: "u1", SnapshotID: scheduled[0], Status: repo.RestoreRunning})

	if err := h.m.applyRetention(ctx, "c1", "p1"); err != nil {
		t.Fatalf("applyRetention: %v", err)
	}
	left, _ := h.repo.ListSnapshots(ctx, repo.SnapshotFilter{BaseID: "p1"})
	if len(left) != 2 || left[0].ID != scheduled[2] || left[1].ID != scheduled[0] {
		t.Fatalf("remaining = %+v", left)
	}
}

func TestStartFailsUnfinishedRestores(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_ = h.repo.CreateRestore(ctx, &repo.Restore{ID: "r1", UserID: "u1", Status: repo.RestoreRunning, TargetBaseID: "pPartial"})

	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Config{}, h.repo, h.conns, blobs)
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); restarted.Wait() })
	if err := restarted.Start(runCtx); err != nil {
		t.Fatal(err)
	}
	r, _ := h.repo.GetRestore(ctx, "", "r1")
	if r.Status != repo.RestoreFailed || r.Error != "interrupted by server restart" || r.TargetBaseID != "pPartial" {
		t.Fatalf("after restart = %+v", r)
	}
}

func TestLimiterSharedPerConnection(t *testing.T) {
	h := newHarness(t)
	a := h.m.limiter("c1", 5)
	if h.m.limiter("c1", 5) != a {
		t.Fatal("same connection and rate should share a limiter")
	}
	if h.m.limiter("c2", 5) == a {
		t.Fatal("connections should not share a limiter")
	}
	if h.m.limiter("c1", 10) == a {
		t.Fatal("a new rate should replace the limiter")
	}
}

type recordingNotifier struct {
	mu     sync.Mutex
	alerts []notify.Alert
}

func (n *recordingNotifier) Notify(_ context.Context, a notify.Alert) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, a)
}

func (n *recordingNotifier) kinds() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, a := range n.alerts {
		out = append(out, a.Kind)
	}
	return out
}

func TestFailuresRaiseAlerts(t *testing.T) {
	h := newHarness(t)
	n := &recordingNotifier{}
	h.m.SetNotifier(n)
	ctx := context.Background()

	// A failed manual snapshot is watched in the dashboard: no alert.
	h.api.fail = true
	manual, _ := h.m.Enqueue(ctx, h.conn, "p1", "CRM", repo.TriggerManual)
	h.waitDone(t, manual.ID)
	scheduled, _ := h.m.Enqueue(ctx, h.conn, "p1", "CRM", repo.TriggerScheduled)
	h.waitDone(t, scheduled.ID)
	if got := n.kinds(); len(got) != 1 || got[0] != "snapshot_failed" {
		t.Fatalf("alerts after snapshots = %v", got)
	}
	a := n.alerts[0]
	if a.UserID != "u1" || a.Key != "nocodb:snapshot_failed:"+scheduled.ID || a.Title != `Scheduled snapshot of "CRM" failed` ||
		a.Link != "/connections/c1/snapshots/"+scheduled.ID || a.Body == "" {
		t.Fatalf("snapshot alert = %+v", a)
	}

	h.api.fail = false
	snap := h.snapshot(t)
	h.api.insertErr = &nocodb.APIError{StatusCode: 500, Method: "POST", Path: "/records", Message: "boom"}
	r, err := h.m.EnqueueRestore(ctx, snap, h.conn, "CRM copy")
	if err != nil {
		t.Fatal(err)
	}
	h.waitRestore(t, r.ID)
	if got := n.kinds(); len(got) != 2 || got[1] != "restore_failed" {
		t.Fatalf("alerts after restore = %v", got)
	}
	a = n.alerts[1]
	if a.Key != "nocodb:restore_failed:"+r.ID || !strings.Contains(a.Body, `The partial Base pR ("CRM copy") is left in NocoDB.`) {
		t.Fatalf("restore alert = %+v", a)
	}
}
