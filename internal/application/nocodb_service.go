package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/snapshot"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	defaultSnapshotListLimit = 50
	maxSnapshotListLimit     = 500
	defaultRecordPageSize    = 50
	maxRecordPageSize        = 500
	tableCacheSize           = 4
)

// NocoDBServiceServer implements neoboxv1connect.NocoDBServiceHandler.
// Dependencies are attached after bootstrap via SetDeps; until then every
// RPC fails with FailedPrecondition.
type NocoDBServiceServer struct {
	mu      sync.RWMutex
	repo    repo.Repository
	manager *backup.Manager
	conns   *connection.Service

	cache *tableCache
}

func NewNocoDBServiceServer() *NocoDBServiceServer {
	return &NocoDBServiceServer{cache: newTableCache(tableCacheSize)}
}

// SetDeps wires the repository, backup manager, and connection service.
func (s *NocoDBServiceServer) SetDeps(r repo.Repository, m *backup.Manager, conns *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repo, s.manager, s.conns = r, m, conns
}

func (s *NocoDBServiceServer) deps(ctx context.Context) (string, repo.Repository, *backup.Manager, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", nil, nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.repo == nil || s.manager == nil || s.conns == nil {
		return "", nil, nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("nocodb backups are not available"))
	}
	return user.GetId(), s.repo, s.manager, nil
}

// --- bases & policies ---

