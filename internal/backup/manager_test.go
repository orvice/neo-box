package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/repo/nocodb/memory"
)

// memConns is an in-memory Connections. Secrets are stored unsealed.
type memConns struct {
	mu       sync.Mutex
	conns    map[string]*connrepo.Connection
	statuses map[string][]error // recorded RecordStatus calls per connection
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

// fakeNocoDB serves one tiny base; block (when set) stalls ListTables so a
// test can observe an in-flight snapshot, and err (when set) fails it. As a
// restore target, writeBlock stalls CreateBase, writeErr fails it, and
// insertErr fails inserts after the Base exists.
type fakeNocoDB struct {
	block chan struct{}
	fail  bool
	err   error

	writeBlock chan struct{}
	writeErr   error
	insertErr  error
	mu         sync.Mutex
	inserted   int
}

func (f *fakeNocoDB) ListBases(context.Context) ([]nocodb.Base, error) {
	return []nocodb.Base{{ID: "p1", Title: "CRM"}}, nil
}
func (f *fakeNocoDB) GetBaseRaw(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"id":"p1"}`), nil
}
func (f *fakeNocoDB) ListTables(ctx context.Context, _ string) ([]nocodb.TableSummary, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.fail {
		return nil, errors.New("boom")
	}
	return []nocodb.TableSummary{{ID: "m1", Title: "T"}}, nil
}
func (f *fakeNocoDB) GetTable(context.Context, string, string) (*nocodb.Table, error) {
	return &nocodb.Table{ID: "m1", Title: "T", Raw: json.RawMessage(`{"id":"m1","fields":[]}`)}, nil
}
func (f *fakeNocoDB) ListRecords(context.Context, string, string, int, int) (*nocodb.RecordPage, error) {
	return &nocodb.RecordPage{Records: []nocodb.Record{{ID: json.RawMessage("1"), Fields: map[string]json.RawMessage{}}}}, nil
}
func (f *fakeNocoDB) ListLinkedIDs(context.Context, string, string, string, string) ([]json.RawMessage, error) {
	return nil, nil
}
func (f *fakeNocoDB) CreateBase(ctx context.Context, _, _ string) (string, error) {
	if f.writeBlock != nil {
		select {
		case <-f.writeBlock:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.writeErr != nil {
		return "", f.writeErr
	}
	return "pR", nil
}
func (f *fakeNocoDB) CreateTable(context.Context, string, any) (*nocodb.Table, error) {
	return &nocodb.Table{ID: "mR", Title: "T", Fields: []nocodb.Field{{ID: "fR", Title: "Id", Type: "ID"}}}, nil
}
func (f *fakeNocoDB) UpdateTable(context.Context, string, string, any) error { return nil }
func (f *fakeNocoDB) CreateField(context.Context, string, string, any) (*nocodb.Field, error) {
	return nil, errors.New("unexpected field")
}
func (f *fakeNocoDB) UpdateField(context.Context, string, string, any) error { return nil }
func (f *fakeNocoDB) InsertRecords(_ context.Context, _, _ string, records []map[string]json.RawMessage) ([]json.RawMessage, error) {
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]json.RawMessage, len(records))
	for i := range records {
		f.inserted++
		ids[i] = json.RawMessage(fmt.Sprint(f.inserted))
	}
	return ids, nil
}
func (f *fakeNocoDB) LinkRecords(context.Context, string, string, string, string, []json.RawMessage) error {
	return nil
}
func (f *fakeNocoDB) ListBaseUserEmails(context.Context, string) ([]string, error) {
	return nil, nil
}

type harness struct {
	m     *Manager
	repo  *memory.Store
	conns *memConns
	api   *fakeNocoDB
	conn  *connrepo.Connection
	clock time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{repo: memory.New(), api: &fakeNocoDB{}, clock: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	h.conn = &connrepo.Connection{
		ID: "c1", UserID: "u1", Provider: nocodb.ProviderType,
		Config: json.RawMessage(`{"base_url":"http://noco"}`), SecretCiphertext: `{"api_token":"tok"}`,
	}
	h.conns = &memConns{conns: map[string]*connrepo.Connection{"c1": h.conn}, statuses: map[string][]error{}}

	h.m = New(Config{}, h.repo, h.conns, blobs)
	h.m.newClient = func(_ nocodb.ConnectionConfig, token string, _ *rate.Limiter) (NocoDB, error) {
		if token != "tok" {
			t.Errorf("token = %q", token)
		}
		return h.api, nil
	}
	// Called from the test and from workers.
	var clockMu sync.Mutex
	h.m.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		h.clock = h.clock.Add(time.Minute)
		return h.clock
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); h.m.Wait() })
	if err := h.m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) waitDone(t *testing.T, id string) *repo.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := h.repo.GetSnapshot(context.Background(), "", id)
		if err == nil && (s.Status == repo.StatusSucceeded || s.Status == repo.StatusFailed) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("snapshot %s did not finish", id)
	return nil
}

func TestSnapshotSucceedsAndContentIsReadable(t *testing.T) {
	h := newHarness(t)
	snap, err := h.m.Enqueue(context.Background(), h.conn, "p1", "", repo.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitDone(t, snap.ID)
	if done.Status != repo.StatusSucceeded {
		t.Fatalf("status = %s (%s)", done.Status, done.Error)
	}
	if done.BaseTitle != "CRM" || done.RecordCount != 1 || len(done.Tables) != 1 || done.SizeBytes == 0 {
		t.Fatalf("snapshot = %+v", done)
	}
	rc, err := h.m.OpenContent(context.Background(), done)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestSecondSnapshotOfSameBaseRejectedWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.api.block = make(chan struct{})
	first, err := h.m.Enqueue(context.Background(), h.conn, "p1", "", repo.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Enqueue(context.Background(), h.conn, "p1", "", repo.TriggerManual); !errors.Is(err, ErrSnapshotInProgress) {
		t.Fatalf("second enqueue err = %v", err)
	}
	if err := h.m.DeleteSnapshot(context.Background(), first); !errors.Is(err, ErrSnapshotInProgress) {
		t.Fatalf("delete in-flight err = %v", err)
	}
	close(h.api.block)
	h.waitDone(t, first.ID)
	if _, err := h.m.Enqueue(context.Background(), h.conn, "p1", "", repo.TriggerManual); err != nil {
		t.Fatalf("enqueue after finish: %v", err)
	}
}

func TestFailedSnapshotRecordsError(t *testing.T) {
	h := newHarness(t)
	h.api.fail = true
	snap, _ := h.m.Enqueue(context.Background(), h.conn, "p1", "", repo.TriggerManual)
	done := h.waitDone(t, snap.ID)
	if done.Status != repo.StatusFailed || done.Error == "" {
		t.Fatalf("snapshot = %+v", done)
	}
}

func TestRetentionPrunesOnlyOldScheduledSnapshots(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_ = h.repo.UpsertPolicy(ctx, &repo.Policy{ConnectionID: "c1", BaseID: "p1", UserID: "u1", Retention: 2})

	manual, _ := h.m.Enqueue(ctx, h.conn, "p1", "", repo.TriggerManual)
	h.waitDone(t, manual.ID)
	var scheduled []string
	for i := 0; i < 4; i++ {
		s, err := h.m.Enqueue(ctx, h.conn, "p1", "", repo.TriggerScheduled)
		if err != nil {
			t.Fatal(err)
		}
		h.waitDone(t, s.ID)
		scheduled = append(scheduled, s.ID)
	}

	left, _ := h.repo.ListSnapshots(ctx, repo.SnapshotFilter{BaseID: "p1"})
	ids := map[string]bool{}
	for _, s := range left {
		ids[s.ID] = true
	}
	if len(left) != 3 || !ids[manual.ID] || !ids[scheduled[2]] || !ids[scheduled[3]] {
		t.Fatalf("remaining = %v (scheduled %v, manual %s)", ids, scheduled, manual.ID)
	}
}

func TestValidateCron(t *testing.T) {
	for _, ok := range []string{"0 3 * * *", "@daily", "CRON_TZ=Asia/Shanghai 0 3 * * *"} {
		if err := ValidateCron(ok); err != nil {
			t.Errorf("ValidateCron(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "* * *", "0 0 3 * * *"} {
		if err := ValidateCron(bad); err == nil {
			t.Errorf("ValidateCron(%q) should fail", bad)
		}
	}
}

func TestSnapshotRecordsConnectionHealth(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	run := func() {
		t.Helper()
		snap, err := h.m.Enqueue(ctx, h.conn, "p1", "", repo.TriggerManual)
		if err != nil {
			t.Fatal(err)
		}
		h.waitDone(t, snap.ID)
	}

	run()
	if got := h.conns.recorded("c1"); len(got) != 1 || got[0] != nil {
		t.Fatalf("after success recorded %v, want [nil]", got)
	}

	h.api.err = &nocodb.APIError{StatusCode: 401, Method: "GET", Path: "/tables", Message: "unauthorized"}
	run()
	if got := h.conns.recorded("c1"); len(got) != 2 || !nocodb.IsAuthError(got[1]) {
		t.Fatalf("after 401 recorded %v, want an auth error", got)
	}

	h.api.err = &nocodb.APIError{StatusCode: 502, Method: "GET", Path: "/tables", Message: "bad gateway"}
	run()
	if got := h.conns.recorded("c1"); len(got) != 2 {
		t.Fatalf("a 502 should not change health; recorded %v", got)
	}
}

func TestProviderCleanup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	p := h.m.ConnectionProvider()

	h.api.block = make(chan struct{})
	running, err := h.m.Enqueue(ctx, h.conn, "p1", "", repo.TriggerManual)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Cleanup(ctx, h.conn); !errors.Is(err, connection.ErrBusy) {
		t.Fatalf("Cleanup while running = %v, want ErrBusy", err)
	}
	close(h.api.block)
	h.waitDone(t, running.ID)

	_ = h.repo.UpsertPolicy(ctx, &repo.Policy{ConnectionID: "c1", BaseID: "p1", UserID: "u1", Enabled: true, Cron: "@daily"})
	if err := p.Cleanup(ctx, h.conn); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if left, _ := h.repo.ListSnapshots(ctx, repo.SnapshotFilter{ConnectionID: "c1"}); len(left) != 0 {
		t.Fatalf("snapshots left after cleanup: %d", len(left))
	}
	if left, _ := h.repo.ListPolicies(ctx, "u1", "c1"); len(left) != 0 {
		t.Fatalf("policies left after cleanup: %d", len(left))
	}
}
