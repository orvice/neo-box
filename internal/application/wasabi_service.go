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
		resp.CostEstimate = estimateToProto(wasabi.EstimateCost(days, cfg.Price(), anchor, today), cfg)
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
	configs, err := d.repo.ListBucketConfigs(ctx, conn.ID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	byName := make(map[string]wasabi.BucketConfig, len(configs))
	for _, c := range configs {
		byName[c.Bucket] = c
	}
	newest := newestDay(latest, buckets)
	out := make([]*neoboxv1.WasabiBucket, 0, len(buckets))
	for _, b := range buckets {
		deleted := b.Day.Before(newest)
		if deleted && !req.Msg.GetIncludeDeleted() {
			continue
		}
		wb := &neoboxv1.WasabiBucket{Name: b.Bucket, Region: b.Region, Deleted: deleted, Latest: usageToProto(b)}
		if c, ok := byName[b.Bucket]; ok {
			wb.Config = bucketConfigToProto(c)
		}
		out = append(out, wb)
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

func (s *WasabiServiceServer) GetWasabiCostBreakdown(ctx context.Context, req *connect.Request[neoboxv1.GetWasabiCostBreakdownRequest]) (*connect.Response[neoboxv1.GetWasabiCostBreakdownResponse], error) {
	userID, d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	conn, cfg, err := d.load(ctx, userID, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	anchor, err := cfg.Anchor()
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	today := wasabi.DayOf(s.now())
	// Two cycles back covers the current cycle or the rolling 30 days.
	from := today.AddDate(0, 0, -2*wasabi.CycleDays)
	account, err := d.repo.ListUsage(ctx, conn.ID, "", from, today)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	buckets, err := d.repo.ListBucketUsage(ctx, conn.ID, from, today)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	b := wasabi.CostBreakdown(account, buckets, cfg.Price(), anchor, today)
	return connect.NewResponse(&neoboxv1.GetWasabiCostBreakdownResponse{Breakdown: breakdownToProto(b, cfg.Price())}), nil
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
	if !st.ConfigFetchedAt.IsZero() {
		out.ConfigFetchedAt = timestamppb.New(st.ConfigFetchedAt)
	}
	out.ConfigError = st.ConfigError
	return out, nil
}

func estimateToProto(e wasabi.Estimate, cfg wasabi.ConnectionConfig) *neoboxv1.WasabiCostEstimate {
	return &neoboxv1.WasabiCostEstimate{
		PeriodStart: formatDay(e.PeriodStart), PeriodEnd: formatDay(e.PeriodEnd), Rolling: e.Rolling,
		DataThrough: formatDay(e.DataThrough), DaysWithData: int32(e.DaysWithData),
		CostToDate: e.CostToDate, ProjectedCost: e.ProjectedCost, PricePerTbMonth: cfg.Price(),
		EgressBytes: e.EgressBytes, EgressExceedsStorage: e.EgressExceedsStorage,
		TrendFitted: e.TrendFitted, TrendProjectedCost: e.TrendProjectedCost, NextCycleCost: e.NextCycleCost,
		BudgetUsd: cfg.BudgetUSD,
	}
}

func breakdownToProto(b wasabi.Breakdown, price float64) *neoboxv1.WasabiCostBreakdown {
	out := &neoboxv1.WasabiCostBreakdown{
		PeriodStart: formatDay(b.PeriodStart), PeriodEnd: formatDay(b.PeriodEnd), Rolling: b.Rolling,
		DataThrough: formatDay(b.DataThrough), DaysWithData: int32(b.DaysWithData), PricePerTbMonth: price,
		ActiveCost: b.ActiveCost, DeletedCost: b.DeletedCost, MinimumCost: b.MinimumCost,
		UnattributedCost: b.Unattributed,
	}
	for _, c := range b.Buckets {
		out.Buckets = append(out.Buckets, &neoboxv1.WasabiBucketCost{
			Bucket: c.Bucket, Region: c.Region, Deleted: c.Gone,
			ActiveCost: c.ActiveCost, DeletedCost: c.DeletedCost,
			ActiveStorageBytes: c.ActiveBytes, DeletedStorageBytes: c.DeletedBytes,
			DeletedInPeriodBytes: c.DeletedInPeriodBytes,
		})
	}
	return out
}

func bucketConfigToProto(c wasabi.BucketConfig) *neoboxv1.WasabiBucketConfig {
	out := &neoboxv1.WasabiBucketConfig{
		Region: c.Region, Tags: c.Tags, Errors: c.Errors, Public: c.Public(),
	}
	if !c.FetchedAt.IsZero() {
		out.FetchedAt = timestamppb.New(c.FetchedAt)
	}
	if !c.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(c.CreatedAt)
	}
	if v := c.Versioning; v != nil {
		out.Versioning = &neoboxv1.WasabiVersioning{Status: v.Status, MfaDelete: v.MFADelete}
	}
	if ol := c.ObjectLock; ol != nil && ol.Enabled {
		out.ObjectLock = &neoboxv1.WasabiObjectLock{Mode: ol.Mode, Days: int32(ol.Days), Years: int32(ol.Years)}
	}
	if cp := c.Compliance; cp != nil && cp.Enabled {
		out.Compliance = &neoboxv1.WasabiCompliance{
			RetentionDays: int32(cp.RetentionDays), ConditionalHold: cp.ConditionalHold,
			DeleteAfterRetention: cp.DeleteAfterRetention, Locked: cp.Locked,
		}
		if !cp.LockTime.IsZero() {
			out.Compliance.LockTime = timestamppb.New(cp.LockTime)
		}
	}
	for _, r := range c.Lifecycle {
		out.LifecycleRules = append(out.LifecycleRules, &neoboxv1.WasabiLifecycleRule{
			Id: r.ID, Enabled: r.Enabled, Prefix: r.Prefix, ExpirationDays: int32(r.ExpirationDays),
			ExpiredObjectDeleteMarker: r.ExpiredObjectDeleteMarker, NoncurrentDays: int32(r.NoncurrentDays),
			AbortMultipartDays: int32(r.AbortMultipartDays),
		})
	}
	if p := c.Policy; p != nil {
		out.Policy = &neoboxv1.WasabiBucketPolicy{Document: p.Document, Public: p.Public}
	}
	if a := c.ACL; a != nil {
		out.Acl = &neoboxv1.WasabiBucketAcl{Owner: a.Owner, Public: a.Public}
		for _, g := range a.Grants {
			out.Acl.Grants = append(out.Acl.Grants, &neoboxv1.WasabiAclGrant{Grantee: g.Grantee, Permission: g.Permission})
		}
	}
	if l := c.Logging; l != nil {
		out.Logging = &neoboxv1.WasabiBucketLogging{TargetBucket: l.TargetBucket, TargetPrefix: l.TargetPrefix}
	}
	for _, r := range c.Replication {
		out.ReplicationRules = append(out.ReplicationRules, &neoboxv1.WasabiReplicationRule{
			Id: r.ID, Enabled: r.Enabled, Prefix: r.Prefix, DestinationBucket: r.DestinationBucket,
		})
	}
	for _, f := range wasabi.BucketFindings(c) {
		out.Findings = append(out.Findings, &neoboxv1.WasabiBucketFinding{
			Code: f.Code, Severity: findingSeverityToProto(f.Severity), Message: f.Message,
		})
	}
	return out
}

func findingSeverityToProto(s string) neoboxv1.WasabiFindingSeverity {
	switch s {
	case wasabi.FindingInfo:
		return neoboxv1.WasabiFindingSeverity_WASABI_FINDING_SEVERITY_INFO
	case wasabi.FindingWarning:
		return neoboxv1.WasabiFindingSeverity_WASABI_FINDING_SEVERITY_WARNING
	case wasabi.FindingCritical:
		return neoboxv1.WasabiFindingSeverity_WASABI_FINDING_SEVERITY_CRITICAL
	}
	return neoboxv1.WasabiFindingSeverity_WASABI_FINDING_SEVERITY_UNSPECIFIED
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
