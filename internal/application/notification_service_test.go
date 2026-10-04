package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"go.orx.me/apps/neo-box/internal/notify"
	notifyrepo "go.orx.me/apps/neo-box/internal/repo/notify"
	"go.orx.me/apps/neo-box/internal/repo/notify/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// newNotificationServer serves a fake Bot API that accepts chat "ok" and
// rejects every other chat.
func newNotificationServer(t *testing.T) (*NotificationServiceServer, *memory.Store, *[]string) {
	t.Helper()
	var sent []string
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(body, &m)
		if m.ChatID != "ok" {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
			return
		}
		sent = append(sent, strings.TrimPrefix(r.URL.Path, "/bot")+" "+m.Text)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(bot.Close)
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	svc := notify.NewService(store, cipher, "")
	svc.Register(&notify.Telegram{Endpoint: bot.URL})
	srv := NewNotificationServiceServer()
	srv.SetService(svc)
	return srv, store, &sent
}

func telegramCreate(name, chat, token string) *connect.Request[neoboxv1.CreateNotificationChannelRequest] {
	return connect.NewRequest(&neoboxv1.CreateNotificationChannelRequest{
		Name: name,
		Settings: &neoboxv1.CreateNotificationChannelRequest_Telegram{
			Telegram: &neoboxv1.TelegramChannelSettings{ChatId: chat, BotToken: token},
		},
	})
}

func TestNotificationChannels(t *testing.T) {
	srv, _, sent := newNotificationServer(t)
	ctx := asUser("u1")

	created, err := srv.CreateNotificationChannel(ctx, telegramCreate("phone", "ok", "111:SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	ch := created.Msg.GetChannel()
	raw, _ := protojson.Marshal(ch)
	if strings.Contains(string(raw), "SECRET") || ch.GetTelegram().GetChatId() != "ok" || !ch.GetEnabled() {
		t.Fatalf("created = %s", raw)
	}

	test, err := srv.TestNotificationChannel(ctx, connect.NewRequest(&neoboxv1.TestNotificationChannelRequest{Id: ch.GetId()}))
	if err != nil || test.Msg.GetError() != "" || test.Msg.GetChannel().GetLastSentAt() == nil {
		t.Fatalf("test = %+v, %v", test, err)
	}
	if len(*sent) != 1 || !strings.HasPrefix((*sent)[0], "111:SECRET/sendMessage <b>ℹ️ Neo Box test message</b>") {
		t.Fatalf("sent = %v", *sent)
	}

	// Moving to a chat the bot can't reach keeps the token, and the test
	// reports the failure on the channel.
	updated, err := srv.UpdateNotificationChannel(ctx, connect.NewRequest(&neoboxv1.UpdateNotificationChannelRequest{
		Id: ch.GetId(), Name: "group", Enabled: false,
		Settings: &neoboxv1.UpdateNotificationChannelRequest_Telegram{Telegram: &neoboxv1.TelegramChannelSettings{ChatId: "-1009"}},
	}))
	if err != nil || updated.Msg.GetChannel().GetName() != "group" || updated.Msg.GetChannel().GetEnabled() {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	test, err = srv.TestNotificationChannel(ctx, connect.NewRequest(&neoboxv1.TestNotificationChannelRequest{Id: ch.GetId()}))
	if err != nil || test.Msg.GetError() != "telegram: Bad Request: chat not found" || test.Msg.GetChannel().GetLastError() == "" {
		t.Fatalf("failed test = %+v, %v", test, err)
	}

	for name, call := range map[string]func() error{
		"another user's channel": func() error {
			_, err := srv.TestNotificationChannel(asUser("u2"), connect.NewRequest(&neoboxv1.TestNotificationChannelRequest{Id: ch.GetId()}))
			return err
		},
		"delete by another user": func() error {
			_, err := srv.DeleteNotificationChannel(asUser("u2"), connect.NewRequest(&neoboxv1.DeleteNotificationChannelRequest{Id: ch.GetId()}))
			return err
		},
	} {
		if connect.CodeOf(call()) != connect.CodeNotFound {
			t.Errorf("%s: want NotFound", name)
		}
	}
	for name, req := range map[string]*connect.Request[neoboxv1.CreateNotificationChannelRequest]{
		"no token":  telegramCreate("x", "ok", ""),
		"no chat":   telegramCreate("x", "", "1:x"),
		"bad token": telegramCreate("x", "ok", "not-a-token"),
		"no name":   telegramCreate(" ", "ok", "1:x"),
	} {
		if _, err := srv.CreateNotificationChannel(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}

	list, _ := srv.ListNotificationChannels(asUser("u2"), connect.NewRequest(&neoboxv1.ListNotificationChannelsRequest{}))
	if len(list.Msg.GetChannels()) != 0 {
		t.Fatalf("u2 sees %d channels", len(list.Msg.GetChannels()))
	}
	if _, err := srv.DeleteNotificationChannel(ctx, connect.NewRequest(&neoboxv1.DeleteNotificationChannelRequest{Id: ch.GetId()})); err != nil {
		t.Fatal(err)
	}
	list, _ = srv.ListNotificationChannels(ctx, connect.NewRequest(&neoboxv1.ListNotificationChannelsRequest{}))
	if len(list.Msg.GetChannels()) != 0 {
		t.Fatalf("channels left after delete: %d", len(list.Msg.GetChannels()))
	}
}

func TestListAlerts(t *testing.T) {
	srv, store, _ := newNotificationServer(t)
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	for i, a := range []*notifyrepo.Alert{
		{ID: "a1", UserID: "u1", Key: "k1", Source: "nocodb", Kind: "snapshot_failed", Severity: notifyrepo.SeverityWarning, Title: "older"},
		{ID: "a2", UserID: "u1", Key: "k2", Source: "connection", Kind: "connection_error", Severity: notifyrepo.SeverityCritical, Title: "newer", Link: "/connections/c1"},
		{ID: "a3", UserID: "u2", Key: "k1", Title: "someone else's"},
	} {
		a.CreatedAt = at.Add(time.Duration(i) * time.Minute)
		_, _ = store.CreateAlert(context.Background(), a)
	}
	_ = store.SetAlertDelivery(context.Background(), "a2", at.Add(time.Hour), "")

	resp, err := srv.ListAlerts(asUser("u1"), connect.NewRequest(&neoboxv1.ListAlertsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Msg.GetAlerts()
	if len(got) != 2 || got[0].GetTitle() != "newer" || got[0].GetSeverity() != neoboxv1.AlertSeverity_ALERT_SEVERITY_CRITICAL ||
		got[0].GetDeliveredAt() == nil || got[0].GetLink() != "/connections/c1" || got[1].GetDeliveredAt() != nil {
		t.Fatalf("alerts = %+v", got)
	}
	resp, _ = srv.ListAlerts(asUser("u1"), connect.NewRequest(&neoboxv1.ListAlertsRequest{Limit: 1}))
	if len(resp.Msg.GetAlerts()) != 1 {
		t.Fatalf("limit ignored: %d", len(resp.Msg.GetAlerts()))
	}
}
