package application

import (
	"context"
	"errors"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/repo/auth"
	tgrepo "go.orx.me/apps/neo-box/internal/repo/telegram"
	"go.orx.me/apps/neo-box/internal/tgbot"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// TelegramServiceServer implements neoboxv1connect.TelegramServiceHandler.
// When the server-wide Telegram bot is not configured, GetTelegramBotStatus
// reports enabled=false and link/unbind RPCs return FailedPrecondition.
type TelegramServiceServer struct {
	mu  sync.RWMutex
	svc *tgbot.Service
}

func NewTelegramServiceServer() *TelegramServiceServer {
	return &TelegramServiceServer{}
}

func (s *TelegramServiceServer) SetService(svc *tgbot.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.svc = svc
}

func (s *TelegramServiceServer) service() *tgbot.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.svc
}

func (s *TelegramServiceServer) GetTelegramBotStatus(ctx context.Context, _ *neoboxv1.GetTelegramBotStatusRequest) (*neoboxv1.GetTelegramBotStatusResponse, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	svc := s.service()
	if svc == nil {
		return &neoboxv1.GetTelegramBotStatusResponse{Enabled: false}, nil
	}
	resp := &neoboxv1.GetTelegramBotStatusResponse{
		Enabled:     true,
		BotUsername: svc.BotUsername(),
	}
	binding, err := svc.GetBinding(ctx, user.GetId())
	if err != nil {
		if errors.Is(err, tgrepo.ErrNotFound) {
			return resp, nil
		}
		return nil, connectx.InternalWith(err)
	}
	resp.Binding = &neoboxv1.TelegramBinding{
		TelegramUserId:   binding.TelegramUserID,
		TelegramUsername: binding.TelegramUsername,
		DisplayName:      binding.DisplayName,
		LinkedAt:         timestamppb.New(binding.CreatedAt),
	}
	return resp, nil
}

func (s *TelegramServiceServer) CreateTelegramLinkToken(ctx context.Context, _ *neoboxv1.CreateTelegramLinkTokenRequest) (*neoboxv1.CreateTelegramLinkTokenResponse, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	svc := s.service()
	if svc == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "telegram bot is not configured on the server")
	}
	info, err := svc.CreateLinkToken(ctx, user.GetId())
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	return &neoboxv1.CreateTelegramLinkTokenResponse{
		DeepLink:  info.DeepLink,
		Command:   info.Command,
		ExpiresAt: timestamppb.New(info.ExpiresAt),
	}, nil
}

func (s *TelegramServiceServer) UnbindTelegram(ctx context.Context, _ *neoboxv1.UnbindTelegramRequest) (*neoboxv1.UnbindTelegramResponse, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	svc := s.service()
	if svc == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "telegram bot is not configured on the server")
	}
	if err := svc.Unbind(ctx, user.GetId()); err != nil {
		return nil, connectx.InternalWith(err)
	}
	return &neoboxv1.UnbindTelegramResponse{}, nil
}
