package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// Page sizes: Cloudflare pages Zones by 5..50 and DNS records by up to
// millions; Workers, Pages projects and the operation log are paged here.
const (
	defaultZonePageSize   = 20
	minZonePageSize       = 5
	maxZonePageSize       = 50
	defaultRecordListSize = 50
	minRecordListSize     = 5
	maxRecordListSize     = 500
	defaultListSize       = 20
	maxListSize           = 50
	defaultOperationsSize = 20
	maxOperationsSize     = 100
)

// CloudflareServiceServer implements neoboxv1connect.CloudflareServiceHandler.
// Dependencies are attached after bootstrap via SetDeps; until then every
// RPC fails with FailedPrecondition.
type CloudflareServiceServer struct {
	mu      sync.RWMutex
	manager *cloudflareacct.Manager
	repo    cfrepo.Repository
	conns   *connection.Service
}

func NewCloudflareServiceServer() *CloudflareServiceServer {
	return &CloudflareServiceServer{}
}

func (s *CloudflareServiceServer) SetDeps(m *cloudflareacct.Manager, r cfrepo.Repository, conns *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manager, s.repo, s.conns = m, r, conns
}

type cloudflareDeps struct {
	manager *cloudflareacct.Manager
	repo    cfrepo.Repository
	conns   *connection.Service
	user    cloudflareacct.Actor
}

func (s *CloudflareServiceServer) deps(ctx context.Context) (*cloudflareDeps, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.manager == nil || s.repo == nil || s.conns == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cloudflare is not available"))
	}
	name := user.GetDisplayName()
	if name == "" {
		name = user.GetUsername()
	}
	return &cloudflareDeps{manager: s.manager, repo: s.repo, conns: s.conns, user: cloudflareacct.Actor{ID: user.GetId(), Name: name}}, nil
}

// load returns the caller's Cloudflare connection; connections of other
// users or providers are reported as not found.
func (d *cloudflareDeps) load(ctx context.Context, connectionID string) (*connrepo.Connection, error) {
	id := strings.TrimSpace(connectionID)
	if id == "" {
		return nil, connectx.RequiredArgument("connection_id")
	}
	c, err := d.conns.Get(ctx, d.user.ID, id)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	if c.Provider != cloudflare.ProviderType {
		return nil, connectx.NotFound("connection not found")
	}
	return c, nil
}

// open is load plus the connection's Account and token.
func (d *cloudflareDeps) open(ctx context.Context, connectionID string) (*cloudflareacct.Account, error) {
	c, err := d.load(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	a, err := d.manager.Open(c)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	return a, nil
}

// --- zones ---

func (s *CloudflareServiceServer) ListCloudflareZones(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareZonesRequest]) (*connect.Response[neoboxv1.ListCloudflareZonesResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultZonePageSize, minZonePageSize, maxZonePageSize)
	zones, info, err := a.Zones(ctx, strings.TrimSpace(req.Msg.GetQuery()), page, size)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := make([]*neoboxv1.CloudflareZone, 0, len(zones))
	for i := range zones {
		out = append(out, zoneToProto(&zones[i]))
	}
	return connect.NewResponse(&neoboxv1.ListCloudflareZonesResponse{Zones: out, PageInfo: cfPageInfoToProto(info, page, size)}), nil
}