func (s *NocoDBServiceServer) ListNocoDBBases(ctx context.Context, req *connect.Request[neoboxv1.ListNocoDBBasesRequest]) (*connect.Response[neoboxv1.ListNocoDBBasesResponse], error) {
	userID, r, m, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.loadConnection(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	api, err := m.Client(conn)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	bases, err := api.ListBases(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	policies, err := r.ListPolicies(ctx, userID, conn.ID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	byBase := make(map[string]*repo.Policy, len(policies))
	for _, p := range policies {
		byBase[p.BaseID] = p
	}
	out := make([]*neoboxv1.NocoDBBase, 0, len(bases))
	for _, b := range bases {
		item := &neoboxv1.NocoDBBase{Id: b.ID, Title: b.Title}
		if p, ok := byBase[b.ID]; ok {
			item.Policy = policyToProto(p, m.NextRun(p.ConnectionID, p.BaseID))
		}
		latest, err := r.ListSnapshots(ctx, repo.SnapshotFilter{UserID: userID, ConnectionID: conn.ID, BaseID: b.ID, Limit: 1})
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
		if len(latest) > 0 {
			item.LatestSnapshot = snapshotToProto(latest[0])
		}
		out = append(out, item)
	}
	return connect.NewResponse(&neoboxv1.ListNocoDBBasesResponse{Bases: out}), nil
}

func (s *NocoDBServiceServer) UpsertBackupPolicy(ctx context.Context, req *connect.Request[neoboxv1.UpsertBackupPolicyRequest]) (*connect.Response[neoboxv1.UpsertBackupPolicyResponse], error) {
	userID, r, m, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.loadConnection(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	baseID := strings.TrimSpace(req.Msg.GetBaseId())
	if baseID == "" {
		return nil, connectx.RequiredArgument("base_id")
	}
	cronExpr := strings.TrimSpace(req.Msg.GetCron())
	if req.Msg.GetEnabled() || cronExpr != "" {
		if cronExpr == "" {
			return nil, connectx.RequiredArgument("cron")
		}
		if err := backup.ValidateCron(cronExpr); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if req.Msg.GetRetention() < 0 {
		return nil, connectx.InvalidArgument("retention", "must be >= 0")
	}
	p := &repo.Policy{
		ConnectionID: conn.ID, BaseID: baseID, UserID: userID,
		Enabled: req.Msg.GetEnabled(), Cron: cronExpr, Retention: int(req.Msg.GetRetention()),
		UpdatedAt: time.Now().UTC(),
	}
	if err := r.UpsertPolicy(ctx, p); err != nil {
		return nil, connectx.InternalWith(err)
	}
	if err := m.ReloadSchedules(ctx); err != nil {
		return nil, connectx.InternalWith(err)
	}
	return connect.NewResponse(&neoboxv1.UpsertBackupPolicyResponse{Policy: policyToProto(p, m.NextRun(p.ConnectionID, p.BaseID))}), nil
}

// --- snapshots ---

func (s *NocoDBServiceServer) CreateSnapshot(ctx context.Context, req *connect.Request[neoboxv1.CreateSnapshotRequest]) (*connect.Response[neoboxv1.CreateSnapshotResponse], error) {
	userID, _, m, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.loadConnection(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	baseID := strings.TrimSpace(req.Msg.GetBaseId())
	if baseID == "" {
		return nil, connectx.RequiredArgument("base_id")
	}
	snap, err := m.Enqueue(ctx, conn, baseID, "", repo.TriggerManual)
	if err != nil {
		if errors.Is(err, backup.ErrSnapshotInProgress) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}
	return connect.NewResponse(&neoboxv1.CreateSnapshotResponse{Snapshot: snapshotToProto(snap)}), nil
}

func (s *NocoDBServiceServer) ListSnapshots(ctx context.Context, req *connect.Request[neoboxv1.ListSnapshotsRequest]) (*connect.Response[neoboxv1.ListSnapshotsResponse], error) {
	userID, r, _, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = defaultSnapshotListLimit
	}
	limit = min(limit, maxSnapshotListLimit)
	snaps, err := r.ListSnapshots(ctx, repo.SnapshotFilter{
		UserID: userID, ConnectionID: req.Msg.GetConnectionId(), BaseID: req.Msg.GetBaseId(), Limit: limit,
	})
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.Snapshot, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, snapshotToProto(snap))
	}
	return connect.NewResponse(&neoboxv1.ListSnapshotsResponse{Snapshots: out}), nil
}

func (s *NocoDBServiceServer) GetSnapshot(ctx context.Context, req *connect.Request[neoboxv1.GetSnapshotRequest]) (*connect.Response[neoboxv1.GetSnapshotResponse], error) {
	userID, r, _, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := r.GetSnapshot(ctx, userID, req.Msg.GetId())
	if err != nil {
		return nil, mapRepoErr(err, "snapshot")
	}
	return connect.NewResponse(&neoboxv1.GetSnapshotResponse{Snapshot: snapshotToProto(snap)}), nil
}

func (s *NocoDBServiceServer) DeleteSnapshot(ctx context.Context, req *connect.Request[neoboxv1.DeleteSnapshotRequest]) (*connect.Response[neoboxv1.DeleteSnapshotResponse], error) {
	userID, r, m, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := r.GetSnapshot(ctx, userID, req.Msg.GetId())
	if err != nil {
		return nil, mapRepoErr(err, "snapshot")
	}
	if err := m.DeleteSnapshot(ctx, snap); err != nil {
		if errors.Is(err, backup.ErrSnapshotInProgress) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot is still in progress"))
		}
		return nil, connectx.InternalWith(err)
	}
	s.cache.dropSnapshot(snap.ID)
	return connect.NewResponse(&neoboxv1.DeleteSnapshotResponse{}), nil
}

func (s *NocoDBServiceServer) ListSnapshotRecords(ctx context.Context, req *connect.Request[neoboxv1.ListSnapshotRecordsRequest]) (*connect.Response[neoboxv1.ListSnapshotRecordsResponse], error) {
	userID, r, m, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := r.GetSnapshot(ctx, userID, req.Msg.GetSnapshotId())
	if err != nil {
		return nil, mapRepoErr(err, "snapshot")
	}
	if snap.Status != repo.StatusSucceeded {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot has not succeeded"))
	}
	tableID := req.Msg.GetTableId()
	table, err := s.cache.get(snap.ID, tableID, func() (*snapshot.Table, error) {
		rc, err := m.OpenContent(ctx, snap)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		_, t, err := snapshot.ReadTable(rc, tableID)
		return t, err
	})
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	if table == nil {
		return nil, connectx.NotFound("table not found in snapshot")
	}

	page := max(int(req.Msg.GetPage()), 1)
	size := int(req.Msg.GetPageSize())
	if size <= 0 {
		size = defaultRecordPageSize
	}
	size = min(size, maxRecordPageSize)
	start := min((page-1)*size, len(table.Records))
	end := min(start+size, len(table.Records))

	fields := table.Fields()
	resp := &neoboxv1.ListSnapshotRecordsResponse{
		Fields: make([]*neoboxv1.SnapshotField, 0, len(fields)),
		Total:  int64(len(table.Records)),
	}
	for _, f := range fields {
		resp.Fields = append(resp.Fields, &neoboxv1.SnapshotField{Id: f.ID, Title: f.Title, Type: f.Type})
	}
	for _, rec := range table.Records[start:end] {
		st, err := recordToStruct(rec)
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
		resp.Records = append(resp.Records, st)
	}
	return connect.NewResponse(resp), nil
}

// --- helpers ---

// loadConnection returns the caller's NocoDB connection; connections of
// other providers are reported as not found.
func (s *NocoDBServiceServer) loadConnection(ctx context.Context, userID, id string) (*connrepo.Connection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, connectx.RequiredArgument("connection_id")
	}
	s.mu.RLock()
	conns := s.conns
	s.mu.RUnlock()
	conn, err := conns.Get(ctx, userID, id)
	if err != nil {
		if errors.Is(err, connrepo.ErrNotFound) {
			return nil, connectx.NotFound("connection not found")
		}
		return nil, connectx.InternalWith(err)
	}
	if conn.Provider != nocodb.ProviderType {
		return nil, connectx.NotFound("connection not found")
	}
	return conn, nil
}

func mapRepoErr(err error, what string) error {
	if errors.Is(err, repo.ErrNotFound) {
		return connectx.NotFound(what + " not found")
	}
	return connectx.InternalWith(err)
}

func recordToStruct(rec nocodb.Record) (*structpb.Struct, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return structpb.NewStruct(m)
}

func policyToProto(p *repo.Policy, next time.Time) *neoboxv1.BackupPolicy {
	out := &neoboxv1.BackupPolicy{
		ConnectionId: p.ConnectionID, BaseId: p.BaseID, Enabled: p.Enabled, Cron: p.Cron,
		Retention: int32(p.Retention), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	if p.Enabled && !next.IsZero() {
		out.NextRunAt = timestamppb.New(next)
	}
	return out
}

func snapshotToProto(s *repo.Snapshot) *neoboxv1.Snapshot {
	out := &neoboxv1.Snapshot{
		Id: s.ID, ConnectionId: s.ConnectionID, BaseId: s.BaseID, BaseTitle: s.BaseTitle,
		Status: snapshotStatusToProto(s.Status), Trigger: snapshotTriggerToProto(s.Trigger),
		Error: s.Error, Progress: s.Progress, CreatedAt: timestamppb.New(s.CreatedAt),
		SizeBytes: s.SizeBytes, RecordCount: s.RecordCount, LinkCount: s.LinkCount,
	}
	if !s.StartedAt.IsZero() {
		out.StartedAt = timestamppb.New(s.StartedAt)
	}
	if !s.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(s.FinishedAt)
	}
	for _, t := range s.Tables {
		out.Tables = append(out.Tables, &neoboxv1.SnapshotTable{
			Id: t.ID, Title: t.Title, RecordCount: t.RecordCount, FieldCount: int32(t.FieldCount), LinkCount: t.LinkCount,
		})
	}
	return out
}

func snapshotStatusToProto(s repo.SnapshotStatus) neoboxv1.SnapshotStatus {
	switch s {
	case repo.StatusPending:
		return neoboxv1.SnapshotStatus_SNAPSHOT_STATUS_PENDING
	case repo.StatusRunning:
		return neoboxv1.SnapshotStatus_SNAPSHOT_STATUS_RUNNING
	case repo.StatusSucceeded:
		return neoboxv1.SnapshotStatus_SNAPSHOT_STATUS_SUCCEEDED
	case repo.StatusFailed:
		return neoboxv1.SnapshotStatus_SNAPSHOT_STATUS_FAILED
	}
	return neoboxv1.SnapshotStatus_SNAPSHOT_STATUS_UNSPECIFIED
}

func snapshotTriggerToProto(t repo.SnapshotTrigger) neoboxv1.SnapshotTrigger {
	switch t {
	case repo.TriggerManual:
		return neoboxv1.SnapshotTrigger_SNAPSHOT_TRIGGER_MANUAL
	case repo.TriggerScheduled:
		return neoboxv1.SnapshotTrigger_SNAPSHOT_TRIGGER_SCHEDULED
	}
	return neoboxv1.SnapshotTrigger_SNAPSHOT_TRIGGER_UNSPECIFIED
}

// tableCache keeps the most recently browsed snapshot tables in memory so
// paging does not re-download and re-scan the snapshot for every page.
type tableCache struct {
	mu    sync.Mutex
	cap   int
	order []string
	items map[string]*snapshot.Table
}

func newTableCache(capacity int) *tableCache {
	return &tableCache{cap: capacity, items: make(map[string]*snapshot.Table)}
}

func (c *tableCache) get(snapshotID, tableID string, load func() (*snapshot.Table, error)) (*snapshot.Table, error) {
	key := snapshotID + "/" + tableID
	c.mu.Lock()
	if t, ok := c.items[key]; ok {
		c.touch(key)
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	t, err := load()
	if err != nil || t == nil {
		return t, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		c.items[key] = t
		c.order = append(c.order, key)
		for len(c.order) > c.cap {
			delete(c.items, c.order[0])
			c.order = c.order[1:]
		}
	}
	return t, nil
}

func (c *tableCache) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(append(c.order[:i:i], c.order[i+1:]...), key)
			return
		}
	}
}

func (c *tableCache) dropSnapshot(snapshotID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := snapshotID + "/"
	kept := c.order[:0]
	for _, k := range c.order {
		if strings.HasPrefix(k, prefix) {
			delete(c.items, k)
			continue
		}
		kept = append(kept, k)
	}
	c.order = kept
}
