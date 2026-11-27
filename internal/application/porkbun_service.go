package application

import (
	"context"
	"errors"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/porkbun"
	"go.orx.me/apps/neo-box/internal/porkbunacct"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	defaultPorkbunDomainPageSize = 20
	minPorkbunDomainPageSize     = 5
	maxPorkbunDomainPageSize     = 100
	defaultPorkbunRecordPageSize = 50
	minPorkbunRecordPageSize     = 5
	maxPorkbunRecordPageSize     = 500
)

// PorkbunServiceServer implements neoboxv1connect.PorkbunServiceHandler.
// All RPCs are strictly read-only. Dependencies are attached after bootstrap
// via SetDeps; until then every RPC fails with FailedPrecondition.
type PorkbunServiceServer struct {
	mu      sync.RWMutex
	manager *porkbunacct.Manager
	conns   *connection.Service
}

func NewPorkbunServiceServer() *PorkbunServiceServer {
	return &PorkbunServiceServer{}
}

func (s *PorkbunServiceServer) SetDeps(m *porkbunacct.Manager, conns *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manager, s.conns = m, conns
}

type porkbunDeps struct {
	manager *porkbunacct.Manager
	conns   *connection.Service
	userID  string
}

func (s *PorkbunServiceServer) deps(ctx context.Context) (*porkbunDeps, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.manager == nil || s.conns == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "porkbun is not available")
	}
	return &porkbunDeps{manager: s.manager, conns: s.conns, userID: user.GetId()}, nil
}

func (d *porkbunDeps) open(ctx context.Context, connectionID string) (*porkbunacct.Account, error) {
	id := strings.TrimSpace(connectionID)
	if id == "" {
		return nil, connectx.RequiredArgument(ctx, "connection_id")
	}
	c, err := d.conns.Get(ctx, d.userID, id)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	if c.Provider != porkbun.ProviderType {
		return nil, connectx.NotFound("connection not found")
	}
	a, err := d.manager.Open(c)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	return a, nil
}

func (s *PorkbunServiceServer) ListPorkbunDomains(ctx context.Context, req *neoboxv1.ListPorkbunDomainsRequest) (*neoboxv1.ListPorkbunDomainsResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.GetPage(), req.GetPageSize(), defaultPorkbunDomainPageSize, minPorkbunDomainPageSize, maxPorkbunDomainPageSize)
	domains, info, summary, err := a.Domains(ctx, req.GetQuery(), porkbunFilterFromProto(req.GetFilter()), page, size)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	out := make([]*neoboxv1.PorkbunDomain, 0, len(domains))
	for i := range domains {
		out = append(out, porkbunDomainToProto(&domains[i]))
	}
	return &neoboxv1.ListPorkbunDomainsResponse{
		Domains:  out,
		PageInfo: porkbunPageInfoToProto(info),
		Summary:  porkbunSummaryToProto(summary),
	}, nil
}

func (s *PorkbunServiceServer) GetPorkbunDomain(ctx context.Context, req *neoboxv1.GetPorkbunDomainRequest) (*neoboxv1.GetPorkbunDomainResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	domain, err := porkbun.NormalizeDomain(req.GetDomain())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "domain", "must be a valid domain name")
	}
	detail, err := a.Domain(ctx, domain)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	return &neoboxv1.GetPorkbunDomainResponse{
		Domain:                porkbunDomainToProto(&detail.Domain),
		Nameservers:           detail.Nameservers,
		UsesPorkbunNameservers: detail.UsesPorkbunNS,
		NameserversIssue:      detail.NameserversIssue,
	}, nil
}

