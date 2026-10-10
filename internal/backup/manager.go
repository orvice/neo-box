// Package backup runs NocoDB Base snapshots and restores: a bounded worker
// queue executes snapshot jobs, a cron scheduler enqueues them from Backup
// Policies, retention prunes old scheduled snapshots, and a separate queue
// rebuilds snapshots into new Bases.
//
// State lives in PostgreSQL (snapshot and restore metadata) and the blob
// store (content, and attachment files shared across snapshots; see
// files.go). Nothing in-flight survives a restart: Start marks leftover
// pending/running snapshots and restores failed. The scheduler and file
// collection assume a single neo-box process.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/notify"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/snapshot"
)

// NocoDB is the client surface the manager and RPC layer use.
type NocoDB interface {
	snapshot.API
	snapshot.WriteAPI
	ListBases(ctx context.Context) ([]nocodb.Base, error)
	Download(ctx context.Context, ref string) (io.ReadCloser, error)
}

// Connections is what the manager needs from the generic connection
// service (*connection.Service).
type Connections interface {
	GetByID(ctx context.Context, id string) (*connrepo.Connection, error)
	// Open decodes the connection's config and decrypted secret.
	Open(c *connrepo.Connection, config, secret any) error
	// RecordStatus records the connection's health after background work.
	RecordStatus(ctx context.Context, id string, err error)
}

var (
	// ErrSnapshotInProgress is returned when the Base already has a
	// pending or running snapshot.
	ErrSnapshotInProgress = errors.New("a snapshot of this base is already in progress")
	// ErrRestoreInProgress is returned when deleting a snapshot that a
	// pending or running restore reads.
	ErrRestoreInProgress = errors.New("a restore of this snapshot is in progress")
	// ErrNotRestorable is returned when restoring a snapshot that has not
	// succeeded.
	ErrNotRestorable = errors.New("only a succeeded snapshot can be restored")
	// ErrQueueFull is returned when a job can't be queued.
	ErrQueueFull = errors.New("the queue is full, try again later")
)

// Config tunes the manager.
type Config struct {
	Workers   int
	QueueSize int
	PageSize  int
	// SnapshotTimeout bounds one snapshot run.
	SnapshotTimeout time.Duration
	// RestoreTimeout bounds one restore run.
	RestoreTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 1
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 64
	}
	if c.PageSize <= 0 {
		c.PageSize = 200
	}
	if c.SnapshotTimeout <= 0 {
		c.SnapshotTimeout = 2 * time.Hour
	}
	if c.RestoreTimeout <= 0 {
		c.RestoreTimeout = 6 * time.Hour
	}
	return c
}

// Manager owns snapshot execution and scheduling.
type Manager struct {
	cfg   Config
	repo  repo.Repository
	conns Connections
	blobs blobstore.Store

	queue        chan string
	restoreQueue chan string
	wg           sync.WaitGroup

	mu       sync.Mutex
	cron     *cron.Cron
	entries  map[string]cron.EntryID // policy key -> entry
	inFlight map[string]string       // connection/base -> snapshot id
	limiters map[string]*connLimiter // connection id -> shared limiter
	notifier notify.Notifier

	// filesMu orders registering attachment files against collecting
	// unused ones (files.go).
	filesMu sync.RWMutex

	// newClient is swapped in tests. limiter is nil for a connection that
	// isn't stored yet.
	newClient func(cfg nocodb.ConnectionConfig, token string, limiter *rate.Limiter) (NocoDB, error)
	now       func() time.Time
}

// New builds a manager. Call Start before use.
func New(cfg Config, r repo.Repository, conns Connections, blobs blobstore.Store) *Manager {
	cfg = cfg.withDefaults()
	m := &Manager{
		cfg:          cfg,
		repo:         r,
		conns:        conns,
		blobs:        blobs,
		queue:        make(chan string, cfg.QueueSize),
		restoreQueue: make(chan string, cfg.QueueSize),
		entries:      make(map[string]cron.EntryID),
		inFlight:     make(map[string]string),
		limiters:     make(map[string]*connLimiter),
		now:          time.Now,
	}
	m.newClient = NewClient
	return m
}

