package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/nocodb"
	authrepo "go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	connmem "go.orx.me/apps/neo-box/internal/repo/connection/memory"
	tgmem "go.orx.me/apps/neo-box/internal/repo/telegram/memory"
	"go.orx.me/apps/neo-box/internal/telegram"
	"go.orx.me/apps/neo-box/internal/telegram/tgtest"
	"go.orx.me/apps/neo-box/internal/wasabi"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type stubAuthRepo struct {
	users map[string]*neoboxv1.User
}

func (s *stubAuthRepo) CountUsers(context.Context) (int64, error) { return int64(len(s.users)), nil }
func (s *stubAuthRepo) ListUsers(context.Context) ([]*neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) CreateUser(context.Context, *neoboxv1.User, string) error { panic("unused") }
func (s *stubAuthRepo) UpdateUserPassword(context.Context, string, string, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) UpdateUserProfile(context.Context, string, string, *string, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) SetUserDisabled(context.Context, string, bool, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) FindUserByUsername(context.Context, string) (*neoboxv1.User, string, error) {
	panic("unused")
}
func (s *stubAuthRepo) FindUserByID(context.Context, string) (*neoboxv1.User, string, error) {
	panic("unused")
}
func (s *stubAuthRepo) FindUserByExternalID(context.Context, string, string) (*neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) GetUser(_ context.Context, id string) (*neoboxv1.User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, authrepo.ErrUserNotFound
	}
	return u, nil
}
func (s *stubAuthRepo) CreateSession(context.Context, *authrepo.Session) error { panic("unused") }
func (s *stubAuthRepo) LookupSession(context.Context, string, time.Time) (*authrepo.Session, *neoboxv1.User, error) {
	panic("unused")
}
func (s *stubAuthRepo) TouchSession(context.Context, string, time.Time) error { panic("unused") }
func (s *stubAuthRepo) RevokeSession(context.Context, string) error           { panic("unused") }

func newTestEnv(t *testing.T, mode string) (*Service, *tgtest.Server, *connmem.Store, *stubAuthRepo, context.CancelFunc) {
	t.Helper()
	const token = "123456:TEST_TOKEN"
	const secret = "shared-multi-pod-secret"
	fakeTG := tgtest.New(token, "neobox_test_bot")
	t.Cleanup(fakeTG.Close)

	tgStore := tgmem.New()
	connStore := connmem.New()
	connSvc := connection.NewService(connStore, nil)
	authStore := &stubAuthRepo{
		users: map[string]*neoboxv1.User{
			"u1": {Id: "u1", Username: "alice", DisplayName: "Alice", Role: "user"},
			"u2": {Id: "u2", Username: "bob", DisplayName: "Bob", Role: "user"},
			"u3": {Id: "u3", Username: "disabled_user", Disabled: true},
		},
	}

	svc, err := New(Config{
		BotToken:       token,
		APIEndpoint:    fakeTG.URL,
		Mode:           mode,
		WebhookURL:     "https://neobox.example.com/api/telegram/webhook",
		WebhookSecret:  secret,
		AutoSetWebhook: true,
		DashboardURL:   "https://neobox.example.com",
		Workers:        2,
	}, tgStore, authStore, connSvc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := svc.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		svc.Wait()
	})
	return svc, fakeTG, connStore, authStore, cancel
}

func postWebhook(t *testing.T, svc *Service, secret string, u telegram.Update) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/webhook", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	rec := httptest.NewRecorder()
	svc.HandleWebhook(rec, req)
	return rec
}

func waitForCount(t *testing.T, want int, fn func() int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for count %d, got %d", want, fn())
}

