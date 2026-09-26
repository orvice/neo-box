package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	wasabirepo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	"go.orx.me/apps/neo-box/internal/wasabi"
	"go.orx.me/apps/neo-box/internal/wasabisync"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	defaultUsageDays = 90
	maxUsageDays     = 800
)

// Syncer is what the RPCs need from the sync manager (*wasabisync.Manager).
type Syncer interface {
	Syncing(connectionID string) bool
	Refresh(connectionID string)
}

// WasabiServiceServer implements neoboxv1connect.WasabiServiceHandler.
// Dependencies are attached after bootstrap via SetDeps; until then every
// RPC fails with FailedPrecondition.
type WasabiServiceServer struct {
	mu    sync.RWMutex
	repo  wasabirepo.Repository
	sync  Syncer
	conns *connection.Service
	now   func() time.Time
}

func NewWasabiServiceServer() *WasabiServiceServer {
	return &WasabiServiceServer{now: time.Now}
}

func (s *WasabiServiceServer) SetDeps(r wasabirepo.Repository, m Syncer, conns *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repo, s.sync, s.conns = r, m, conns
}

type wasabiDeps struct {
	repo  wasabirepo.Repository
	sync  Syncer
	conns *connection.Service
}

func (s *WasabiServiceServer) deps(ctx context.Context) (string, *wasabiDeps, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.repo == nil || s.sync == nil || s.conns == nil {
		return "", nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("wasabi usage is not available"))
	}
	return user.GetId(), &wasabiDeps{repo: s.repo, sync: s.sync, conns: s.conns}, nil
}

// load returns the caller's Wasabi connection and its config; connections
// of other providers are reported as not found.
func (d *wasabiDeps) load(ctx context.Context, userID, id string) (*connrepo.Connection, wasabi.ConnectionConfig, error) {
	var cfg wasabi.ConnectionConfig
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, cfg, connectx.RequiredArgument("connection_id")
	}
	c, err := d.conns.Get(ctx, userID, id)
	if err != nil {
		return nil, cfg, mapConnectionErr(err)
	}
	if c.Provider != wasabi.ProviderType {
		return nil, cfg, connectx.NotFound("connection not found")
	}
	if err := d.conns.Open(c, &cfg, nil); err != nil {
		return nil, cfg, connectx.InternalWith(err)
	}
	return c, cfg, nil
}