// Start fails interrupted snapshots and restores, launches workers, and
// loads schedules. Workers stop when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	n, err := m.repo.FailUnfinishedSnapshots(ctx, "interrupted by server restart", m.now().UTC())
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Warn("marked interrupted snapshots failed", "count", n)
	}
	n, err = m.repo.FailUnfinishedRestores(ctx, "interrupted by server restart", m.now().UTC())
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Warn("marked interrupted restores failed", "count", n)
	}
	for i := 0; i < m.cfg.Workers; i++ {
		m.wg.Add(1)
		go m.worker(ctx, m.queue, m.execute)
	}
	// One restore at a time: restores are rare and long, and run beside
	// snapshots rather than behind them.
	m.wg.Add(1)
	go m.worker(ctx, m.restoreQueue, m.executeRestore)
	m.mu.Lock()
	m.cron = cron.New(cron.WithParser(CronParser))
	m.cron.Start()
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		stopCtx := m.cron.Stop()
		m.mu.Unlock()
		<-stopCtx.Done()
	}()
	return m.ReloadSchedules(ctx)
}

// SetNotifier makes failed scheduled snapshots and failed restores raise
// alerts.
func (m *Manager) SetNotifier(n notify.Notifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = n
}

func (m *Manager) notify(ctx context.Context, a notify.Alert) {
	m.mu.Lock()
	n := m.notifier
	m.mu.Unlock()
	if n != nil {
		n.Notify(ctx, a)
	}
}

// Wait blocks until all workers exit (after the Start context is cancelled).
func (m *Manager) Wait() { m.wg.Wait() }

// CronParser accepts standard 5-field expressions, descriptors such as
// "@daily", and an optional "CRON_TZ=Area/City " prefix.
var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ValidateCron reports whether expr is a usable schedule.
func ValidateCron(expr string) error {
	if _, err := CronParser.Parse(expr); err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}

// NextRun returns the next scheduled time of a policy, or zero.
func (m *Manager) NextRun(connectionID, baseID string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cron == nil {
		return time.Time{}
	}
	id, ok := m.entries[policyKey(connectionID, baseID)]
	if !ok {
		return time.Time{}
	}
	return m.cron.Entry(id).Next
}

// ReloadSchedules replaces every cron entry with the enabled policies.
func (m *Manager) ReloadSchedules(ctx context.Context) error {
	policies, err := m.repo.ListEnabledPolicies(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cron == nil {
		return nil
	}
	for key, id := range m.entries {
		m.cron.Remove(id)
		delete(m.entries, key)
	}
	logger := log.FromContext(ctx)
	for _, p := range policies {
		p := p
		id, err := m.cron.AddFunc(p.Cron, func() { m.runScheduled(p) })
		if err != nil {
			logger.Warn("skipping backup policy with invalid cron", "connection_id", p.ConnectionID, "base_id", p.BaseID, "cron", p.Cron, "err", err)
			continue
		}
		m.entries[policyKey(p.ConnectionID, p.BaseID)] = id
	}
	logger.Info("backup schedules loaded", "count", len(m.entries))
	return nil
}

func (m *Manager) runScheduled(p *repo.Policy) {
	ctx := context.Background()
	logger := log.FromContext(ctx)
	conn, err := m.conns.GetByID(ctx, p.ConnectionID)
	if err != nil {
		logger.Warn("scheduled snapshot skipped: connection lookup failed", "connection_id", p.ConnectionID, "err", err)
		return
	}
	if _, err := m.Enqueue(ctx, conn, p.BaseID, "", repo.TriggerScheduled); err != nil {
		logger.Warn("scheduled snapshot not queued", "connection_id", p.ConnectionID, "base_id", p.BaseID, "err", err)
	}
}

// NewClient builds a client for a connection's instance. It uses limiter
// when given, otherwise its own limiter at the connection's requests per
// second.
func NewClient(cfg nocodb.ConnectionConfig, token string, limiter *rate.Limiter) (NocoDB, error) {
	if limiter != nil {
		return nocodb.New(cfg.BaseURL, token, nocodb.WithLimiter(limiter))
	}
	return nocodb.New(cfg.BaseURL, token, nocodb.WithRateLimit(cfg.RateLimit()))
}

type connLimiter struct {
	rps     float64
	limiter *rate.Limiter
}

// limiter returns the rate limiter shared by every client of a connection,
// so a snapshot and a restore running together stay within the
// connection's rate. It is replaced when the rate changes.
func (m *Manager) limiter(connectionID string, rps float64) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.limiters[connectionID]; ok && l.rps == rps {
		return l.limiter
	}
	l := &connLimiter{rps: rps, limiter: rate.NewLimiter(rate.Limit(rps), 1)}
	m.limiters[connectionID] = l
	return l.limiter
}

