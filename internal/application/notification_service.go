package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/notify"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	notifyrepo "go.orx.me/apps/neo-box/internal/repo/notify"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	defaultAlertListLimit = 50
	maxAlertListLimit     = 500
)

// NotificationServiceServer implements
// neoboxv1connect.NotificationServiceHandler. The notify service is attached
// after bootstrap via SetService; until then every RPC fails with
// FailedPrecondition.
//
// Adding a channel type means adding its settings to the proto oneofs and a
// case to channelSettingsFromProto and channelToProto.
type NotificationServiceServer struct {
	mu  sync.RWMutex
	svc *notify.Service
}

func NewNotificationServiceServer() *NotificationServiceServer {
	return &NotificationServiceServer{}
}

func (s *NotificationServiceServer) SetService(svc *notify.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.svc = svc
}

func (s *NotificationServiceServer) deps(ctx context.Context) (string, *notify.Service, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return "", nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.svc == nil {
		return "", nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("notifications are not available"))
	}
	return user.GetId(), s.svc, nil
}

func (s *NotificationServiceServer) ListNotificationChannels(ctx context.Context, _ *connect.Request[neoboxv1.ListNotificationChannelsRequest]) (*connect.Response[neoboxv1.ListNotificationChannelsResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := svc.ListChannels(ctx, userID)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.NotificationChannel, 0, len(channels))
	for _, c := range channels {
		out = append(out, channelToProto(c))
	}
	return connect.NewResponse(&neoboxv1.ListNotificationChannelsResponse{Channels: out}), nil
}

func (s *NotificationServiceServer) CreateNotificationChannel(ctx context.Context, req *connect.Request[neoboxv1.CreateNotificationChannelRequest]) (*connect.Response[neoboxv1.CreateNotificationChannelResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connectx.RequiredArgument("name")
	}
	settings, err := channelSettingsFromProto(req.Msg.GetTelegram(), true)
	if err != nil {
		return nil, err
	}
	c, err := svc.CreateChannel(ctx, userID, settings.typ, name, settings.config, settings.secret)
	if err != nil {
		return nil, mapNotifyErr(err)
	}
	return connect.NewResponse(&neoboxv1.CreateNotificationChannelResponse{Channel: channelToProto(c)}), nil
}