func (s *WasabiServiceServer) GetWasabiOverview(ctx context.Context, req *connect.Request[neoboxv1.GetWasabiOverviewRequest]) (*connect.Response[neoboxv1.GetWasabiOverviewResponse], error) {
	userID, d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, cfg, err := d.load(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	today := wasabi.DayOf(s.now())
	resp := &neoboxv1.GetWasabiOverviewResponse{}

	latest, err := d.repo.LatestAccountUsage(ctx, conn.ID)
	switch {
	case err == nil:
		resp.Latest = usageToProto(*latest)
	case !errors.Is(err, wasabirepo.ErrNotFound):
		return nil, connectx.InternalWith(err)
	}

	if cfg.CostEstimateEnabled {
		anchor, err := cfg.Anchor()
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
		// Two cycles back covers the current cycle or the rolling 30 days.
		days, err := d.repo.ListUsage(ctx, conn.ID, "", today.AddDate(0, 0, -2*wasabi.CycleDays), today)
		if err != nil {
			return nil, connectx.InternalWith(err)
		}
		resp.CostEstimate = estimateToProto(wasabi.EstimateCost(days, cfg.Price(), anchor, today), cfg.Price())
	}

	if resp.Sync, err = syncStateToProto(ctx, d, conn.ID, today); err != nil {
		return nil, err
	}
	buckets, err := d.repo.LatestBucketUsage(ctx, conn.ID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	newest := newestDay(latest, buckets)
	for _, b := range buckets {
		if b.Day.Equal(newest) {
			resp.BucketCount++
		}
	}
	return connect.NewResponse(resp), nil
}

func (s *WasabiServiceServer) ListWasabiBuckets(ctx context.Context, req *connect.Request[neoboxv1.ListWasabiBucketsRequest]) (*connect.Response[neoboxv1.ListWasabiBucketsResponse], error) {
	userID, d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, _, err := d.load(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	buckets, err := d.repo.LatestBucketUsage(ctx, conn.ID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	latest, err := d.repo.LatestAccountUsage(ctx, conn.ID)
	if err != nil && !errors.Is(err, wasabirepo.ErrNotFound) {
		return nil, connectx.InternalWith(err)
	}
	newest := newestDay(latest, buckets)
	out := make([]*neoboxv1.WasabiBucket, 0, len(buckets))
	for _, b := range buckets {
		deleted := b.Day.Before(newest)
		if deleted && !req.Msg.GetIncludeDeleted() {
			continue
		}
		out = append(out, &neoboxv1.WasabiBucket{Name: b.Bucket, Region: b.Region, Deleted: deleted, Latest: usageToProto(b)})
	}
	return connect.NewResponse(&neoboxv1.ListWasabiBucketsResponse{Buckets: out}), nil
}

func (s *WasabiServiceServer) GetWasabiUsage(ctx context.Context, req *connect.Request[neoboxv1.GetWasabiUsageRequest]) (*connect.Response[neoboxv1.GetWasabiUsageResponse], error) {
	userID, d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, _, err := d.load(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	to := wasabi.DayOf(s.now())
	if v := req.Msg.GetTo(); v != "" {
		if to, err = time.Parse(wasabi.DateLayout, v); err != nil {
			return nil, connectx.InvalidArgument("to", "must be a YYYY-MM-DD day")
		}
	}
	from := to.AddDate(0, 0, -(defaultUsageDays - 1))
	if v := req.Msg.GetFrom(); v != "" {
		if from, err = time.Parse(wasabi.DateLayout, v); err != nil {
			return nil, connectx.InvalidArgument("from", "must be a YYYY-MM-DD day")
		}
	}
	if from.After(to) {
		return nil, connectx.InvalidArgument("from", "must not be after to")
	}
	if to.Sub(from) > maxUsageDays*24*time.Hour {
		return nil, connectx.InvalidArgument("from", "range must be at most 800 days")
	}
	days, err := d.repo.ListUsage(ctx, conn.ID, req.Msg.GetBucket(), from, to)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.WasabiUsage, 0, len(days))
	for _, u := range days {
		out = append(out, usageToProto(u))
	}
	return connect.NewResponse(&neoboxv1.GetWasabiUsageResponse{Days: out}), nil
}

func (s *WasabiServiceServer) SyncWasabiConnection(ctx context.Context, req *connect.Request[neoboxv1.SyncWasabiConnectionRequest]) (*connect.Response[neoboxv1.SyncWasabiConnectionResponse], error) {
	userID, d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, _, err := d.load(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	d.sync.Refresh(conn.ID)
	state, err := syncStateToProto(ctx, d, conn.ID, wasabi.DayOf(s.now()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.SyncWasabiConnectionResponse{Sync: state}), nil
}

// --- conversions ---

// newestDay is the newest day with data: the account's, or failing that
// the newest bucket row's.
func newestDay(account *wasabi.Usage, buckets []wasabi.Usage) time.Time {
	var newest time.Time
	if account != nil {
		newest = account.Day
	}
	for _, b := range buckets {
		if b.Day.After(newest) {
			newest = b.Day
		}
	}
	return newest
}

func syncStateToProto(ctx context.Context, d *wasabiDeps, connectionID string, today time.Time) (*neoboxv1.WasabiSyncState, error) {
	out := &neoboxv1.WasabiSyncState{Syncing: d.sync.Syncing(connectionID)}
	st, err := d.repo.GetSyncState(ctx, connectionID)
	if errors.Is(err, wasabirepo.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	if !st.LastSuccessAt.IsZero() {
		out.LastSuccessAt = timestamppb.New(st.LastSuccessAt)
	}
	out.LastSyncedDay = formatDay(st.LastSyncedDay)
	out.BackfillComplete = !st.BackfillCompletedAt.IsZero()
	out.BackfillProgress = wasabisync.BackfillProgress(st, today)
	return out, nil
}

func estimateToProto(e wasabi.Estimate, price float64) *neoboxv1.WasabiCostEstimate {
	return &neoboxv1.WasabiCostEstimate{
		PeriodStart: formatDay(e.PeriodStart), PeriodEnd: formatDay(e.PeriodEnd), Rolling: e.Rolling,
		DataThrough: formatDay(e.DataThrough), DaysWithData: int32(e.DaysWithData),
		CostToDate: e.CostToDate, ProjectedCost: e.ProjectedCost, PricePerTbMonth: price,
		EgressBytes: e.EgressBytes, EgressExceedsStorage: e.EgressExceedsStorage,
	}
}

func usageToProto(u wasabi.Usage) *neoboxv1.WasabiUsage {
	return &neoboxv1.WasabiUsage{
		Day: formatDay(u.Day), Bucket: u.Bucket, Region: u.Region,
		ActiveStorageBytes: u.ActiveStorageBytes(), DeletedStorageBytes: u.DeletedStorageSizeBytes,
		BillableObjects: u.NumBillableObjects, BillableDeletedObjects: u.NumBillableDeletedObjects,
		RawStorageBytes: u.RawStorageSizeBytes, PaddedStorageBytes: u.PaddedStorageSizeBytes,
		MetadataStorageBytes: u.MetadataStorageSizeBytes, OrphanedStorageBytes: u.OrphanedStorageSizeBytes,
		MinStorageChargeBytes: u.MinStorageChargeBytes, ApiCalls: u.NumAPICalls,
		UploadBytes: u.UploadBytes, DownloadBytes: u.DownloadBytes,
		StorageWroteBytes: u.StorageWroteBytes, StorageReadBytes: u.StorageReadBytes, DeleteBytes: u.DeleteBytes,
		GetCalls: u.NumGETCalls, PutCalls: u.NumPUTCalls, DeleteCalls: u.NumDELETECalls,
		ListCalls: u.NumLISTCalls, HeadCalls: u.NumHEADCalls,
	}
}

func formatDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(wasabi.DateLayout)
}