// Client returns a NocoDB client for a stored connection.
func (m *Manager) Client(conn *connrepo.Connection) (NocoDB, error) {
	api, _, err := m.open(conn)
	return api, err
}

func (m *Manager) open(conn *connrepo.Connection) (NocoDB, nocodb.ConnectionConfig, error) {
	var (
		cfg    nocodb.ConnectionConfig
		secret nocodb.ConnectionSecret
	)
	if err := m.conns.Open(conn, &cfg, &secret); err != nil {
		return nil, cfg, err
	}
	api, err := m.newClient(cfg, secret.APIToken, m.limiter(conn.ID, cfg.RateLimit()))
	return api, cfg, err
}

// Enqueue records a pending snapshot and queues it. baseTitle may be empty;
// it is filled in when the run starts.
func (m *Manager) Enqueue(ctx context.Context, conn *connrepo.Connection, baseID, baseTitle string, trigger repo.SnapshotTrigger) (*repo.Snapshot, error) {
	key := policyKey(conn.ID, baseID)
	snap := &repo.Snapshot{
		ID:           uuid.NewString(),
		UserID:       conn.UserID,
		ConnectionID: conn.ID,
		BaseID:       baseID,
		BaseTitle:    baseTitle,
		Status:       repo.StatusPending,
		Trigger:      trigger,
		CreatedAt:    m.now().UTC(),
	}

	m.mu.Lock()
	if _, busy := m.inFlight[key]; busy {
		m.mu.Unlock()
		return nil, ErrSnapshotInProgress
	}
	m.inFlight[key] = snap.ID
	m.mu.Unlock()

	if err := m.repo.CreateSnapshot(ctx, snap); err != nil {
		m.release(key)
		return nil, err
	}
	select {
	case m.queue <- snap.ID:
		return snap, nil
	default:
		m.release(key)
		snap.Status = repo.StatusFailed
		snap.Error = "snapshot queue is full"
		snap.FinishedAt = m.now().UTC()
		_ = m.repo.UpdateSnapshot(ctx, snap)
		return nil, ErrQueueFull
	}
}

func (m *Manager) release(key string) {
	m.mu.Lock()
	delete(m.inFlight, key)
	m.mu.Unlock()
}

func (m *Manager) worker(ctx context.Context, queue <-chan string, run func(context.Context, string)) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-queue:
			run(ctx, id)
		}
	}
}