func (s *PorkbunServiceServer) ListPorkbunDNSRecords(ctx context.Context, req *neoboxv1.ListPorkbunDNSRecordsRequest) (*neoboxv1.ListPorkbunDNSRecordsResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	domain, err := porkbun.NormalizeDomain(req.GetDomain())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "domain", "must be a valid domain name")
	}
	typ := strings.ToUpper(strings.TrimSpace(req.GetType()))
	if typ != "" && !recordTypePattern.MatchString(typ) {
		return nil, connectx.InvalidArgument(ctx, "type", "must be a DNS record type")
	}
	page, size := pageArgs(req.GetPage(), req.GetPageSize(), defaultPorkbunRecordPageSize, minPorkbunRecordPageSize, maxPorkbunRecordPageSize)
	records, info, types, err := a.DNSRecords(ctx, domain, req.GetQuery(), typ, page, size)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	out := make([]*neoboxv1.PorkbunDNSRecord, 0, len(records))
	for i := range records {
		out = append(out, &neoboxv1.PorkbunDNSRecord{
			Id:       records[i].ID,
			Name:     records[i].Name,
			Type:     records[i].Type,
			Content:  records[i].Content,
			Ttl:      int32(records[i].TTL),
			Priority: records[i].Priority,
			Notes:    records[i].Notes,
		})
	}
	return &neoboxv1.ListPorkbunDNSRecordsResponse{
		Records:     out,
		PageInfo:    porkbunPageInfoToProto(info),
		RecordTypes: types,
	}, nil
}

func (s *PorkbunServiceServer) ListPorkbunURLForwards(ctx context.Context, req *neoboxv1.ListPorkbunURLForwardsRequest) (*neoboxv1.ListPorkbunURLForwardsResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	domain, err := porkbun.NormalizeDomain(req.GetDomain())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "domain", "must be a valid domain name")
	}
	fwds, err := a.URLForwards(ctx, domain)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	out := make([]*neoboxv1.PorkbunURLForward, 0, len(fwds))
	for i := range fwds {
		out = append(out, &neoboxv1.PorkbunURLForward{
			Id:          fwds[i].ID,
			Subdomain:   fwds[i].Subdomain,
			Location:    fwds[i].Location,
			Type:        fwds[i].Type,
			IncludePath: fwds[i].IncludePath,
			Wildcard:    fwds[i].Wildcard,
		})
	}
	return &neoboxv1.ListPorkbunURLForwardsResponse{Forwards: out}, nil
}

func (s *PorkbunServiceServer) ListPorkbunDNSSECRecords(ctx context.Context, req *neoboxv1.ListPorkbunDNSSECRecordsRequest) (*neoboxv1.ListPorkbunDNSSECRecordsResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	domain, err := porkbun.NormalizeDomain(req.GetDomain())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "domain", "must be a valid domain name")
	}
	recs, err := a.DNSSECRecords(ctx, domain)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	out := make([]*neoboxv1.PorkbunDNSSECRecord, 0, len(recs))
	for i := range recs {
		out = append(out, &neoboxv1.PorkbunDNSSECRecord{
			KeyTag:     recs[i].KeyTag,
			Algorithm:  recs[i].Algorithm,
			DigestType: recs[i].DigestType,
			Digest:     recs[i].Digest,
		})
	}
	return &neoboxv1.ListPorkbunDNSSECRecordsResponse{Records: out}, nil
}

