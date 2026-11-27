package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRetryOn429AndRedactsToken(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  429,
				"description": "Too Many Requests",
				"parameters":  map[string]any{"retry_after": 2},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": User{ID: 1, IsBot: true, Username: "neobox_bot"},
		})
	}))
	defer srv.Close()

	var slept time.Duration
	c := NewClient("123456:SECRET_TOKEN", srv.URL)
	c.sleep = func(_ context.Context, d time.Duration) error {
		slept = d
		return nil
	}

	me, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if me.Username != "neobox_bot" {
		t.Fatalf("Username = %q, want neobox_bot", me.Username)
	}
	if slept != 2*time.Second {
		t.Fatalf("slept = %v, want 2s", slept)
	}

	// Network errors must never leak the bot token.
	closedURL := srv.URL
	srv.Close()
	c2 := NewClient("123456:VERY_SECRET", closedURL)
	_, err = c2.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected error against closed server")
	}
	if strings.Contains(err.Error(), "VERY_SECRET") {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestEditMessageNotModifiedIsIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  400,
			"description": "Bad Request: message is not modified: specified new message content and reply markup are exactly the same",
		})
	}))
	defer srv.Close()

	c := NewClient("123:ABC", srv.URL)
	err := c.EditMessageText(context.Background(), EditMessageTextRequest{
		ChatID: 1, MessageID: 2, Text: "same",
	})
	if err != nil {
		t.Fatalf("EditMessageText returned error for message not modified: %v", err)
	}
}

func TestTruncateAndEscape(t *testing.T) {
	if got := Escape("<b>a & b</b>"); got != "&lt;b&gt;a &amp; b&lt;/b&gt;" {
		t.Fatalf("Escape = %q", got)
	}
	if got := Truncate("你好世界", 2); got != "你好…" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("abc", 3); got != "abc" {
		t.Fatalf("Truncate exact = %q", got)
	}
}