func TestConfigValidation(t *testing.T) {
	tgStore := tgmem.New()
	connSvc := connection.NewService(connmem.New(), nil)
	authStore := &stubAuthRepo{}

	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing token", Config{Mode: ModeWebhook, WebhookURL: "https://x", WebhookSecret: "s"}},
		{"webhook missing url", Config{BotToken: "1:a", Mode: ModeWebhook, WebhookSecret: "s"}},
		{"webhook missing secret", Config{BotToken: "1:a", Mode: ModeWebhook, WebhookURL: "https://x"}},
		{"invalid mode", Config{BotToken: "1:a", Mode: "invalid"}},
	}
	for _, tc := range cases {
		if _, err := New(tc.cfg, tgStore, authStore, connSvc); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestWebhookSecurityAndBootstrap(t *testing.T) {
	svc, fakeTG, _, _, _ := newTestEnv(t, ModeWebhook)

	if got := svc.BotUsername(); got != "neobox_test_bot" {
		t.Fatalf("BotUsername = %q, want neobox_test_bot", got)
	}
	if len(fakeTG.Webhooks()) != 1 || fakeTG.Webhooks()[0].SecretToken != "shared-multi-pod-secret" {
		t.Fatalf("unexpected setWebhook calls: %+v", fakeTG.Webhooks())
	}
	if len(fakeTG.Commands()) == 0 {
		t.Fatal("expected setMyCommands to be called")
	}

	// Missing or wrong secret is rejected.
	if rec := postWebhook(t, svc, "", telegram.Update{UpdateID: 1}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing secret status = %d, want 401", rec.Code)
	}
	if rec := postWebhook(t, svc, "wrong", telegram.Update{UpdateID: 1}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d, want 401", rec.Code)
	}

	// Invalid JSON returns 400.
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/webhook", strings.NewReader("{bad"))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "shared-multi-pod-secret")
	rec := httptest.NewRecorder()
	svc.HandleWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json status = %d, want 400", rec.Code)
	}
}

func TestLinkViewConnectionsDetailStatusAndUnbind(t *testing.T) {
	svc, fakeTG, connStore, authStore, _ := newTestEnv(t, ModeWebhook)
	ctx := context.Background()

	// Seed 4 provider connections for u1 (plus 1 for u2 for isolation).
	now := time.Now().UTC()
	seed := []*connrepo.Connection{
		{
			ID: "11111111-1111-1111-1111-111111111111", UserID: "u1", Provider: nocodb.ProviderType,
			Name: "Prod NocoDB <main>", Status: connrepo.StatusOK, StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now,
			Config:           json.RawMessage(`{"base_url":"https://nocodb.example.com","requests_per_second":10}`),
			SecretCiphertext: "SHOULD_NEVER_APPEAR_NOCODB_SECRET",
		},
		{
			ID: "22222222-2222-2222-2222-222222222222", UserID: "u1", Provider: wasabi.ProviderType,
			Name: "Backup Wasabi", Status: connrepo.StatusError, StatusMessage: "403 forbidden", StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now,
			Config:           json.RawMessage(`{"access_key_id":"AKIA_WASABI","price_per_tb_month":6.99,"billing_cycle_anchor":"2026-01-05","cost_estimate_enabled":true,"budget_usd":50}`),
			SecretCiphertext: "SHOULD_NEVER_APPEAR_WASABI_SECRET",
		},
		{
			ID: "33333333-3333-3333-3333-333333333333", UserID: "u1", Provider: cloudflare.ProviderType,
			Name: "Edge CF", Status: connrepo.StatusOK, StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now,
			Config:           json.RawMessage(`{"account_id":"0123456789abcdef0123456789abcdef"}`),
			SecretCiphertext: "SHOULD_NEVER_APPEAR_CF_SECRET",
		},
		{
			ID: "44444444-4444-4444-4444-444444444444", UserID: "u1", Provider: googleworkspace.ProviderType,
			Name: "Corp Workspace", Status: connrepo.StatusOK, StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now,
			Config:           json.RawMessage(`{"service_account_email":"sa@proj.iam.gserviceaccount.com","client_id":"12345"}`),
			SecretCiphertext: "SHOULD_NEVER_APPEAR_GWS_SECRET",
		},
		{
			ID: "99999999-9999-9999-9999-999999999999", UserID: "u2", Provider: cloudflare.ProviderType,
			Name: "Bob Private CF", Status: connrepo.StatusOK, StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now,
			Config: json.RawMessage(`{"account_id":"ffffffffffffffffffffffffffffffff"}`),
		},
	}
	for _, c := range seed {
		if err := connStore.Create(ctx, c); err != nil {
			t.Fatalf("seed connection: %v", err)
		}
	}

	// Group chat messages must be ignored completely.
	rec := postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      telegram.Chat{ID: -1001, Type: "group"},
			From:      &telegram.User{ID: 7001, FirstName: "Alice", Username: "alice_tg"},
			Text:      "/connections",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("group message status = %d", rec.Code)
	}

	// Unbound private /connections prompts linking.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			MessageID: 2,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001, FirstName: "Alice", Username: "alice_tg"},
			Text:      "/connections",
		},
	})
	waitForCount(t, 1, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[0].Text, "not linked") {
		t.Fatalf("expected unbound prompt, got: %s", fakeTG.SentMessages()[0].Text)
	}

	// Create link token and link via /start <token>.
	link, err := svc.CreateLinkToken(ctx, "u1")
	if err != nil {
		t.Fatalf("CreateLinkToken: %v", err)
	}
	if !strings.HasPrefix(link.DeepLink, "https://t.me/neobox_test_bot?start=") {
		t.Fatalf("unexpected DeepLink: %q", link.DeepLink)
	}
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 3,
		Message: &telegram.Message{
			MessageID: 3,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001, FirstName: "Alice", Username: "alice_tg"},
			Text:      link.Command,
		},
	})
	waitForCount(t, 2, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[1].Text, "Linked to Neo Box account") {
		t.Fatalf("expected link confirmation, got: %s", fakeTG.SentMessages()[1].Text)
	}

	// Reusing the same link token must fail.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 4,
		Message: &telegram.Message{
			MessageID: 4,
			Chat:      telegram.Chat{ID: 7002, Type: "private"},
			From:      &telegram.User{ID: 7002, FirstName: "Other"},
			Text:      link.Command,
		},
	})
	waitForCount(t, 3, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[2].Text, "invalid or has expired") {
		t.Fatalf("expected expired/invalid link message, got: %s", fakeTG.SentMessages()[2].Text)
	}

	// /connections lists u1's 4 connections and never Bob's.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 5,
		Message: &telegram.Message{
			MessageID: 5,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001, FirstName: "Alice"},
			Text:      "/connections",
		},
	})
	waitForCount(t, 4, func() int { return len(fakeTG.SentMessages()) })
	listMsg := fakeTG.SentMessages()[3]
	if !strings.Contains(listMsg.Text, "Showing 1–4 of 4") || !strings.Contains(listMsg.Text, "Prod NocoDB &lt;main&gt;") {
		t.Fatalf("unexpected list message: %s", listMsg.Text)
	}
	if strings.Contains(listMsg.Text, "Bob Private CF") {
		t.Fatalf("leaked other user's connection: %s", listMsg.Text)
	}

	// Filter by Wasabi via callback.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 6,
		CallbackQuery: &telegram.CallbackQuery{
			ID:      "cb1",
			From:    telegram.User{ID: 7001, FirstName: "Alice"},
			Message: &telegram.Message{MessageID: 104, Chat: telegram.Chat{ID: 7001, Type: "private"}},
			Data:    "cl:w:1",
		},
	})
	waitForCount(t, 1, func() int { return len(fakeTG.EditedMessages()) })
	if !strings.Contains(fakeTG.EditedMessages()[0].Text, "Connections · Wasabi") || !strings.Contains(fakeTG.EditedMessages()[0].Text, "Backup Wasabi") {
		t.Fatalf("unexpected filtered list: %s", fakeTG.EditedMessages()[0].Text)
	}

	// Inspect detail of all 4 connections; check non-secret fields and absence of secret ciphertext.
	detailChecks := []struct {
		id           string
		wantSubstr   string
		forbidSubstr string
	}{
		{"11111111-1111-1111-1111-111111111111", "https://nocodb.example.com", "SHOULD_NEVER_APPEAR_NOCODB_SECRET"},
		{"22222222-2222-2222-2222-222222222222", "AKIA_WASABI", "SHOULD_NEVER_APPEAR_WASABI_SECRET"},
		{"33333333-3333-3333-3333-333333333333", "0123456789abcdef0123456789abcdef", "SHOULD_NEVER_APPEAR_CF_SECRET"},
		{"44444444-4444-4444-4444-444444444444", "sa@proj.iam.gserviceaccount.com", "SHOULD_NEVER_APPEAR_GWS_SECRET"},
	}
	for i, dc := range detailChecks {
		cbData := fmt.Sprintf("cv:%s:a:1", dc.id)
		if len(cbData) > telegram.MaxCallbackDataBytes {
			t.Fatalf("callback_data too long (%d): %s", len(cbData), cbData)
		}
		postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
			UpdateID: int64(10 + i),
			CallbackQuery: &telegram.CallbackQuery{
				ID:      fmt.Sprintf("cb-detail-%d", i),
				From:    telegram.User{ID: 7001},
				Message: &telegram.Message{MessageID: 104, Chat: telegram.Chat{ID: 7001, Type: "private"}},
				Data:    cbData,
			},
		})
		waitForCount(t, 2+i, func() int { return len(fakeTG.EditedMessages()) })
		ed := fakeTG.EditedMessages()[1+i]
		if !strings.Contains(ed.Text, dc.wantSubstr) {
			t.Fatalf("detail %s missing %q in:\n%s", dc.id, dc.wantSubstr, ed.Text)
		}
		if strings.Contains(ed.Text, dc.forbidSubstr) {
			t.Fatalf("detail %s leaked secret %q", dc.id, dc.forbidSubstr)
		}
		// Must include Open in Neo Box URL button.
		foundURL := false
		for _, row := range ed.ReplyMarkup.InlineKeyboard {
			for _, btn := range row {
				if btn.URL == "https://neobox.example.com/connections/"+dc.id {
					foundURL = true
				}
			}
		}
		if !foundURL {
			t.Fatalf("detail %s missing dashboard URL button", dc.id)
		}
	}

	// Attempt to view Bob's connection by ID must be denied (treated as not found).
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 20,
		CallbackQuery: &telegram.CallbackQuery{
			ID:      "cb-cross",
			From:    telegram.User{ID: 7001},
			Message: &telegram.Message{MessageID: 104, Chat: telegram.Chat{ID: 7001, Type: "private"}},
			Data:    "cv:99999999-9999-9999-9999-999999999999:a:1",
		},
	})
	waitForCount(t, 6, func() int { return len(fakeTG.CallbackAnswers()) })
	if !strings.Contains(fakeTG.CallbackAnswers()[5].Text, "no longer exists") {
		t.Fatalf("expected not found answer for cross-user view, got: %+v", fakeTG.CallbackAnswers()[5])
	}

	// /status shows summary and failing Wasabi connection.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 21,
		Message: &telegram.Message{
			MessageID: 21,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001},
			Text:      "/status",
		},
	})
	waitForCount(t, 5, func() int { return len(fakeTG.SentMessages()) })
	statusMsg := fakeTG.SentMessages()[4].Text
	if !strings.Contains(statusMsg, "Total: <b>4</b>") || !strings.Contains(statusMsg, "Backup Wasabi") || !strings.Contains(statusMsg, "403 forbidden") {
		t.Fatalf("unexpected /status text:\n%s", statusMsg)
	}

	// Disabled user is immediately blocked.
	authStore.users["u1"].Disabled = true
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 22,
		Message: &telegram.Message{
			MessageID: 22,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001},
			Text:      "/connections",
		},
	})
	waitForCount(t, 6, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[5].Text, "disabled") {
		t.Fatalf("expected disabled notice, got: %s", fakeTG.SentMessages()[5].Text)
	}
	authStore.users["u1"].Disabled = false

	// /unbind + confirm disconnects the account.
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 23,
		Message: &telegram.Message{
			MessageID: 23,
			Chat:      telegram.Chat{ID: 7001, Type: "private"},
			From:      &telegram.User{ID: 7001},
			Text:      "/unbind",
		},
	})
	waitForCount(t, 7, func() int { return len(fakeTG.SentMessages()) })
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 24,
		CallbackQuery: &telegram.CallbackQuery{
			ID:      "cb-unbind",
			From:    telegram.User{ID: 7001},
			Message: &telegram.Message{MessageID: 107, Chat: telegram.Chat{ID: 7001, Type: "private"}},
			Data:    "ub:y",
		},
	})
	waitForCount(t, 7, func() int { return len(fakeTG.CallbackAnswers()) })
	if _, err := svc.GetBinding(ctx, "u1"); err == nil {
		t.Fatal("expected binding to be deleted after ub:y")
	}
}

