// Package wasabisync keeps the daily usage of Wasabi connections in
// PostgreSQL. It backfills 12 months when a connection is added, pulls new
// days every day at 02:30 UTC (Wasabi publishes the previous day around
// 01:30), and re-fetches the last week on demand. One sync runs at a time.
// The package also implements the "wasabi" connection provider.
//
// A failed sync is not retried the same day: the next run starts from the
// last synced day, so it catches up. Startup queues connections whose last
// success is over a day old or whose backfill never finished. The scheduler
// assumes a single neo-box process.
package wasabisync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/robfig/cron/v3"

	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// Stats is the Stats API surface a sync needs (*wasabi.Client).
type Stats interface {
	AccountUsage(ctx context.Context, from, to time.Time) ([]wasabi.Usage, error)
	BucketUsage(ctx context.Context, from, to time.Time) ([]wasabi.Usage, error)
}

// Connections is what the manager needs from the connection service
// (*connection.Service).
type Connections interface {
	List(ctx context.Context, f connrepo.Filter) ([]*connrepo.Connection, error)
	GetByID(ctx context.Context, id string) (*connrepo.Connection, error)
	Open(c *connrepo.Connection, config, secret any) error
	RecordStatus(ctx context.Context, id string, err error)
}

type Config struct {
	// Endpoint is the Stats API host; empty means wasabi.DefaultEndpoint.
	Endpoint string
	// Schedule is the daily run's cron expression.
	Schedule string
	// BackfillDays is how far back a new connection is filled.
	BackfillDays int
	// RefreshDays is how many recent days "sync now" re-fetches.
	RefreshDays int
	// SyncTimeout bounds one sync, backfill included.
	SyncTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Schedule == "" {
		c.Schedule = "CRON_TZ=UTC 30 2 * * *"
	}
	if c.BackfillDays <= 0 {
		c.BackfillDays = 365
	}
	if c.RefreshDays <= 0 {
		c.RefreshDays = 7
	}
	if c.SyncTimeout <= 0 {
		c.SyncTimeout = 30 * time.Minute
	}
	return c
}

// chunkDays is the span of one backfill request; progress is saved after
// each chunk.
const chunkDays = 30

type job struct {
	connectionID string
	refresh      bool
}

type Manager struct {
	cfg   Config
	repo  repo.Repository
	conns Connections

	queue chan job
	wg    sync.WaitGroup

	mu        sync.Mutex
	pending   map[string]bool // queued or running
	running   string
	cancelled map[string]bool // deleted while queued

	// newClient and now are swapped in tests.
	newClient func(accessKey, secretKey string) (Stats, error)
	now       func() time.Time
}

func New(cfg Config, r repo.Repository, conns Connections) *Manager {
	cfg = cfg.withDefaults()
	return &Manager{
		cfg:       cfg,
		repo:      r,
		conns:     conns,
		queue:     make(chan job, 1024),
		pending:   make(map[string]bool),
		cancelled: make(map[string]bool),
		newClient: func(accessKey, secretKey string) (Stats, error) {
			return wasabi.New(accessKey, secretKey, wasabi.WithEndpoint(cfg.Endpoint))
		},
		now: time.Now,
	}
}

// Start launches the worker and the daily schedule, and queues connections
// that missed a run. Both stop when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	c := cron.New()
	if _, err := c.AddFunc(m.cfg.Schedule, func() { m.queueAll(ctx) }); err != nil {
		return fmt.Errorf("wasabi sync schedule %q: %w", m.cfg.Schedule, err)
	}
	m.wg.Add(1)
	go m.worker(ctx)
	c.Start()
	go func() {
		<-ctx.Done()
		<-c.Stop().Done()
	}()
	m.catchUp(ctx)
	return nil
}

// Wait blocks until the worker exits (after the Start context is cancelled).
func (m *Manager) Wait() { m.wg.Wait() }

