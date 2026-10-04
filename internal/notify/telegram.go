package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// TelegramType is the channel type of Telegram chats.
const TelegramType = "telegram"

// TelegramConfig is a Telegram channel's non-secret settings.
type TelegramConfig struct {
	// ChatID is a numeric chat ID (negative for groups) or @channelname.
	ChatID string `json:"chat_id"`
}

// TelegramSecret is a Telegram channel's secret settings.
type TelegramSecret struct {
	BotToken string `json:"bot_token"`
}

// maxTelegramBody keeps a message under Telegram's 4096-character limit.
const maxTelegramBody = 3500

// Telegram sends messages through the Bot API's sendMessage.
type Telegram struct {
	// Endpoint is the Bot API host; empty means https://api.telegram.org.
	Endpoint string
	HTTP     *http.Client
	// sleep is swapped in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

func (t *Telegram) Type() string { return TelegramType }

func (t *Telegram) Check(config, secret json.RawMessage) error {
	var (
		cfg TelegramConfig
		sec TelegramSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode telegram config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode telegram secret: %w", err)
	}
	if strings.TrimSpace(cfg.ChatID) == "" {
		return errors.New("chat_id is required")
	}
	if !strings.Contains(sec.BotToken, ":") {
		return errors.New("bot_token looks wrong: it should be like 123456:ABC-DEF…, from @BotFather")
	}
	return nil
}

func (t *Telegram) Send(ctx context.Context, config, secret json.RawMessage, m Message) error {
	var (
		cfg TelegramConfig
		sec TelegramSecret
	)
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("decode telegram config: %w", err)
	}
	if err := json.Unmarshal(secret, &sec); err != nil {
		return fmt.Errorf("decode telegram secret: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":                  cfg.ChatID,
		"text":                     telegramText(m),
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = "https://api.telegram.org"
	}
	target := strings.TrimRight(endpoint, "/") + "/bot" + sec.BotToken + "/sendMessage"
	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	sleep := t.sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	// One retry, after Telegram's requested pause, on 429.
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return redact(err, sec.BotToken)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			// The request URL holds the token; keep it out of errors.
			return redact(err, sec.BotToken)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		var out struct {
			OK          bool   `json:"ok"`
			ErrorCode   int    `json:"error_code"`
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return fmt.Errorf("telegram: HTTP %d", resp.StatusCode)
		}
		if out.OK {
			return nil
		}
		if out.ErrorCode == http.StatusTooManyRequests && attempt == 0 {
			wait := time.Duration(min(max(out.Parameters.RetryAfter, 1), 30)) * time.Second
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("telegram: %s", out.Description)
	}
}

// telegramText renders a message as Telegram HTML: a bold title with a
// severity mark, the body, and a link.
func telegramText(m Message) string {
	mark := map[Severity]string{Info: "ℹ️", Warning: "⚠️", Critical: "🚨"}[m.Severity]
	var b strings.Builder
	b.WriteString("<b>")
	if mark != "" {
		b.WriteString(mark + " ")
	}
	b.WriteString(html.EscapeString(m.Title))
	b.WriteString("</b>")
	if body := truncate(m.Body, maxTelegramBody); body != "" {
		b.WriteString("\n")
		b.WriteString(html.EscapeString(body))
	}
	if m.URL != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s\">Open in Neo Box</a>", html.EscapeString(m.URL))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func redact(err error, token string) error {
	if token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