func (s *NotificationServiceServer) UpdateNotificationChannel(ctx context.Context, req *connect.Request[neoboxv1.UpdateNotificationChannelRequest]) (*connect.Response[neoboxv1.UpdateNotificationChannelResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.GetChannel(ctx, userID, req.Msg.GetId())
	if err != nil {
		return nil, mapNotifyErr(err)
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connectx.RequiredArgument("name")
	}
	settings, err := channelSettingsFromProto(req.Msg.GetTelegram(), false)
	if err != nil {
		return nil, err
	}
	if settings.typ != c.Type {
		return nil, connectx.InvalidArgument("settings", "must match the channel's type")
	}
	if err := svc.UpdateChannel(ctx, c, name, req.Msg.GetEnabled(), settings.config, settings.secret); err != nil {
		return nil, mapNotifyErr(err)
	}
	return connect.NewResponse(&neoboxv1.UpdateNotificationChannelResponse{Channel: channelToProto(c)}), nil
}

func (s *NotificationServiceServer) DeleteNotificationChannel(ctx context.Context, req *connect.Request[neoboxv1.DeleteNotificationChannelRequest]) (*connect.Response[neoboxv1.DeleteNotificationChannelResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.DeleteChannel(ctx, userID, req.Msg.GetId()); err != nil {
		return nil, mapNotifyErr(err)
	}
	return connect.NewResponse(&neoboxv1.DeleteNotificationChannelResponse{}), nil
}

func (s *NotificationServiceServer) TestNotificationChannel(ctx context.Context, req *connect.Request[neoboxv1.TestNotificationChannelRequest]) (*connect.Response[neoboxv1.TestNotificationChannelResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	c, err := svc.GetChannel(ctx, userID, req.Msg.GetId())
	if err != nil {
		return nil, mapNotifyErr(err)
	}
	resp := &neoboxv1.TestNotificationChannelResponse{}
	if terr := svc.TestChannel(ctx, c); terr != nil {
		if errors.Is(terr, notify.ErrNoCipher) {
			return nil, mapNotifyErr(terr)
		}
		resp.Error = terr.Error()
	}
	// Re-read for the recorded outcome.
	if c, err = svc.GetChannel(ctx, userID, c.ID); err != nil {
		return nil, mapNotifyErr(err)
	}
	resp.Channel = channelToProto(c)
	return connect.NewResponse(resp), nil
}

func (s *NotificationServiceServer) ListAlerts(ctx context.Context, req *connect.Request[neoboxv1.ListAlertsRequest]) (*connect.Response[neoboxv1.ListAlertsResponse], error) {
	userID, svc, err := s.deps(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = defaultAlertListLimit
	}
	alerts, err := svc.ListAlerts(ctx, userID, min(limit, maxAlertListLimit))
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	out := make([]*neoboxv1.Alert, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, alertToProto(a))
	}
	return connect.NewResponse(&neoboxv1.ListAlertsResponse{Alerts: out}), nil
}

type channelSettings struct {
	typ            string
	config, secret any // secret is nil to keep the stored one
}

// channelSettingsFromProto turns a settings oneof into a channel type and
// its config and secret. requireSecret is set on create.
func channelSettingsFromProto(tg *neoboxv1.TelegramChannelSettings, requireSecret bool) (*channelSettings, error) {
	if tg == nil {
		return nil, connectx.RequiredArgument("settings")
	}
	chatID := strings.TrimSpace(tg.GetChatId())
	if chatID == "" {
		return nil, connectx.RequiredArgument("telegram.chat_id")
	}
	out := &channelSettings{typ: notify.TelegramType, config: notify.TelegramConfig{ChatID: chatID}}
	if token := strings.TrimSpace(tg.GetBotToken()); token != "" {
		out.secret = notify.TelegramSecret{BotToken: token}
	} else if requireSecret {
		return nil, connectx.RequiredArgument("telegram.bot_token")
	}
	return out, nil
}

func mapNotifyErr(err error) error {
	var cerr *notify.CheckError
	switch {
	case errors.As(err, &cerr), errors.Is(err, notify.ErrUnknownType):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, notify.ErrNoCipher):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, notifyrepo.ErrNotFound):
		return connectx.NotFound("notification channel not found")
	}
	return connectx.InternalWith(err)
}

// channelToProto never includes the secret.
func channelToProto(c *notifyrepo.Channel) *neoboxv1.NotificationChannel {
	out := &neoboxv1.NotificationChannel{
		Id: c.ID, Name: c.Name, Enabled: c.Enabled, LastError: c.LastError,
		CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
	if !c.LastSentAt.IsZero() {
		out.LastSentAt = timestamppb.New(c.LastSentAt)
	}
	if c.Type == notify.TelegramType {
		var cfg notify.TelegramConfig
		_ = json.Unmarshal(c.Config, &cfg)
		out.Config = &neoboxv1.NotificationChannel_Telegram{Telegram: &neoboxv1.TelegramChannelConfig{ChatId: cfg.ChatID}}
	}
	return out
}

func alertToProto(a *notifyrepo.Alert) *neoboxv1.Alert {
	out := &neoboxv1.Alert{
		Id: a.ID, Source: a.Source, ConnectionId: a.ConnectionID, Kind: a.Kind,
		Severity: severityToProto(a.Severity), Title: a.Title, Body: a.Body, Link: a.Link,
		CreatedAt: timestamppb.New(a.CreatedAt), DeliveryError: a.DeliveryError,
	}
	if !a.DeliveredAt.IsZero() {
		out.DeliveredAt = timestamppb.New(a.DeliveredAt)
	}
	return out
}

func severityToProto(s notifyrepo.Severity) neoboxv1.AlertSeverity {
	switch s {
	case notifyrepo.SeverityInfo:
		return neoboxv1.AlertSeverity_ALERT_SEVERITY_INFO
	case notifyrepo.SeverityWarning:
		return neoboxv1.AlertSeverity_ALERT_SEVERITY_WARNING
	case notifyrepo.SeverityCritical:
		return neoboxv1.AlertSeverity_ALERT_SEVERITY_CRITICAL
	}
	return neoboxv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
}