// Syncing reports whether the connection has a sync queued or running.
func (m *Manager) Syncing(connectionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[connectionID]
}

// Refresh queues a re-fetch of the last RefreshDays days (a backfill first,
// if it never finished). It does nothing while a sync is queued or running.
func (m *Manager) Refresh(connectionID string) {
	m.enqueue(job{connectionID: connectionID, refresh: true})
}

// BackfillProgress is the share of the backfill done, from 0 to 1.
func BackfillProgress(st *repo.SyncState, today time.Time) float64 {
	switch {
	case st == nil || st.BackfillFrom.IsZero():
		return 0
	case !st.BackfillCompletedAt.IsZero():
		return 1
	case st.BackfillThrough.IsZero():
		return 0
	}
	total := wasabi.DayOf(today).Sub(st.BackfillFrom).Hours()
	if total <= 0 {
		return 1
	}
	return min(st.BackfillThrough.Sub(st.BackfillFrom).Hours()/total, 1)
}

func (m *Manager) enqueue(j job) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending[j.connectionID] {
		return false
	}
	select {
	case m.queue <- j:
		m.pending[j.connectionID] = true
		delete(m.cancelled, j.connectionID)
		return true
	default:
		log.FromContext(context.Background()).Warn("wasabi sync queue is full; skipping", "connection_id", j.connectionID)
		return false
	}
}

func (m *Manager) worker(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.queue:
			m.mu.Lock()
			if m.cancelled[j.connectionID] {
				delete(m.cancelled, j.connectionID)
				delete(m.pending, j.connectionID)
				m.mu.Unlock()
				continue
			}
			m.running = j.connectionID
			m.mu.Unlock()

			m.run(ctx, j)

			m.mu.Lock()
			m.running = ""
			delete(m.pending, j.connectionID)
			m.mu.Unlock()
		}
	}
}

// forget drops a connection that is being deleted. It fails with busy=true
// while the connection's sync is running.
func (m *Manager) forget(connectionID string) (busy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running == connectionID {
		return true
	}
	if m.pending[connectionID] {
		m.cancelled[connectionID] = true
	}
	return false
}

func (m *Manager) queueAll(ctx context.Context) {
	conns, err := m.conns.List(ctx, connrepo.Filter{Provider: wasabi.ProviderType})
	if err != nil {
		log.FromContext(ctx).Error("list wasabi connections failed", "err", err)
		return
	}
	for _, c := range conns {
		m.enqueue(job{connectionID: c.ID})
	}
}

// catchUp queues connections whose backfill is unfinished or whose last
// success is more than a day old, e.g. because the server was down at
// 02:30.
func (m *Manager) catchUp(ctx context.Context) {
	logger := log.FromContext(ctx)
	conns, err := m.conns.List(ctx, connrepo.Filter{Provider: wasabi.ProviderType})
	if err != nil {
		logger.Error("list wasabi connections failed", "err", err)
		return
	}
	queued := 0
	for _, c := range conns {
		st, err := m.repo.GetSyncState(ctx, c.ID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			logger.Warn("load wasabi sync state failed", "connection_id", c.ID, "err", err)
			continue
		}
		if st == nil || st.BackfillCompletedAt.IsZero() || m.now().Sub(st.LastSuccessAt) > 24*time.Hour {
			if m.enqueue(job{connectionID: c.ID}) {
				queued++
			}
		}
	}
	if queued > 0 {
		logger.Info("queued wasabi syncs that missed a run", "count", queued)
	}
}

