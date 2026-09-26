package application

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	"go.orx.me/apps/neo-box/internal/wasabi"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// ConnectionServiceServer implements neoboxv1connect.ConnectionServiceHandler.
// The connection service is attached after bootstrap via SetService; until
// then every RPC fails with FailedPrecondition.
//
// Adding a provider means adding its settings to the proto oneofs and a case
// to settingsFromProto and configToProto.
type ConnectionServiceServer struct {
	mu  sync.RWMutex
	svc *connection.Service
}

func NewConnectionServiceServer() *ConnectionServiceServer {
	return &ConnectionServiceServer{}
}

func (s *ConnectionServiceServer) SetService(svc *connection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.svc = svc
}

func (s *ConnectionServiceServer) deps(ctx context.Context) (string, *connection.Service, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.svc == nil {
		return "", nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("connections are not available"))
	}
	return user.GetId(), s.svc, nil
}

func (s *ConnectionServiceServer) ListConnections(ctx context.Context, req *connect.Request[neoboxv1.ListConnectionsRequest]) (*connect.Response[neoboxv1.ListConnectionsResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	filter := connrepo.Filter{UserID: userID}
	if p := req.Msg.GetProvider(); p != neoboxv1.Provider_PROVIDER_UNSPECIFIED {
		key, ok := providerKey(p)
		if !ok {
			return nil, connectx.InvalidArgument("provider", "unknown provider")
		}
		filter.Provider = key
	}
	conns, err := svc.List(ctx, filter)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.Connection, 0, len(conns))
	for _, c := range conns {
		out = append(out, connectionToProto(c))
	}
	return connect.NewResponse(&neoboxv1.ListConnectionsResponse{Connections: out}), nil
}

func (s *ConnectionServiceServer) GetConnection(ctx context.Context, req *connect.Request[neoboxv1.GetConnectionRequest]) (*connect.Response[neoboxv1.GetConnectionResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := loadConnection(ctx, svc, userID, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.GetConnectionResponse{Connection: connectionToProto(c)}), nil
}

func (s *ConnectionServiceServer) CreateConnection(ctx context.Context, req *connect.Request[neoboxv1.CreateConnectionRequest]) (*connect.Response[neoboxv1.CreateConnectionResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connectx.RequiredArgument("name")
	}
	settings, err := createSettings(req.Msg)
	if err != nil {
		return nil, err
	}
	if settings.secret == nil {
		return nil, connectx.RequiredArgument(settings.secretField)
	}
	c, err := svc.Create(ctx, userID, settings.provider, name, settings.config, settings.secret)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateConnectionResponse{Connection: connectionToProto(c)}), nil
}