func TestConnectionsPagination(t *testing.T) {
	svc, fakeTG, connStore, _, _ := newTestEnv(t, ModeWebhook)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		_ = connStore.Create(ctx, &connrepo.Connection{
			ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), UserID: "u1",
			Provider: cloudflare.ProviderType, Name: fmt.Sprintf("CF %02d", i+1),
			Status: connrepo.StatusOK, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
			Config: json.RawMessage(`{"account_id":"0123456789abcdef0123456789abcdef"}`),
		})
	}
	link, _ := svc.CreateLinkToken(ctx, "u1")
	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 1, Chat: telegram.Chat{ID: 7001, Type: "private"},
			From: &telegram.User{ID: 7001}, Text: link.Command,
		},
	})
	waitForCount(t, 1, func() int { return len(fakeTG.SentMessages()) })

	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 2,
		Message: &telegram.Message{
			MessageID: 2, Chat: telegram.Chat{ID: 7001, Type: "private"},
			From: &telegram.User{ID: 7001}, Text: "/connections",
		},
	})
	waitForCount(t, 2, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[1].Text, "Showing 1–8 of 10") {
		t.Fatalf("page 1 text: %s", fakeTG.SentMessages()[1].Text)
	}

	postWebhook(t, svc, "shared-multi-pod-secret", telegram.Update{
		UpdateID: 3,
		CallbackQuery: &telegram.CallbackQuery{
			ID: "cb-p2", From: telegram.User{ID: 7001},
			Message: &telegram.Message{MessageID: 102, Chat: telegram.Chat{ID: 7001, Type: "private"}},
			Data:    "cl:a:2",
		},
	})
	waitForCount(t, 1, func() int { return len(fakeTG.EditedMessages()) })
	if !strings.Contains(fakeTG.EditedMessages()[0].Text, "Showing 9–10 of 10") {
		t.Fatalf("page 2 text: %s", fakeTG.EditedMessages()[0].Text)
	}
}

func TestPollingMode(t *testing.T) {
	_, fakeTG, _, _, _ := newTestEnv(t, ModePolling)
	if fakeTG.DeleteWebhookCount() == 0 {
		t.Fatal("expected deleteWebhook in polling mode")
	}
	fakeTG.EnqueueUpdates(telegram.Update{
		UpdateID: 100,
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      telegram.Chat{ID: 8001, Type: "private"},
			From:      &telegram.User{ID: 8001},
			Text:      "/help",
		},
	})
	waitForCount(t, 1, func() int { return len(fakeTG.SentMessages()) })
	if !strings.Contains(fakeTG.SentMessages()[0].Text, "/connections") {
		t.Fatalf("unexpected /help response: %s", fakeTG.SentMessages()[0].Text)
	}
}
