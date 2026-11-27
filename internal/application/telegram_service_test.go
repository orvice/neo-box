package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	connmem "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	tgrepo "go.orx.me/apps/neo-box/internal/repo/telegram"
	tgmem "go.orx.me/apps/neo-box/internal/repo/telegram/memory"
	"go.orx.me/apps/neo-box/internal/telegram/tgtest"
	"go.orx.me/apps/neo-box/internal/tgbot"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

func TestTelegramServiceDisabledAndLifecycle(t *testing.T) {
	srv := NewTelegramServiceServer()
	userCtx := auth.WithAuthenticated(context.Background(), &neoboxv1.User{Id: "u1", Username: "alice"}, &auth.Session{ID: "s1", UserID: "u1"})

	// Unauthenticated fails with Unauthenticated.
	if _, err := srv.GetTelegramBotStatus(context.Background(), &neoboxv1.GetTelegramBotStatusRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated code = %v, want Unauthenticated", connect.CodeOf(err))
	}

	// Disabled bot reports enabled=false and rejects link/unbind.
	status, err := srv.GetTelegramBotStatus(userCtx, &neoboxv1.GetTelegramBotStatusRequest{})
	if err != nil || status.GetEnabled() {
		t.Fatalf("disabled status = %+v, %v", status, err)
	}
	if _, err := srv.CreateTelegramLinkToken(userCtx, &neoboxv1.CreateTelegramLinkTokenRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled CreateLinkToken code = %v, want FailedPrecondition", connect.CodeOf(err))
	}

	// Attach an active bot service.
	fakeTG := tgtest.New("123:TOK", "my_neobox_bot")
	defer fakeTG.Close()
	tgStore := tgmem.New()
	bot, err := tgbot.New(tgbot.Config{
		BotToken:       "123:TOK",
		APIEndpoint:    fakeTG.URL,
		Mode:           tgbot.ModeWebhook,
		WebhookURL:     "https://neobox.example.com/api/telegram/webhook",
		WebhookSecret:  "sec",
		AutoSetWebhook: true,
	}, tgStore, nil, connection.NewService(connmem.New(), nil))
	if err != nil {
		t.Fatalf("tgbot.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); bot.Wait() }()
	if err := bot.Start(runCtx); err != nil {
		t.Fatalf("bot.Start: %v", err)
	}
	srv.SetService(bot)

	status, err = srv.GetTelegramBotStatus(userCtx, &neoboxv1.GetTelegramBotStatusRequest{})
	if err != nil || !status.GetEnabled() || status.GetBotUsername() != "my_neobox_bot" || status.GetBinding() != nil {
		t.Fatalf("enabled status = %+v, %v", status, err)
	}

	linkResp, err := srv.CreateTelegramLinkToken(userCtx, &neoboxv1.CreateTelegramLinkTokenRequest{})
	if err != nil {
		t.Fatalf("CreateTelegramLinkToken: %v", err)
	}
	if !strings.HasPrefix(linkResp.GetDeepLink(), "https://t.me/my_neobox_bot?start=") || !strings.HasPrefix(linkResp.GetCommand(), "/start ") {
		t.Fatalf("unexpected link response: %+v", linkResp)
	}

	// Simulate binding persisted and verify GetTelegramBotStatus + UnbindTelegram.
	now := time.Now().UTC()
	if err := tgStore.SaveBinding(context.Background(), &tgrepo.Binding{
		ID: "b1", UserID: "u1", TelegramUserID: 42, ChatID: 42, TelegramUsername: "alice_tg", DisplayName: "Alice",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}
	status, err = srv.GetTelegramBotStatus(userCtx, &neoboxv1.GetTelegramBotStatusRequest{})
	if err != nil || status.GetBinding() == nil || status.GetBinding().GetTelegramUsername() != "alice_tg" {
		t.Fatalf("bound status = %+v, %v", status, err)
	}

	if _, err := srv.UnbindTelegram(userCtx, &neoboxv1.UnbindTelegramRequest{}); err != nil {
		t.Fatalf("UnbindTelegram: %v", err)
	}
	status, err = srv.GetTelegramBotStatus(userCtx, &neoboxv1.GetTelegramBotStatusRequest{})
	if err != nil || status.GetBinding() != nil {
		t.Fatalf("unbound status = %+v, %v", status, err)
	}
}
