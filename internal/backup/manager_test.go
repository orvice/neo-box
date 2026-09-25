package backup

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/nocodb"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

// memRepo is an in-memory repo.Repository for tests.
type memRepo struct {
	mu       sync.Mutex
	conns    map[string]*repo.Connection
	policies map[string]*repo.Policy
	snaps    map[string]*repo.Snapshot
}

func newMemRepo() *memRepo {
	return &memRepo{conns: map[string]*repo.Connection{}, policies: map[string]*repo.Policy{}, snaps: map[string]*repo.Snapshot{}}
}

func (r *memRepo) EnsureIndexes(context.Context) error { return nil }
func (r *memRepo) CreateConnection(_ context.Context, c *repo.Connection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *c
	r.conns[c.ID] = &cp
	return nil
}
func (r *memRepo) GetConnection(ctx context.Context, userID, id string) (*repo.Connection, error) {
	c, err := r.GetConnectionByID(ctx, id)
	if err != nil || c.UserID != userID {
		return nil, repo.ErrNotFound
	}
	return c, nil
}
func (r *memRepo) GetConnectionByID(_ context.Context, id string) (*repo.Connection, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.conns[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}
func (r *memRepo) ListConnections(context.Context, string) ([]*repo.Connection, error) {
	return nil, nil
}
func (r *memRepo) UpdateConnection(context.Context, *repo.Connection) error { return nil }
func (r *memRepo) DeleteConnection(context.Context, string, string) error   { return nil }
func (r *memRepo) UpsertPolicy(_ context.Context, p *repo.Policy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *p
	r.policies[p.ConnectionID+"/"+p.BaseID] = &cp
	return nil
}
func (r *memRepo) ListPolicies(_ context.Context, userID, connectionID string) ([]*repo.Policy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*repo.Policy
	for _, p := range r.policies {
		if p.UserID == userID && p.ConnectionID == connectionID {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *memRepo) ListEnabledPolicies(context.Context) ([]*repo.Policy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*repo.Policy
	for _, p := range r.policies {
		if p.Enabled {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *memRepo) DeletePoliciesForConnection(context.Context, string) error { return nil }
func (r *memRepo) CreateSnapshot(_ context.Context, s *repo.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *s
	r.snaps[s.ID] = &cp
	return nil
}
func (r *memRepo) GetSnapshot(_ context.Context, userID, id string) (*repo.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.snaps[id]
	if !ok || (userID != "" && s.UserID != userID) {
		return nil, repo.ErrNotFound
	}
	cp := *s
	return &cp, nil
}
func (r *memRepo) UpdateSnapshot(ctx context.Context, s *repo.Snapshot) error {
	return r.CreateSnapshot(ctx, s)
}
func (r *memRepo) UpdateSnapshotProgress(_ context.Context, id, progress string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.snaps[id]; ok {
		s.Progress = progress
	}
	return nil
}
func (r *memRepo) ListSnapshots(_ context.Context, f repo.SnapshotFilter) ([]*repo.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*repo.Snapshot
	for _, s := range r.snaps {
		if (f.ConnectionID == "" || s.ConnectionID == f.ConnectionID) &&
			(f.BaseID == "" || s.BaseID == f.BaseID) &&
			(f.Trigger == "" || s.Trigger == f.Trigger) &&
			(f.Status == "" || s.Status == f.Status) {
			cp := *s
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (r *memRepo) DeleteSnapshot(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.snaps, id)
	return nil
}
func (r *memRepo) FailUnfinishedSnapshots(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

// fakeNocoDB serves one tiny base; block (when set) stalls ListTables so a
// test can observe an in-flight snapshot.
type fakeNocoDB struct {
	block chan struct{}
	fail  bool
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

type harness struct {
	m     *Manager
	repo  *memRepo
	api   *fakeNocoDB
	conn  *repo.Connection
	clock time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{repo: newMemRepo(), api: &fakeNocoDB{}, clock: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	ct, _ := cipher.Encrypt([]byte("tok"))
	h.conn = &repo.Connection{ID: "c1", UserID: "u1", BaseURL: "http://noco", TokenCiphertext: ct}
	_ = h.repo.CreateConnection(context.Background(), h.conn)

	h.m = New(Config{}, h.repo, blobs, cipher)
	h.m.newClient = func(_, token string) (NocoDB, error) {
		if token != "tok" {
			t.Errorf("token = %q", token)
		}
		return h.api, nil
	}
	h.m.now = func() time.Time {
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