func (m *Manager) run(parent context.Context, j job) {
	logger := log.FromContext(parent)
	ctx, cancel := context.WithTimeout(parent, m.cfg.SyncTimeout)
	defer cancel()

	conn, err := m.conns.GetByID(ctx, j.connectionID)
	if err != nil {
		if !errors.Is(err, connrepo.ErrNotFound) {
			logger.Error("wasabi sync: load connection failed", "connection_id", j.connectionID, "err", err)
		}
		return
	}
	if conn.Provider != wasabi.ProviderType {
		return
	}
	api, err := m.client(conn)
	if err == nil {
		err = m.sync(ctx, conn.ID, api, j.refresh)
	}
	// Record the outcome even if the sync ran out of time.
	m.conns.RecordStatus(context.WithoutCancel(ctx), conn.ID, err)
	if err != nil {
		logger.Warn("wasabi sync failed", "connection_id", conn.ID, "err", err)
		return
	}
	logger.Info("wasabi sync succeeded", "connection_id", conn.ID, "refresh", j.refresh)
}

func (m *Manager) client(conn *connrepo.Connection) (Stats, error) {
	var (
		cfg wasabi.ConnectionConfig
		sec wasabi.ConnectionSecret
	)
	if err := m.conns.Open(conn, &cfg, &sec); err != nil {
		return nil, err
	}
	return m.newClient(cfg.AccessKeyID, sec.SecretKey)
}

// sync backfills (resuming where a previous run stopped) or pulls the days
// since the last synced one.
func (m *Manager) sync(ctx context.Context, connectionID string, api Stats, refresh bool) error {
	today := wasabi.DayOf(m.now())
	st, err := m.repo.GetSyncState(ctx, connectionID)
	if errors.Is(err, repo.ErrNotFound) {
		st, err = &repo.SyncState{ConnectionID: connectionID}, nil
	}
	if err != nil {
		return err
	}
	save := func() error {
		st.UpdatedAt = m.now().UTC()
		return m.repo.SaveSyncState(ctx, st)
	}

	if st.BackfillCompletedAt.IsZero() {
		if st.BackfillFrom.IsZero() {
			st.BackfillFrom = today.AddDate(0, 0, -m.cfg.BackfillDays)
		}
		from := st.BackfillFrom
		if !st.BackfillThrough.IsZero() {
			from = st.BackfillThrough.AddDate(0, 0, 1)
		}
		for !from.After(today) {
			to := from.AddDate(0, 0, chunkDays-1)
			if to.After(today) {
				to = today
			}
			newest, err := m.fetch(ctx, api, connectionID, from, to)
			if err != nil {
				return err
			}
			st.LastSyncedDay = later(st.LastSyncedDay, newest)
			st.BackfillThrough = to
			if err := save(); err != nil {
				return err
			}
			from = to.AddDate(0, 0, 1)
		}
		st.BackfillCompletedAt = m.now().UTC()
	} else {
		from := st.LastSyncedDay
		if recent := today.AddDate(0, 0, -(m.cfg.RefreshDays - 1)); from.IsZero() || (refresh && recent.Before(from)) {
			from = recent
		}
		newest, err := m.fetch(ctx, api, connectionID, from, today)
		if err != nil {
			return err
		}
		st.LastSyncedDay = later(st.LastSyncedDay, newest)
	}
	st.LastSuccessAt = m.now().UTC()
	return save()
}

// fetch stores account and bucket usage for from..to and returns the newest
// day with account data.
func (m *Manager) fetch(ctx context.Context, api Stats, connectionID string, from, to time.Time) (time.Time, error) {
	account, err := api.AccountUsage(ctx, from, to)
	if err != nil {
		return time.Time{}, err
	}
	buckets, err := api.BucketUsage(ctx, from, to)
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	rows := make([]wasabi.Usage, 0, len(account)+len(buckets))
	for _, u := range account {
		u.Bucket = "" // account rows are keyed by the empty bucket
		rows = append(rows, u)
		newest = later(newest, u.Day)
	}
	for _, u := range buckets {
		if u.Bucket != "" {
			rows = append(rows, u)
		}
	}
	if err := m.repo.UpsertUsage(ctx, connectionID, rows); err != nil {
		return time.Time{}, err
	}
	return newest, nil
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
