package wasabisync

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/connection"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/repo/wasabi/memory"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// --- fakes ---

type span struct{ from, to time.Time }

// fakeStats publishes a day's figures the day after, like Wasabi: it
// returns account and two buckets' usage for from..min(to, yesterday).
type fakeStats struct {
	mu      sync.Mutex
	now     func() time.Time
	calls   []span
	failAt  int // fail the n-th AccountUsage call (1-based); 0 never
	err     error
	block   chan struct{}
	started chan struct{}
}

func (f *fakeStats) AccountUsage(ctx context.Context, from, to time.Time) ([]wasabi.Usage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, span{from, to})
	n := len(f.calls)
	block, started := f.block, f.started
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.failAt == n {
		return nil, f.err
	}
	return f.days(from, to, ""), nil
}

func (f *fakeStats) BucketUsage(_ context.Context, from, to time.Time) ([]wasabi.Usage, error) {
	return append(f.days(from, to, "media"), f.days(from, to, "logs")...), nil
}

func (f *fakeStats) days(from, to time.Time, bucket string) []wasabi.Usage {
	yesterday := wasabi.DayOf(f.now()).AddDate(0, 0, -1)
	var out []wasabi.Usage
	for d := from; !d.After(to) && !d.After(yesterday); d = d.AddDate(0, 0, 1) {
		out = append(out, wasabi.Usage{Day: d, Bucket: bucket, PaddedStorageSizeBytes: int64(d.YearDay())})
	}
	return out
}

func (f *fakeStats) spans() []span {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]span(nil), f.calls...)
}

// rows lists a connection's stored rows for one bucket ("" = account).
func (h *harness) rows(connectionID, bucket string) []wasabi.Usage {
	rows, _ := h.repo.ListUsage(context.Background(), connectionID, bucket, day("2000-01-01"), day("2100-01-01"))
	return rows
}

// memConns stores secrets unsealed and records RecordStatus calls.
type memConns struct {
	mu       sync.Mutex
	conns    map[string]*connrepo.Connection
	statuses map[string][]error
}

func (c *memConns) List(_ context.Context, f connrepo.Filter) ([]*connrepo.Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*connrepo.Connection
	for _, conn := range c.conns {
		if f.Provider == "" || conn.Provider == f.Provider {
			cp := *conn
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (c *memConns) GetByID(_ context.Context, id string) (*connrepo.Connection, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, ok := c.conns[id]
	if !ok {
		return nil, connrepo.ErrNotFound
	}
	cp := *conn
	return &cp, nil
}
func (c *memConns) Open(conn *connrepo.Connection, config, secret any) error {
	if err := json.Unmarshal(conn.Config, config); err != nil {
		return err
	}
	return json.Unmarshal([]byte(conn.SecretCiphertext), secret)
}
func (c *memConns) RecordStatus(_ context.Context, id string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses[id] = append(c.statuses[id], err)
}
func (c *memConns) recorded(id string) []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]error(nil), c.statuses[id]...)
}

// --- harness ---

type harness struct {
	m     *Manager
	repo  *memory.Store
	conns *memConns
	api   *fakeStats

	mu    sync.Mutex
	clock time.Time
}

func (h *harness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.clock = h.clock.Add(d)
	h.mu.Unlock()
}

func wasabiConn(id string) *connrepo.Connection {
	return &connrepo.Connection{
		ID: id, UserID: "u1", Provider: wasabi.ProviderType,
		Config:           json.RawMessage(`{"access_key_id":"AK","cost_estimate_enabled":true}`),
		SecretCiphertext: `{"secret_key":"SK"}`,
	}
}