func (s *PorkbunServiceServer) ListPorkbunGlueRecords(ctx context.Context, req *neoboxv1.ListPorkbunGlueRecordsRequest) (*neoboxv1.ListPorkbunGlueRecordsResponse, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	a, err := d.open(ctx, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	domain, err := porkbun.NormalizeDomain(req.GetDomain())
	if err != nil {
		return nil, connectx.InvalidArgument(ctx, "domain", "must be a valid domain name")
	}
	hosts, err := a.GlueRecords(ctx, domain)
	if err != nil {
		return nil, mapPorkbunErr(ctx, err)
	}
	out := make([]*neoboxv1.PorkbunGlueHost, 0, len(hosts))
	for i := range hosts {
		out = append(out, &neoboxv1.PorkbunGlueHost{
			Host: hosts[i].Host,
			Ipv4: hosts[i].IPv4,
			Ipv6: hosts[i].IPv6,
		})
	}
	return &neoboxv1.ListPorkbunGlueRecordsResponse{Hosts: out}, nil
}

// --- conversions and error mapping ---

func porkbunFilterFromProto(f neoboxv1.PorkbunDomainFilter) porkbunacct.DomainFilter {
	switch f {
	case neoboxv1.PorkbunDomainFilter_PORKBUN_DOMAIN_FILTER_EXPIRING_SOON:
		return porkbunacct.FilterExpiringSoon
	case neoboxv1.PorkbunDomainFilter_PORKBUN_DOMAIN_FILTER_AUTO_RENEW_OFF:
		return porkbunacct.FilterAutoRenewOff
	case neoboxv1.PorkbunDomainFilter_PORKBUN_DOMAIN_FILTER_API_ACCESS_OFF:
		return porkbunacct.FilterAPIAccessOff
	case neoboxv1.PorkbunDomainFilter_PORKBUN_DOMAIN_FILTER_EXTERNAL_NAMESERVERS:
		return porkbunacct.FilterExternalNameservers
	default:
		return porkbunacct.FilterAll
	}
}

func porkbunDomainToProto(d *porkbun.Domain) *neoboxv1.PorkbunDomain {
	out := &neoboxv1.PorkbunDomain{
		Domain:       d.Domain,
		Status:       d.Status,
		Tld:          d.TLD,
		SecurityLock: d.SecurityLock,
		WhoisPrivacy: d.WhoisPrivacy,
		AutoRenew:    d.AutoRenew,
		NotLocal:     d.NotLocal,
		ApiAccess:    d.APIAccess,
	}
	if !d.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(d.CreatedAt)
	}
	if !d.ExpiresAt.IsZero() {
		out.ExpiresAt = timestamppb.New(d.ExpiresAt)
	}
	if len(d.Labels) > 0 {
		out.Labels = make([]*neoboxv1.PorkbunDomainLabel, 0, len(d.Labels))
		for _, l := range d.Labels {
			out.Labels = append(out.Labels, &neoboxv1.PorkbunDomainLabel{
				Id:    l.ID,
				Title: l.Title,
				Color: l.Color,
			})
		}
	}
	return out
}

func porkbunPageInfoToProto(info porkbunacct.PageInfo) *neoboxv1.PorkbunPageInfo {
	return &neoboxv1.PorkbunPageInfo{
		Page:       int32(info.Page),
		PageSize:   int32(info.PageSize),
		TotalPages: int32(info.TotalPages),
		TotalCount: int64(info.TotalCount),
	}
}

func porkbunSummaryToProto(s porkbunacct.PortfolioSummary) *neoboxv1.PorkbunPortfolioSummary {
	return &neoboxv1.PorkbunPortfolioSummary{
		TotalDomains:             int32(s.TotalDomains),
		ExpiringSoonCount:        int32(s.ExpiringSoonCount),
		ExpiredCount:             int32(s.ExpiredCount),
		AutoRenewOffCount:        int32(s.AutoRenewOffCount),
		SecurityLockOffCount:     int32(s.SecurityLockOffCount),
		ApiAccessOffCount:        int32(s.APIAccessOffCount),
		ExternalNameserversCount: int32(s.ExternalNameserversCount),
	}
}

// mapPorkbunErr translates porkbunacct / porkbun errors into Connect errors.
// Rejected API keys become FailedPrecondition (never Unauthenticated, which
// signs the user out of Neo Box).
func mapPorkbunErr(ctx context.Context, err error) error {
	var (
		rejected *porkbunacct.CredentialsRejectedError
		perm     *porkbunacct.PermissionError
		disabled *porkbunacct.APIAccessDisabledError
		limited  *porkbunacct.RateLimitedError
		apiErr   *porkbun.APIError
	)
	switch {
	case errors.As(err, &rejected):
		return connect.NewError(connect.CodeFailedPrecondition, rejected.Error()).WithCause(rejected)
	case errors.As(err, &disabled):
		return connect.NewError(connect.CodeFailedPrecondition, disabled.Error()).WithCause(disabled)
	case errors.As(err, &perm):
		return connect.NewError(connect.CodePermissionDenied, perm.Error()).WithCause(perm)
	case errors.As(err, &limited):
		return connect.NewError(connect.CodeResourceExhausted, limited.Error()).WithCause(limited)
	case errors.Is(err, porkbunacct.ErrDomainNotFound):
		return connectx.NotFound(err.Error())
	case errors.Is(err, connrepo.ErrNotFound):
		return connectx.NotFound("connection not found")
	case errors.As(err, &apiErr):
		return connect.NewError(connect.CodeFailedPrecondition, apiErr.DisplayMessage()).WithCause(apiErr)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, "Porkbun took too long to respond")
	}
	return connectx.InternalWith(err)
}