func (s *ConnectionServiceServer) UpdateConnection(ctx context.Context, req *connect.Request[neoboxv1.UpdateConnectionRequest]) (*connect.Response[neoboxv1.UpdateConnectionResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := loadConnection(ctx, svc, userID, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connectx.RequiredArgument("name")
	}
	settings, err := updateSettings(req.Msg)
	if err != nil {
		return nil, err
	}
	if settings.provider != c.Provider {
		return nil, connectx.InvalidArgument("settings", "must be for the connection's provider")
	}
	// A nil secret keeps the stored one.
	if err := svc.Update(ctx, c, name, settings.config, settings.secret); err != nil {
		return nil, mapConnectionErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateConnectionResponse{Connection: connectionToProto(c)}), nil
}

func (s *ConnectionServiceServer) DeleteConnection(ctx context.Context, req *connect.Request[neoboxv1.DeleteConnectionRequest]) (*connect.Response[neoboxv1.DeleteConnectionResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := loadConnection(ctx, svc, userID, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, c); err != nil {
		return nil, mapConnectionErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteConnectionResponse{}), nil
}

func (s *ConnectionServiceServer) TestConnection(ctx context.Context, req *connect.Request[neoboxv1.TestConnectionRequest]) (*connect.Response[neoboxv1.TestConnectionResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := loadConnection(ctx, svc, userID, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	if err := svc.Test(ctx, c); err != nil {
		return nil, mapConnectionErr(err)
	}
	return connect.NewResponse(&neoboxv1.TestConnectionResponse{Connection: connectionToProto(c)}), nil
}

// --- provider settings ---

// providerSettings is one provider's settings decoded from a request.
// secret is nil when the request left it empty.
type providerSettings struct {
	provider    string
	config      any
	secret      any
	secretField string
}

func createSettings(m *neoboxv1.CreateConnectionRequest) (*providerSettings, error) {
	switch v := m.GetSettings().(type) {
	case *neoboxv1.CreateConnectionRequest_Nocodb:
		return nocodbSettings(v.Nocodb)
	case *neoboxv1.CreateConnectionRequest_Wasabi:
		return wasabiSettings(v.Wasabi)
	}
	return nil, connectx.RequiredArgument("settings")
}

func updateSettings(m *neoboxv1.UpdateConnectionRequest) (*providerSettings, error) {
	switch v := m.GetSettings().(type) {
	case *neoboxv1.UpdateConnectionRequest_Nocodb:
		return nocodbSettings(v.Nocodb)
	case *neoboxv1.UpdateConnectionRequest_Wasabi:
		return wasabiSettings(v.Wasabi)
	}
	return nil, connectx.RequiredArgument("settings")
}

func nocodbSettings(in *neoboxv1.NocoDBConnectionSettings) (*providerSettings, error) {
	baseURL, err := nocodb.NormalizeBaseURL(in.GetBaseUrl())
	if err != nil {
		return nil, connectx.InvalidArgument("base_url", "must be an http(s) URL")
	}
	rps := in.GetRequestsPerSecond()
	if rps < 0 || rps > 1000 || math.IsNaN(rps) {
		return nil, connectx.InvalidArgument("requests_per_second", "must be between 0 and 1000")
	}
	out := &providerSettings{
		provider:    nocodb.ProviderType,
		config:      nocodb.ConnectionConfig{BaseURL: baseURL, RequestsPerSecond: rps},
		secretField: "api_token",
	}
	if token := strings.TrimSpace(in.GetApiToken()); token != "" {
		out.secret = nocodb.ConnectionSecret{APIToken: token}
	}
	return out, nil
}

func wasabiSettings(in *neoboxv1.WasabiConnectionSettings) (*providerSettings, error) {
	keyID := strings.TrimSpace(in.GetAccessKeyId())
	if keyID == "" {
		return nil, connectx.RequiredArgument("access_key_id")
	}
	price := in.GetPricePerTbMonth()
	if price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return nil, connectx.InvalidArgument("price_per_tb_month", "must be a non-negative number")
	}
	cfg := wasabi.ConnectionConfig{
		AccessKeyID:         keyID,
		PricePerTBMonth:     price,
		BillingCycleAnchor:  strings.TrimSpace(in.GetBillingCycleAnchor()),
		CostEstimateEnabled: in.GetCostEstimateEnabled(),
	}
	anchor, err := cfg.Anchor()
	if err != nil {
		return nil, connectx.InvalidArgument("billing_cycle_anchor", "must be a YYYY-MM-DD day")
	}
	if anchor.After(time.Now()) {
		return nil, connectx.InvalidArgument("billing_cycle_anchor", "must not be in the future")
	}
	out := &providerSettings{provider: wasabi.ProviderType, config: cfg, secretField: "secret_key"}
	if sk := strings.TrimSpace(in.GetSecretKey()); sk != "" {
		out.secret = wasabi.ConnectionSecret{SecretKey: sk}
	}
	return out, nil
}

func configToProto(c *connrepo.Connection, out *neoboxv1.Connection) {
	switch c.Provider {
	case nocodb.ProviderType:
		var cfg nocodb.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) == nil {
			out.Config = &neoboxv1.Connection_Nocodb{Nocodb: &neoboxv1.NocoDBConnectionConfig{
				BaseUrl: cfg.BaseURL, RequestsPerSecond: cfg.RateLimit(),
			}}
		}
	case wasabi.ProviderType:
		var cfg wasabi.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) == nil {
			out.Config = &neoboxv1.Connection_Wasabi{Wasabi: &neoboxv1.WasabiConnectionConfig{
				AccessKeyId: cfg.AccessKeyID, PricePerTbMonth: cfg.Price(),
				BillingCycleAnchor: cfg.BillingCycleAnchor, CostEstimateEnabled: cfg.CostEstimateEnabled,
			}}
		}
	}
}

func providerKey(p neoboxv1.Provider) (string, bool) {
	switch p {
	case neoboxv1.Provider_PROVIDER_NOCODB:
		return nocodb.ProviderType, true
	case neoboxv1.Provider_PROVIDER_WASABI:
		return wasabi.ProviderType, true
	}
	return "", false
}

func providerToProto(key string) neoboxv1.Provider {
	switch key {
	case nocodb.ProviderType:
		return neoboxv1.Provider_PROVIDER_NOCODB
	case wasabi.ProviderType:
		return neoboxv1.Provider_PROVIDER_WASABI
	}
	return neoboxv1.Provider_PROVIDER_UNSPECIFIED
}

// --- helpers ---

func loadConnection(ctx context.Context, svc *connection.Service, userID, id string) (*connrepo.Connection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, connectx.RequiredArgument("id")
	}
	c, err := svc.Get(ctx, userID, id)
	if err != nil {
		return nil, mapConnectionErr(err)
	}
	return c, nil
}

func mapConnectionErr(err error) error {
	var verr *connection.VerifyError
	switch {
	case errors.As(err, &verr):
		return connect.NewError(connect.CodeInvalidArgument, verr)
	case errors.Is(err, connection.ErrNoCipher), errors.Is(err, connection.ErrBusy):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, connrepo.ErrNotFound):
		return connectx.NotFound("connection not found")
	}
	return connectx.InternalWith(err)
}

// connectionToProto never includes the secret.
func connectionToProto(c *connrepo.Connection) *neoboxv1.Connection {
	out := &neoboxv1.Connection{
		Id: c.ID, Name: c.Name, Provider: providerToProto(c.Provider),
		Status: statusToProto(c.Status), StatusMessage: c.StatusMessage,
		CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
	if !c.StatusCheckedAt.IsZero() {
		out.StatusCheckedAt = timestamppb.New(c.StatusCheckedAt)
	}
	configToProto(c, out)
	return out
}

func statusToProto(s connrepo.Status) neoboxv1.ConnectionStatus {
	switch s {
	case connrepo.StatusOK:
		return neoboxv1.ConnectionStatus_CONNECTION_STATUS_OK
	case connrepo.StatusError:
		return neoboxv1.ConnectionStatus_CONNECTION_STATUS_ERROR
	}
	return neoboxv1.ConnectionStatus_CONNECTION_STATUS_UNSPECIFIED
}