func (s *CloudflareServiceServer) GetCloudflareZone(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareZoneRequest]) (*connect.Response[neoboxv1.GetCloudflareZoneResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	z, err := a.Zone(ctx, strings.TrimSpace(req.Msg.GetZoneId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.GetCloudflareZoneResponse{Zone: zoneToProto(z)}), nil
}

// --- DNS records ---

var recordTypePattern = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

func (s *CloudflareServiceServer) ListCloudflareDNSRecords(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareDNSRecordsRequest]) (*connect.Response[neoboxv1.ListCloudflareDNSRecordsResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	typ := strings.ToUpper(strings.TrimSpace(req.Msg.GetType()))
	if typ != "" && !recordTypePattern.MatchString(typ) {
		return nil, connectx.InvalidArgument("type", "must be a DNS record type")
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultRecordListSize, minRecordListSize, maxRecordListSize)
	records, info, err := a.Records(ctx, strings.TrimSpace(req.Msg.GetZoneId()), cloudflare.RecordQuery{
		Search: strings.TrimSpace(req.Msg.GetQuery()), Type: typ, Page: page, PerPage: size,
	})
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := make([]*neoboxv1.CloudflareDNSRecord, 0, len(records))
	for i := range records {
		out = append(out, recordToProto(&records[i]))
	}
	return connect.NewResponse(&neoboxv1.ListCloudflareDNSRecordsResponse{Records: out, PageInfo: cfPageInfoToProto(info, page, size)}), nil
}

func (s *CloudflareServiceServer) CreateCloudflareDNSRecord(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareDNSRecordRequest]) (*connect.Response[neoboxv1.CreateCloudflareDNSRecordResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetRecord() == nil {
		return nil, connectx.RequiredArgument("record")
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	ch, err := a.CreateRecord(ctx, d.user, strings.TrimSpace(req.Msg.GetZoneId()), recordInput(req.Msg.GetRecord()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareDNSRecordResponse{
		Record: recordToProto(ch.Record), Operation: operationToProto(ch.Operation), OperationLogError: logErrText(ch),
	}), nil
}

func (s *CloudflareServiceServer) UpdateCloudflareDNSRecord(ctx context.Context, req *connect.Request[neoboxv1.UpdateCloudflareDNSRecordRequest]) (*connect.Response[neoboxv1.UpdateCloudflareDNSRecordResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetRecord() == nil {
		return nil, connectx.RequiredArgument("record")
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	ch, err := a.UpdateRecord(ctx, d.user, strings.TrimSpace(req.Msg.GetZoneId()), strings.TrimSpace(req.Msg.GetRecordId()), recordInput(req.Msg.GetRecord()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateCloudflareDNSRecordResponse{
		Record: recordToProto(ch.Record), Operation: operationToProto(ch.Operation), OperationLogError: logErrText(ch),
	}), nil
}

func (s *CloudflareServiceServer) SetCloudflareDNSRecordProxied(ctx context.Context, req *connect.Request[neoboxv1.SetCloudflareDNSRecordProxiedRequest]) (*connect.Response[neoboxv1.SetCloudflareDNSRecordProxiedResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	ch, err := a.SetProxied(ctx, d.user, strings.TrimSpace(req.Msg.GetZoneId()), strings.TrimSpace(req.Msg.GetRecordId()), req.Msg.GetProxied())
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.SetCloudflareDNSRecordProxiedResponse{
		Record: recordToProto(ch.Record), Operation: operationToProto(ch.Operation), OperationLogError: logErrText(ch),
	}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareDNSRecord(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareDNSRecordRequest]) (*connect.Response[neoboxv1.DeleteCloudflareDNSRecordResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	ch, err := a.DeleteRecord(ctx, d.user, strings.TrimSpace(req.Msg.GetZoneId()), strings.TrimSpace(req.Msg.GetRecordId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareDNSRecordResponse{
		Operation: operationToProto(ch.Operation), OperationLogError: logErrText(ch),
	}), nil
}

func (s *CloudflareServiceServer) ListCloudflareDNSOperations(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareDNSOperationsRequest]) (*connect.Response[neoboxv1.ListCloudflareDNSOperationsResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := d.load(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultOperationsSize, 1, maxOperationsSize)
	ops, total, err := d.repo.ListDNSOperations(ctx, cfrepo.Filter{
		UserID: d.user.ID, ConnectionID: c.ID, ZoneID: strings.TrimSpace(req.Msg.GetZoneId()),
		Offset: (page - 1) * size, Limit: size,
	})
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.CloudflareDNSOperation, 0, len(ops))
	for _, op := range ops {
		out = append(out, operationToProto(op))
	}
	return connect.NewResponse(&neoboxv1.ListCloudflareDNSOperationsResponse{Operations: out, PageInfo: pageInfoOf(page, size, total)}), nil
}

func logErrText(ch *cloudflareacct.Change) string {
	if ch.LogErr == nil {
		return ""
	}
	return "the change was made, but its outcome could not be added to the operation log, which shows it as pending: " + ch.LogErr.Error()
}

func recordInput(in *neoboxv1.CloudflareDNSRecordInput) cloudflare.RecordInput {
	return cloudflare.RecordInput{
		Type: in.GetType(), Name: in.GetName(), Content: in.GetContent(), TTL: int(in.GetTtl()),
		Proxied: in.GetProxied(), Priority: int(in.GetPriority()),
		SRV: cloudflare.SRVData{
			Priority: int(in.GetSrv().GetPriority()), Weight: int(in.GetSrv().GetWeight()),
			Port: int(in.GetSrv().GetPort()), Target: in.GetSrv().GetTarget(),
		},
		CAA: cloudflare.CAAData{Flags: int(in.GetCaa().GetFlags()), Tag: in.GetCaa().GetTag(), Value: in.GetCaa().GetValue()},
	}
}

func recordToProto(r *cloudflare.DNSRecord) *neoboxv1.CloudflareDNSRecord {
	if r == nil {
		return nil
	}
	out := &neoboxv1.CloudflareDNSRecord{
		Id: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, Ttl: int32(r.TTL),
		Proxied: r.Proxied, Proxiable: r.Proxiable, Comment: r.Comment, Tags: r.Tags,
		Editable: cloudflare.Editable(r.Type),
	}
	if r.Priority != nil {
		out.Priority = int32(*r.Priority)
	}
	if d := r.Data; d != nil {
		switch r.Type {
		case "SRV":
			out.Srv = &neoboxv1.CloudflareSRVData{Priority: intOf(d.Priority), Weight: intOf(d.Weight), Port: intOf(d.Port), Target: d.Target}
		case "CAA":
			out.Caa = &neoboxv1.CloudflareCAAData{Flags: intOf(d.Flags), Tag: d.Tag, Value: d.Value}
		}
	}
	if !r.CreatedOn.IsZero() {
		out.CreatedOn = timestamppb.New(r.CreatedOn)
	}
	if !r.ModifiedOn.IsZero() {
		out.ModifiedOn = timestamppb.New(r.ModifiedOn)
	}
	return out
}

func intOf(p *int) int32 {
	if p == nil {
		return 0
	}
	return int32(*p)
}

func operationToProto(op *cfrepo.DNSOperation) *neoboxv1.CloudflareDNSOperation {
	out := &neoboxv1.CloudflareDNSOperation{
		Id: op.ID, ConnectionId: op.ConnectionID, AccountId: op.AccountID, ZoneId: op.ZoneID, ZoneName: op.ZoneName,
		Action: dnsActionToProto(op.Action), RecordId: op.RecordID, RecordType: op.RecordType, RecordName: op.RecordName,
		Before: recordToProto(op.Before), After: recordToProto(op.After),
		Status: dnsStatusToProto(op.Status), Error: op.Error,
		ActorId: op.ActorID, ActorName: op.ActorName, CreatedAt: timestamppb.New(op.CreatedAt),
	}
	if !op.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(op.FinishedAt)
	}
	if op.Requested != nil {
		// A body that doesn't fit a Struct is left out rather than failing
		// the whole list.
		if req, err := structpb.NewStruct(op.Requested); err == nil {
			out.Requested = req
		}
	}
	return out
}

func dnsActionToProto(a cfrepo.Action) neoboxv1.CloudflareRecordAction {
	switch a {
	case cfrepo.ActionCreate:
		return neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_CREATE
	case cfrepo.ActionUpdate:
		return neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_UPDATE
	case cfrepo.ActionDelete:
		return neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_DELETE
	case cfrepo.ActionSetProxied:
		return neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_SET_PROXIED
	}
	return neoboxv1.CloudflareRecordAction_CLOUDFLARE_RECORD_ACTION_UNSPECIFIED
}

func dnsStatusToProto(s cfrepo.Status) neoboxv1.CloudflareOperationStatus {
	switch s {
	case cfrepo.StatusPending:
		return neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_PENDING
	case cfrepo.StatusSucceeded:
		return neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_SUCCEEDED
	case cfrepo.StatusFailed:
		return neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_FAILED
	case cfrepo.StatusUnknown:
		return neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_UNKNOWN
	}
	return neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_UNSPECIFIED
}

// --- Workers and Pages ---

func (s *CloudflareServiceServer) ListCloudflareWorkers(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareWorkersRequest]) (*connect.Response[neoboxv1.ListCloudflareWorkersResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultListSize, 1, maxListSize)
	list, err := a.Workers(ctx, req.Msg.GetQuery(), page, size)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflareWorkersResponse{
		PageInfo: pageInfoOf(page, size, list.Total), AddressIssues: issuesToProto(list.Issues),
	}
	for _, w := range list.Items {
		out.Workers = append(out.Workers, &neoboxv1.CloudflareWorker{
			Name: w.Name, Addresses: addressesToProto(w.Addresses), AddressesIncomplete: w.Incomplete,
		})
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) ListCloudflarePagesProjects(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflarePagesProjectsRequest]) (*connect.Response[neoboxv1.ListCloudflarePagesProjectsResponse], error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultListSize, 1, maxListSize)
	list, err := a.PagesProjects(ctx, req.Msg.GetQuery(), page, size)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflarePagesProjectsResponse{
		PageInfo: pageInfoOf(page, size, list.Total), AddressIssues: issuesToProto(list.Issues),
	}
	for _, p := range list.Items {
		out.Projects = append(out.Projects, &neoboxv1.CloudflarePagesProject{
			Name: p.Name, Addresses: addressesToProto(p.Addresses), AddressesIncomplete: p.Incomplete,
		})
	}
	return connect.NewResponse(out), nil
}

// pageInfoOf describes a page of a list paged here; cfPageInfoToProto
// describes one Cloudflare paged.
func pageInfoOf(page, size, total int) *neoboxv1.CloudflarePageInfo {
	return &neoboxv1.CloudflarePageInfo{
		Page: int32(page), PageSize: int32(size), TotalCount: int64(total), TotalPages: int32((total + size - 1) / size),
	}
}

func addressesToProto(as []cloudflareacct.Address) []*neoboxv1.CloudflareAddress {
	out := make([]*neoboxv1.CloudflareAddress, 0, len(as))
	for _, a := range as {
		kind := neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_UNSPECIFIED
		switch a.Kind {
		case cloudflareacct.KindWorkersDev:
			kind = neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_WORKERS_DEV
		case cloudflareacct.KindCustomDomain:
			kind = neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_CUSTOM_DOMAIN
		case cloudflareacct.KindRoute:
			kind = neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_ROUTE
		case cloudflareacct.KindPagesDev:
			kind = neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_PAGES_DEV
		case cloudflareacct.KindProduction:
			kind = neoboxv1.CloudflareAddressKind_CLOUDFLARE_ADDRESS_KIND_PRODUCTION
		}
		out = append(out, &neoboxv1.CloudflareAddress{Url: a.URL, Kind: kind, Status: a.Status})
	}
	return out
}

func issuesToProto(is []cloudflareacct.AddressIssue) []*neoboxv1.CloudflareAddressIssue {
	out := make([]*neoboxv1.CloudflareAddressIssue, 0, len(is))
	for _, i := range is {
		out = append(out, &neoboxv1.CloudflareAddressIssue{Source: i.Source, Message: i.Message, PermissionDenied: i.PermissionDenied})
	}
	return out
}

// --- helpers ---

// pageArgs clamps a 1-based page and its size; size 0 means def.
func pageArgs(page, size int32, def, lo, hi int) (int, int) {
	p, n := int(page), int(size)
	if p < 1 {
		p = 1
	}
	if n <= 0 {
		n = def
	}
	return p, max(lo, min(hi, n))
}

func cfPageInfoToProto(info *cloudflare.PageInfo, page, size int) *neoboxv1.CloudflarePageInfo {
	out := &neoboxv1.CloudflarePageInfo{Page: int32(page), PageSize: int32(size)}
	if info != nil {
		out.TotalCount, out.TotalPages = int64(info.TotalCount), int32(info.TotalPages)
		if info.Page > 0 {
			out.Page = int32(info.Page)
		}
	}
	return out
}

func zoneToProto(z *cloudflare.Zone) *neoboxv1.CloudflareZone {
	access := neoboxv1.CloudflareRecordAccess_CLOUDFLARE_RECORD_ACCESS_UNKNOWN
	if z.DNSAccess() == cloudflare.DNSAccessRead {
		access = neoboxv1.CloudflareRecordAccess_CLOUDFLARE_RECORD_ACCESS_READ
	}
	return &neoboxv1.CloudflareZone{
		Id: z.ID, Name: z.Name, Status: z.Status, Paused: z.Paused, Type: z.Type,
		NameServers: z.NameServers, DnsAccess: access,
	}
}

// mapCloudflareErr turns Cloudflare and Manager errors into Connect codes.
// A rejected token is FailedPrecondition, never Unauthenticated, which
// would sign the user out of Neo Box.
func mapCloudflareErr(err error) error {
	var (
		cerr     *connect.Error
		ferr     *cloudflare.FieldError
		perm     *cloudflareacct.PermissionError
		rejected *cloudflareacct.TokenRejectedError
		apiErr   *cloudflare.APIError
	)
	switch {
	case errors.As(err, &cerr):
		return cerr
	case errors.As(err, &ferr):
		return connectx.InvalidArgument(ferr.Field, ferr.Message)
	case errors.As(err, &rejected):
		return connect.NewError(connect.CodeFailedPrecondition, rejected)
	case errors.As(err, &perm), errors.Is(err, cloudflareacct.ErrReadOnly):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, cloudflareacct.ErrZoneNotFound), errors.Is(err, cloudflareacct.ErrRecordNotFound):
		return connectx.NotFound(err.Error())
	case errors.Is(err, cloudflareacct.ErrNotEditable):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, cloudflareacct.ErrLogUnavailable):
		return connect.NewError(connect.CodeUnavailable, err)
	case cloudflare.IsUncertain(err):
		return connect.NewError(connect.CodeUnknown, err)
	case cloudflare.IsRejected(err) && errors.As(err, &apiErr):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("Cloudflare refused it: "+apiErr.Message()))
	case cloudflare.IsNotFound(err):
		return connectx.NotFound("not found at Cloudflare")
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	}
	return connect.NewError(connect.CodeUnavailable, err)
}