func newHarness(t *testing.T, conns ...*connrepo.Connection) *harness {
	t.Helper()
	h := &harness{repo: memory.New(), clock: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	h.conns = &memConns{conns: map[string]*connrepo.Connection{}, statuses: map[string][]error{}}
	for _, c := range conns {
		h.conns.conns[c.ID] = c
	}
	h.api = &fakeStats{now: h.now}
	h.m = New(Config{}, h.repo, h.conns)
	h.m.now = h.now
	h.m.newClient = func(ak, sk string) (Stats, error) {
		if ak != "AK" || sk != "SK" {
			t.Errorf("keys = %q/%q", ak, sk)
		}
		return h.api, nil
	}
	return h
}

func (h *harness) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); h.m.Wait() })
	if err := h.m.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) waitIdle(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.m.Syncing(id) {
		if time.Now().After(deadline) {
			t.Fatalf("sync of %s did not finish", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func day(s string) time.Time {
	t, err := time.Parse(wasabi.DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

// --- tests ---

func TestBackfillThenDailySync(t *testing.T) {
	h := newHarness(t, wasabiConn("c1"))
	h.start(t) // catch-up queues the never-synced connection
	h.waitIdle(t, "c1")

	// 2025-09-30..2026-09-30 is 366 days: 12 chunks of 30 days and one of 6.
	spans := h.api.spans()
	if len(spans) != 13 || !spans[0].from.Equal(day("2025-09-30")) || !spans[12].to.Equal(day("2026-09-30")) {
		t.Fatalf("backfill spans: %d, first %v, last %v", len(spans), spans[0], spans[len(spans)-1])
	}
	st, _ := h.repo.GetSyncState(context.Background(), "c1")
	if st.BackfillCompletedAt.IsZero() || !st.LastSyncedDay.Equal(day("2026-09-29")) || st.LastSuccessAt.IsZero() {
		t.Fatalf("state after backfill: %+v", st)
	}
	if n := len(h.rows("c1", "")); n != 365 {
		t.Fatalf("account rows = %d, want 365 (through yesterday)", n)
	}
	if n := len(h.rows("c1", "media")); n != 365 {
		t.Fatalf("bucket rows = %d", n)
	}
	if got := h.conns.recorded("c1"); len(got) != 1 || got[0] != nil {
		t.Fatalf("health after backfill: %v", got)
	}
	if p := BackfillProgress(st, h.now()); p != 1 {
		t.Fatalf("progress = %v", p)
	}

	// Two days later (one daily run missed) the next run starts at the
	// last synced day.
	h.advance(48 * time.Hour)
	h.m.queueAll(context.Background())
	h.waitIdle(t, "c1")
	spans = h.api.spans()
	if last := spans[len(spans)-1]; !last.from.Equal(day("2026-09-29")) || !last.to.Equal(day("2026-10-02")) {
		t.Fatalf("daily sync span = %v", last)
	}
	if st, _ = h.repo.GetSyncState(context.Background(), "c1"); !st.LastSyncedDay.Equal(day("2026-10-01")) {
		t.Fatalf("last synced day = %s", st.LastSyncedDay)
	}
}

func TestBackfillResumesAfterFailure(t *testing.T) {
	h := newHarness(t, wasabiConn("c1"))
	h.api.failAt, h.api.err = 3, &wasabi.APIError{StatusCode: 503, Path: "/v1/standalone/utilizations", Message: "busy"}
	h.start(t)
	h.waitIdle(t, "c1")

	st, _ := h.repo.GetSyncState(context.Background(), "c1")
	if !st.BackfillCompletedAt.IsZero() || !st.BackfillThrough.Equal(day("2025-11-28")) {
		t.Fatalf("state after failed chunk 3: %+v", st)
	}
	if p := BackfillProgress(st, h.now()); p <= 0 || p >= 1 {
		t.Fatalf("progress = %v", p)
	}
	if got := h.conns.recorded("c1"); len(got) != 1 || got[0] == nil {
		t.Fatalf("health after failure: %v", got)
	}

	h.api.failAt = 0
	h.m.queueAll(context.Background())
	h.waitIdle(t, "c1")
	spans := h.api.spans()
	if resumed := spans[3]; !resumed.from.Equal(day("2025-11-29")) {
		t.Fatalf("resumed at %s, want 2025-11-29", resumed.from.Format(wasabi.DateLayout))
	}
	if st, _ = h.repo.GetSyncState(context.Background(), "c1"); st.BackfillCompletedAt.IsZero() {
		t.Fatalf("backfill not completed on retry: %+v", st)
	}
	if got := h.conns.recorded("c1"); len(got) != 2 || got[1] != nil {
		t.Fatalf("health after retry: %v", got)
	}
}

func TestRefreshRefetchesLastWeek(t *testing.T) {
	h := newHarness(t, wasabiConn("c1"))
	h.start(t)
	h.waitIdle(t, "c1")

	h.m.Refresh("c1")
	h.waitIdle(t, "c1")
	spans := h.api.spans()
	if last := spans[len(spans)-1]; !last.from.Equal(day("2026-09-24")) || !last.to.Equal(day("2026-09-30")) {
		t.Fatalf("refresh span = %v", last)
	}
}

func TestQueueDeduplicates(t *testing.T) {
	h := newHarness(t)
	if !h.m.enqueue(job{connectionID: "c1"}) || h.m.enqueue(job{connectionID: "c1", refresh: true}) {
		t.Fatal("a queued connection should not be queued twice")
	}
	if !h.m.Syncing("c1") {
		t.Fatal("Syncing should report a queued connection")
	}
}

func TestCleanupWhileSyncing(t *testing.T) {
	h := newHarness(t, wasabiConn("c1"))
	block := make(chan struct{})
	h.api.block, h.api.started = block, make(chan struct{}, 1)
	h.start(t)
	<-h.api.started // the backfill is running

	p := h.m.ConnectionProvider()
	if err := p.Cleanup(context.Background(), wasabiConn("c1")); !errors.Is(err, connection.ErrBusy) {
		t.Fatalf("Cleanup while syncing = %v, want ErrBusy", err)
	}
	close(block) // a closed channel no longer blocks later calls
	h.waitIdle(t, "c1")
	if err := p.Cleanup(context.Background(), wasabiConn("c1")); err != nil {
		t.Fatalf("Cleanup after the sync: %v", err)
	}
	if n := len(h.rows("c1", "")); n != 0 {
		t.Fatalf("usage left after Cleanup: %d", n)
	}
}

func TestCleanupDropsQueuedSync(t *testing.T) {
	h := newHarness(t)
	_ = h.repo.UpsertUsage(context.Background(), "c1", []wasabi.Usage{{Day: day("2026-09-01")}})
	h.m.enqueue(job{connectionID: "c1"})

	p := h.m.ConnectionProvider()
	if err := p.Cleanup(context.Background(), wasabiConn("c1")); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if n := len(h.rows("c1", "")); n != 0 {
		t.Fatalf("usage left after Cleanup: %d", n)
	}
	h.start(t) // the worker skips the cancelled job
	h.waitIdle(t, "c1")
	if n := len(h.api.spans()); n != 0 {
		t.Fatalf("a deleted connection was synced (%d calls)", n)
	}
}

func TestCatchUpSkipsFreshConnections(t *testing.T) {
	h := newHarness(t, wasabiConn("fresh"), wasabiConn("stale"))
	now := h.now()
	_ = h.repo.SaveSyncState(context.Background(), &repo.SyncState{ConnectionID: "fresh", BackfillCompletedAt: now, LastSuccessAt: now.Add(-3 * time.Hour)})
	_ = h.repo.SaveSyncState(context.Background(), &repo.SyncState{ConnectionID: "stale", BackfillCompletedAt: now, LastSuccessAt: now.Add(-30 * time.Hour), LastSyncedDay: day("2026-09-28")})

	h.start(t)
	h.waitIdle(t, "stale")
	h.waitIdle(t, "fresh")
	if got := h.conns.recorded("fresh"); len(got) != 0 {
		t.Fatalf("fresh connection was synced: %v", got)
	}
	if got := h.conns.recorded("stale"); len(got) != 1 {
		t.Fatalf("stale connection was not synced: %v", got)
	}
}

func TestProviderVerifyAndActivate(t *testing.T) {
	h := newHarness(t, wasabiConn("c1"))
	p := h.m.ConnectionProvider()
	cfg, sec := json.RawMessage(`{"access_key_id":"AK"}`), json.RawMessage(`{"secret_key":"SK"}`)

	if err := p.Verify(context.Background(), cfg, sec); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if last := h.api.spans()[0]; !last.from.Equal(day("2026-09-29")) || !last.to.Equal(day("2026-09-30")) {
		t.Fatalf("Verify span = %v", last)
	}
	h.api.failAt, h.api.err = 2, &wasabi.APIError{StatusCode: 403, Message: "Access denied"}
	if err := p.Verify(context.Background(), cfg, sec); !wasabi.IsAuthError(err) {
		t.Fatalf("Verify with a denied key = %v", err)
	}

	p.(connection.Activator).Activate(context.Background(), wasabiConn("c1"))
	if !h.m.Syncing("c1") {
		t.Fatal("Activate should queue a sync")
	}
}