func (m *Manager) execute(parent context.Context, snapshotID string) {
	logger := log.FromContext(parent)
	snap, err := m.repo.GetSnapshot(parent, "", snapshotID)
	if err != nil {
		logger.Error("snapshot job lookup failed", "snapshot_id", snapshotID, "err", err)
		return
	}
	key := policyKey(snap.ConnectionID, snap.BaseID)
	defer m.release(key)

	ctx, cancel := context.WithTimeout(parent, m.cfg.SnapshotTimeout)
	defer cancel()

	snap.Status = repo.StatusRunning
	snap.StartedAt = m.now().UTC()
	snap.Progress = "starting"
	if err := m.repo.UpdateSnapshot(ctx, snap); err != nil {
		logger.Error("snapshot start update failed", "snapshot_id", snap.ID, "err", err)
		return
	}

	runErr := m.capture(ctx, snap)
	// Persist the terminal state even if the run context expired.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer finishCancel()
	// A run proves the token works; a 401/403 proves it doesn't. Other
	// failures say nothing about the connection's health.
	if runErr == nil || nocodb.IsAuthError(runErr) {
		m.conns.RecordStatus(finishCtx, snap.ConnectionID, runErr)
	}
	snap.FinishedAt = m.now().UTC()
	snap.Progress = ""
	if runErr != nil {
		snap.Status = repo.StatusFailed
		snap.Error = runErr.Error()
		logger.Warn("snapshot failed", "snapshot_id", snap.ID, "base_id", snap.BaseID, "err", runErr)
		// A failed snapshot keeps no files.
		m.releaseFiles(finishCtx, snap)
	} else {
		snap.Status = repo.StatusSucceeded
		logger.Info("snapshot succeeded", "snapshot_id", snap.ID, "base_id", snap.BaseID,
			"records", snap.RecordCount, "links", snap.LinkCount, "bytes", snap.SizeBytes,
			"views", snap.ViewCount, "hooks", snap.HookCount, "files", snap.FileCount, "file_bytes", snap.FileBytes, "files_missing", snap.FilesMissing)
	}
	if err := m.repo.UpdateSnapshot(finishCtx, snap); err != nil {
		logger.Error("snapshot finish update failed", "snapshot_id", snap.ID, "err", err)
		return
	}
	// Manual snapshots are watched in the dashboard; scheduled ones run
	// unattended.
	if runErr != nil && snap.Trigger == repo.TriggerScheduled {
		title := snap.BaseTitle
		if title == "" {
			title = snap.BaseID
		}
		m.notify(finishCtx, notify.Alert{
			UserID: snap.UserID, Source: "nocodb", ConnectionID: snap.ConnectionID, Kind: "snapshot_failed",
			Key:      "nocodb:snapshot_failed:" + snap.ID,
			Severity: notify.Warning,
			Title:    fmt.Sprintf("Scheduled snapshot of %q failed", title),
			Body:     snap.Error,
			Link:     fmt.Sprintf("/connections/%s/snapshots/%s", snap.ConnectionID, snap.ID),
		})
	}
	if runErr == nil && snap.Trigger == repo.TriggerScheduled {
		if err := m.applyRetention(finishCtx, snap.ConnectionID, snap.BaseID); err != nil {
			logger.Warn("snapshot retention failed", "base_id", snap.BaseID, "err", err)
		}
	}
}

func (m *Manager) capture(ctx context.Context, snap *repo.Snapshot) error {
	conn, err := m.conns.GetByID(ctx, snap.ConnectionID)
	if err != nil {
		return fmt.Errorf("load connection: %w", err)
	}
	api, cfg, err := m.open(conn)
	if err != nil {
		return err
	}
	if snap.BaseTitle == "" {
		if bases, err := api.ListBases(ctx); err == nil {
			for _, b := range bases {
				if b.ID == snap.BaseID {
					snap.BaseTitle = b.Title
				}
			}
		}
	}

	include, err := m.includeAttachments(ctx, snap)
	if err != nil {
		return fmt.Errorf("load backup policy: %w", err)
	}
	var files snapshot.Files
	if include {
		files = &fileStore{m: m, api: api, snap: snap}
	}
	snap.AttachmentsIncluded = include

	tmp, err := os.CreateTemp("", "neobox-snapshot-*.json.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	var (
		progressMu   sync.Mutex
		lastProgress time.Time
	)
	stats, err := snapshot.Build(ctx, api, snapshot.Source{BaseURL: cfg.BaseURL, BaseID: snap.BaseID}, tmp, snapshot.Options{
		PageSize: m.cfg.PageSize,
		Files:    files,
		// Called from concurrent link lookups; throttled because the UI
		// only polls every few seconds.
		Progress: func(msg string) {
			progressMu.Lock()
			if time.Since(lastProgress) < time.Second {
				progressMu.Unlock()
				return
			}
			lastProgress = time.Now()
			progressMu.Unlock()
			_ = m.repo.UpdateSnapshotProgress(ctx, snap.ID, msg)
		},
	})
	if err != nil {
		return err
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_ = m.repo.UpdateSnapshotProgress(ctx, snap.ID, "uploading")
	key := ObjectKey(snap)
	if err := m.blobs.Put(ctx, key, tmp, size, snapshot.ContentType); err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}

	snap.ObjectKey = key
	snap.SizeBytes = size
	snap.RecordCount = stats.RecordCount
	snap.LinkCount = stats.LinkCount
	snap.ViewCount = stats.ViewCount
	snap.HookCount = stats.HookCount
	snap.FileCount = stats.FileCount
	snap.FileBytes = stats.FileBytes
	snap.FilesMissing = stats.FilesMissing
	snap.Tables = make([]repo.SnapshotTable, 0, len(stats.Tables))
	for _, t := range stats.Tables {
		snap.Tables = append(snap.Tables, repo.SnapshotTable{
			ID: t.ID, Title: t.Title, RecordCount: t.RecordCount, FieldCount: t.FieldCount, LinkCount: t.LinkCount,
			ViewCount: t.ViewCount, HookCount: t.HookCount, FileCount: t.FileCount, FileBytes: t.FileBytes, FilesMissing: t.FilesMissing,
		})
	}
	return nil
}

