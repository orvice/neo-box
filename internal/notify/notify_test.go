package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/notify"
	"go.orx.me/apps/neo-box/internal/repo/notify/memory"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

func TestTelegramSend(t *testing.T) {
	var (
		calls int
		got   map[string]any
		path  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		if calls == 1 {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 3","parameters":{"retry_after":3}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()
	var slept time.Duration
	tg := &Telegram{Endpoint: srv.URL, sleep: func(_ context.Context, d time.Duration) error { slept = d; return nil }}

	err := tg.Send(context.Background(), json.RawMessage(`{"chat_id":"-100"}`), json.RawMessage(`{"bot_token":"123:ABC"}`), Message{
		Severity: Warning, Title: `Snapshot of "A<B>" failed`, Body: "x & y", URL: "https://neo.example/c?a=1&b=2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || slept != 3*time.Second || path != "/bot123:ABC/sendMessage" {
		t.Fatalf("calls=%d slept=%v path=%s", calls, slept, path)
	}
	want := "<b>⚠️ Snapshot of &#34;A&lt;B&gt;&#34; failed</b>\nx &amp; y\n<a href=\"https://neo.example/c?a=1&amp;b=2\">Open in Neo Box</a>"
	if got["text"] != want || got["chat_id"] != "-100" || got["parse_mode"] != "HTML" {
		t.Fatalf("body = %v", got)
	}
}

func TestTelegramErrorsHideToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	tg := &Telegram{Endpoint: srv.URL}
	err := tg.Send(context.Background(), json.RawMessage(`{"chat_id":"1"}`), json.RawMessage(`{"bot_token":"999:SECRET"}`), Message{Title: "t"})
	if err == nil || err.Error() != "telegram: Bad Request: chat not found" {
		t.Fatalf("err = %v", err)
	}
	srv.Close()
	// Unreachable: the transport error names the URL, which holds the token.
	err = tg.Send(context.Background(), json.RawMessage(`{"chat_id":"1"}`), json.RawMessage(`{"bot_token":"999:SECRET"}`), Message{Title: "t"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestTelegramCheck(t *testing.T) {
	tg := &Telegram{}
	for cfg, ok := range map[[2]string]bool{
		{`{"chat_id":"42"}`, `{"bot_token":"1:x"}`}:  true,
		{`{"chat_id":""}`, `{"bot_token":"1:x"}`}:    false,
		{`{"chat_id":"42"}`, `{"bot_token":"nope"}`}: false,
	} {
		if err := tg.Check(json.RawMessage(cfg[0]), json.RawMessage(cfg[1])); (err == nil) != ok {
			t.Errorf("Check(%s, %s) = %v", cfg[0], cfg[1], err)
		}
	}
}

// fakeSender records what it sends; fail makes chat "bad" fail.
type fakeSender struct {
	mu   sync.Mutex
	sent []string // chat: title
}

func (f *fakeSender) Type() string { return "fake" }
func (f *fakeSender) Check(config, _ json.RawMessage) error {
	if strings.Contains(string(config), `"invalid"`) {
		return errors.New("invalid config")
	}
	return nil
}
func (f *fakeSender) Send(_ context.Context, config, secret json.RawMessage, m Message) error {
	var cfg struct{ Chat string }
	var sec struct{ Token string }
	_ = json.Unmarshal(config, &cfg)
	_ = json.Unmarshal(secret, &sec)
	if sec.Token != "s3cret" {
		return errors.New("wrong secret: " + sec.Token)
	}
	if cfg.Chat == "bad" {
		return errors.New("chat not found")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, cfg.Chat+": "+m.Title+" "+m.URL)
	return nil
}
func (f *fakeSender) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func newTestService(t *testing.T) (*Service, *memory.Store, *fakeSender) {
	t.Helper()
	cipher, err := secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	s := NewService(store, cipher, "https://neo.example/")
	snd := &fakeSender{}
	s.Register(snd)
	return s, store, snd
}

type chat struct{ Chat string }
type token struct{ Token string }

func waitDelivered(t *testing.T, store *memory.Store, userID string) []*repo.Alert {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alerts, _ := store.ListAlerts(context.Background(), userID, 0)
		done := len(alerts) > 0
		for _, a := range alerts {
			if a.DeliveredAt.IsZero() && a.DeliveryError == "" {
				done = false
			}
		}
		if done {
			return alerts
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("alerts were not delivered")
	return nil
}

func TestNotifyDeliversOncePerKey(t *testing.T) {
	s, store, snd := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.Wait() })
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	phone, _ := s.CreateChannel(ctx, "u1", "fake", "phone", chat{"phone"}, token{"s3cret"})
	off, _ := s.CreateChannel(ctx, "u1", "fake", "off", chat{"off"}, token{"s3cret"})
	bad, _ := s.CreateChannel(ctx, "u1", "fake", "bad", chat{"bad"}, token{"s3cret"})
	if err := s.UpdateChannel(ctx, off, "off", false, chat{"off"}, nil); err != nil {
		t.Fatal(err)
	}

	alert := Alert{UserID: "u1", Source: "nocodb", Kind: "snapshot_failed", Key: "k1", Severity: Warning, Title: "Snapshot failed", Link: "/connections/c1"}
	s.Notify(ctx, alert)
	s.Notify(ctx, alert) // same key: dropped
	alerts := waitDelivered(t, store, "u1")

	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
	if got := snd.messages(); len(got) != 1 || got[0] != "phone: Snapshot failed https://neo.example/connections/c1" {
		t.Fatalf("sent = %v", got)
	}
	a := alerts[0]
	if a.DeliveredAt.IsZero() || a.DeliveryError != "bad: chat not found" {
		t.Fatalf("delivery = %v / %q", a.DeliveredAt, a.DeliveryError)
	}
	p, _ := store.GetChannel(ctx, "u1", phone.ID)
	b, _ := store.GetChannel(ctx, "u1", bad.ID)
	if p.LastSentAt.IsZero() || p.LastError != "" || b.LastError != "chat not found" {
		t.Fatalf("channel results: phone=%+v bad=%+v", p, b)
	}
}

func TestNotifyWithoutChannelsRecordsWhy(t *testing.T) {
	s, store, _ := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.Wait() })
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.Notify(ctx, Alert{UserID: "u2", Source: "connection", Kind: "connection_error", Key: "k", Severity: Critical, Title: "t"})
	alerts := waitDelivered(t, store, "u2")
	if !alerts[0].DeliveredAt.IsZero() || alerts[0].DeliveryError != "no enabled notification channels" {
		t.Fatalf("alert = %+v", alerts[0])
	}
}

func TestStartRedeliversPending(t *testing.T) {
	s, store, snd := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.Wait() })
	if _, err := s.CreateChannel(ctx, "u1", "fake", "phone", chat{"phone"}, token{"s3cret"}); err != nil {
		t.Fatal(err)
	}
	// Stored by a process that stopped before delivering it.
	_, _ = store.CreateAlert(ctx, &repo.Alert{ID: "a1", UserID: "u1", Key: "k", Title: "left over", CreatedAt: time.Now().Add(-time.Hour)})
	_, _ = store.CreateAlert(ctx, &repo.Alert{ID: "a2", UserID: "u1", Key: "old", Title: "too old", CreatedAt: time.Now().Add(-48 * time.Hour)})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(snd.messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := snd.messages(); len(got) != 1 || !strings.HasPrefix(got[0], "phone: left over") {
		t.Fatalf("sent = %v", got)
	}
}

func TestChannelSettings(t *testing.T) {
	s, _, snd := newTestService(t)
	ctx := context.Background()
	if _, err := s.CreateChannel(ctx, "u1", "nope", "x", chat{"a"}, token{"s3cret"}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown type = %v", err)
	}
	var cerr *CheckError
	if _, err := s.CreateChannel(ctx, "u1", "fake", "x", chat{"invalid"}, token{"s3cret"}); !errors.As(err, &cerr) {
		t.Fatalf("invalid config = %v", err)
	}
	c, err := s.CreateChannel(ctx, "u1", "fake", "phone", chat{"phone"}, token{"s3cret"})
	if err != nil || !c.Enabled || strings.Contains(c.SecretCiphertext, "s3cret") {
		t.Fatalf("created = %+v, %v", c, err)
	}
	// A nil secret keeps the stored one; the test message proves it.
	if err := s.UpdateChannel(ctx, c, "renamed", true, chat{"phone"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.TestChannel(ctx, c); err != nil {
		t.Fatalf("TestChannel: %v", err)
	}
	if got := snd.messages(); len(got) != 1 || !strings.Contains(got[0], "Neo Box test message https://neo.example/settings/notifications") {
		t.Fatalf("sent = %v", got)
	}
	// A new secret replaces it.
	if err := s.UpdateChannel(ctx, c, "renamed", true, chat{"phone"}, token{"other"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TestChannel(ctx, c); err == nil || !strings.Contains(err.Error(), "wrong secret: other") {
		t.Fatalf("TestChannel after new secret = %v", err)
	}
}
