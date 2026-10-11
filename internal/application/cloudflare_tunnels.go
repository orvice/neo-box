package application

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// --- Tunnels ---

// openAccount resolves the caller and their Cloudflare connection.
func (s *CloudflareServiceServer) openAccount(ctx context.Context, connectionID string) (*cloudflareDeps, *cloudflareacct.Account, error) {
	d, err := s.deps(ctx)
	if err != nil {
		return nil, nil, err
	}
	a, err := d.open(ctx, connectionID)
	if err != nil {
		return nil, nil, err
	}
	return d, a, nil
}

func (s *CloudflareServiceServer) ListCloudflareTunnels(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareTunnelsRequest]) (*connect.Response[neoboxv1.ListCloudflareTunnelsResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	page, size := pageArgs(req.Msg.GetPage(), req.Msg.GetPageSize(), defaultListSize, 1, maxListSize)
	list, err := a.Tunnels(ctx, req.Msg.GetQuery(), page, size)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflareTunnelsResponse{PageInfo: pageInfoOf(page, size, list.Total)}
	for i := range list.Items {
		out.Tunnels = append(out.Tunnels, tunnelToProto(&list.Items[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) GetCloudflareTunnel(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareTunnelRequest]) (*connect.Response[neoboxv1.GetCloudflareTunnelResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	d, err := a.TunnelDetail(ctx, strings.TrimSpace(req.Msg.GetTunnelId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.GetCloudflareTunnelResponse{Tunnel: tunnelToProto(d.Tunnel), Issues: readIssuesToProto(d.Issues)}
	for i := range d.Connectors {
		out.Connectors = append(out.Connectors, connectorToProto(&d.Connectors[i]))
	}
	if d.Config != nil {
		out.ConfigVersion = int32(d.Config.Version)
		out.CatchAllService = d.Config.Config.CatchAllService()
		for _, r := range d.Config.Config.Routes() {
			out.Hostnames = append(out.Hostnames, hostnameToProto(r))
		}
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) CreateCloudflareTunnel(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareTunnelRequest]) (*connect.Response[neoboxv1.CreateCloudflareTunnelResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	t, err := a.CreateTunnel(ctx, req.Msg.GetName())
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareTunnelResponse{Tunnel: tunnelToProto(t)}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareTunnel(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareTunnelRequest]) (*connect.Response[neoboxv1.DeleteCloudflareTunnelResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := a.DeleteTunnel(ctx, strings.TrimSpace(req.Msg.GetTunnelId()), req.Msg.GetCleanupConnections()); err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareTunnelResponse{}), nil
}

func (s *CloudflareServiceServer) GetCloudflareTunnelToken(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareTunnelTokenRequest]) (*connect.Response[neoboxv1.GetCloudflareTunnelTokenResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	token, err := a.TunnelToken(ctx, strings.TrimSpace(req.Msg.GetTunnelId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.GetCloudflareTunnelTokenResponse{Token: token}), nil
}

func (s *CloudflareServiceServer) CleanupCloudflareTunnelConnections(ctx context.Context, req *connect.Request[neoboxv1.CleanupCloudflareTunnelConnectionsRequest]) (*connect.Response[neoboxv1.CleanupCloudflareTunnelConnectionsResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := a.CleanupTunnelConnections(ctx, strings.TrimSpace(req.Msg.GetTunnelId())); err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.CleanupCloudflareTunnelConnectionsResponse{}), nil
}

// --- hostnames ---

func (s *CloudflareServiceServer) AddCloudflareTunnelHostname(ctx context.Context, req *connect.Request[neoboxv1.AddCloudflareTunnelHostnameRequest]) (*connect.Response[neoboxv1.AddCloudflareTunnelHostnameResponse], error) {
	d, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	m := req.Msg
	ch, err := a.AddHostname(ctx, d.user, strings.TrimSpace(m.GetTunnelId()), cloudflareacct.HostnameInput{
		Hostname: m.GetHostname(), Path: m.GetPath(), Service: m.GetService(),
	}, m.GetCreateDns())
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.AddCloudflareTunnelHostnameResponse{
		Hostname:      routeToProto(ch.Config, cloudflare.NormalizeHostname(m.GetHostname()), strings.TrimSpace(m.GetPath())),
		ConfigVersion: int32(ch.Config.Version),
		Record:        recordResultToProto(ch.Record),
	}), nil
}

func (s *CloudflareServiceServer) UpdateCloudflareTunnelHostname(ctx context.Context, req *connect.Request[neoboxv1.UpdateCloudflareTunnelHostnameRequest]) (*connect.Response[neoboxv1.UpdateCloudflareTunnelHostnameResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	m := req.Msg
	cfg, err := a.UpdateHostname(ctx, strings.TrimSpace(m.GetTunnelId()), m.GetHostname(), m.GetCurrentPath(), cloudflareacct.HostnameInput{
		Path: m.GetPath(), Service: m.GetService(),
	})
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateCloudflareTunnelHostnameResponse{
		Hostname:      routeToProto(cfg, cloudflare.NormalizeHostname(m.GetHostname()), strings.TrimSpace(m.GetPath())),
		ConfigVersion: int32(cfg.Version),
	}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareTunnelHostname(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareTunnelHostnameRequest]) (*connect.Response[neoboxv1.DeleteCloudflareTunnelHostnameResponse], error) {
	d, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	m := req.Msg
	ch, err := a.DeleteHostname(ctx, d.user, strings.TrimSpace(m.GetTunnelId()), m.GetHostname(), m.GetPath(), m.GetDeleteDns())
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareTunnelHostnameResponse{
		ConfigVersion: int32(ch.Config.Version),
		Record:        recordResultToProto(ch.Record),
	}), nil
}

// --- conversion ---

func tunnelToProto(t *cloudflare.Tunnel) *neoboxv1.CloudflareTunnel {
	out := &neoboxv1.CloudflareTunnel{
		Id: t.ID, Name: t.Name, ManagedRemotely: t.RemoteConfig, ActiveConnections: int32(len(t.Connections)),
		Status: tunnelStatusToProto(t.Status),
	}
	if !t.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(t.CreatedAt)
	}
	if len(t.Connections) == 0 && t.ConnsInactiveAt != nil {
		out.InactiveSince = timestamppb.New(*t.ConnsInactiveAt)
	}
	return out
}

func tunnelStatusToProto(s string) neoboxv1.CloudflareTunnelStatus {
	switch s {
	case "inactive":
		return neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_INACTIVE
	case "degraded":
		return neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_DEGRADED
	case "healthy":
		return neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_HEALTHY
	case "down":
		return neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_DOWN
	}
	return neoboxv1.CloudflareTunnelStatus_CLOUDFLARE_TUNNEL_STATUS_UNSPECIFIED
}

func connectorToProto(c *cloudflare.TunnelConnector) *neoboxv1.CloudflareTunnelConnector {
	out := &neoboxv1.CloudflareTunnelConnector{Id: c.ID, Version: c.Version, Arch: c.Arch}
	if c.RunAt != nil {
		out.RunAt = timestamppb.New(*c.RunAt)
	}
	for _, conn := range c.Conns {
		pc := &neoboxv1.CloudflareTunnelConnection{
			Id: conn.ID, ColoName: conn.ColoName, OriginIp: conn.OriginIP, PendingReconnect: conn.IsPendingReconnect,
		}
		if conn.OpenedAt != nil {
			pc.OpenedAt = timestamppb.New(*conn.OpenedAt)
		}
		out.Connections = append(out.Connections, pc)
	}
	return out
}

func hostnameToProto(r cloudflare.IngressRule) *neoboxv1.CloudflareTunnelHostname {
	return &neoboxv1.CloudflareTunnelHostname{
		Hostname: r.Hostname, Path: r.Path, Service: r.Service, HasOriginSettings: r.HasSettings(),
	}
}

// routeToProto is the route of cfg for the hostname and path, as saved.
func routeToProto(cfg *cloudflare.TunnelConfiguration, hostname, path string) *neoboxv1.CloudflareTunnelHostname {
	for _, r := range cfg.Config.Routes() {
		if strings.EqualFold(r.Hostname, hostname) && r.Path == path {
			return hostnameToProto(r)
		}
	}
	return nil
}

func readIssuesToProto(is []cloudflareacct.AddressIssue) []*neoboxv1.CloudflareReadIssue {
	out := make([]*neoboxv1.CloudflareReadIssue, 0, len(is))
	for _, i := range is {
		out = append(out, &neoboxv1.CloudflareReadIssue{Source: i.Source, Message: i.Message, PermissionDenied: i.PermissionDenied})
	}
	return out
}

func recordResultToProto(r cloudflareacct.RecordResult) *neoboxv1.CloudflareTunnelRecordResult {
	status := neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_UNSPECIFIED
	switch r.Status {
	case cloudflareacct.RecordNotRequested:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_NOT_REQUESTED
	case cloudflareacct.RecordCreated:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CREATED
	case cloudflareacct.RecordExists:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_EXISTS
	case cloudflareacct.RecordDeleted:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_DELETED
	case cloudflareacct.RecordKept:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_KEPT
	case cloudflareacct.RecordNoZone:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_NO_ZONE
	case cloudflareacct.RecordConflict:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_CONFLICT
	case cloudflareacct.RecordFailed:
		status = neoboxv1.CloudflareTunnelRecordStatus_CLOUDFLARE_TUNNEL_RECORD_STATUS_FAILED
	}
	return &neoboxv1.CloudflareTunnelRecordResult{Status: status, Message: r.Message, RecordId: r.RecordID}
}