// ObjectKey is where a snapshot's content is stored.
func ObjectKey(s *repo.Snapshot) string {
	return fmt.Sprintf("nocodb/%s/%s/%s/%s.json.gz", s.UserID, s.ConnectionID, s.BaseID, s.ID)
}

// applyRetention deletes the oldest succeeded scheduled snapshots beyond the
// policy's retention count.
func (m *Manager) applyRetention(ctx context.Context, connectionID, baseID string) error {
	conn, err := m.conns.GetByID(ctx, connectionID)
	if err != nil {
		return err
	}
	policies, err := m.repo.ListPolicies(ctx, conn.UserID, connectionID)
	if err != nil {
		return err
	}
	retention := 0
	for _, p := range policies {
		if p.BaseID == baseID {
			retention = p.Retention
		}
	}
	if retention <= 0 {
		return nil
	}
	snaps, err := m.repo.ListSnapshots(ctx, repo.SnapshotFilter{
		ConnectionID: connectionID, BaseID: baseID,
		Trigger: repo.TriggerScheduled, Status: repo.StatusSucceeded,
	})
	if err != nil {
		return err
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].CreatedAt.After(snaps[j].CreatedAt) })
	for _, s := range snaps[min(retention, len(snaps)):] {
		// A snapshot being restored is pruned by a later run.
		if err := m.DeleteSnapshot(ctx, s); err != nil && !errors.Is(err, ErrRestoreInProgress) {
			return err
		}
	}
	return nil
}

// DeleteSnapshot removes a snapshot's content and metadata, and the
// attachment files no other snapshot uses. Refuses while the snapshot is
// still pending or running, or while a restore reads it.
func (m *Manager) DeleteSnapshot(ctx context.Context, s *repo.Snapshot) error {
	if s.Status == repo.StatusPending || s.Status == repo.StatusRunning {
		return ErrSnapshotInProgress
	}
	active, err := m.repo.ListRestores(ctx, repo.RestoreFilter{SnapshotID: s.ID, ActiveOnly: true, Limit: 1})
	if err != nil {
		return err
	}
	if len(active) > 0 {
		return ErrRestoreInProgress
	}
	if s.ObjectKey != "" {
		if err := m.blobs.Delete(ctx, s.ObjectKey); err != nil {
			return err
		}
	}
	if err := m.repo.DeleteSnapshot(ctx, s.ID); err != nil {
		return err
	}
	m.collectFiles(ctx, s.UserID)
	return nil
}

// OpenContent opens a succeeded snapshot's stored content.
func (m *Manager) OpenContent(ctx context.Context, s *repo.Snapshot) (io.ReadCloser, error) {
	if s.Status != repo.StatusSucceeded || s.ObjectKey == "" {
		return nil, errors.New("snapshot has no content")
	}
	return m.blobs.Get(ctx, s.ObjectKey)
}

func policyKey(connectionID, baseID string) string { return connectionID + "/" + baseID }
