package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEndpoint = "https://api.telegram.org"

// APIError is a non-ok response returned by the Telegram Bot API.
type APIError struct {
	Method      string
	ErrorCode   int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	if e.Method != "" {
		return fmt.Sprintf("telegram %s: %s (code %d)", e.Method, e.Description, e.ErrorCode)
	}
	return fmt.Sprintf("telegram: %s (code %d)", e.Description, e.ErrorCode)
}

// IsMessageNotModified reports whether err is Telegram's benign 400 error
// when editMessageText is called with identical text and markup.
func IsMessageNotModified(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(apiErr.Description), "message is not modified")
}

// Client calls the Telegram Bot API over net/http.
type Client struct {
	Token    string
	Endpoint string
	HTTP     *http.Client
	// sleep is swapped in tests.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewClient constructs a Bot API client. Empty endpoint uses
// https://api.telegram.org.
func NewClient(token, endpoint string) *Client {
	return &Client{
		Token:    token,
		Endpoint: endpoint,
		HTTP:     &http.Client{Timeout: 45 * time.Second},
	}
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var out User
	if err := c.call(ctx, "getMe", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) SetWebhook(ctx context.Context, req SetWebhookRequest) error {
	return c.call(ctx, "setWebhook", req, nil)
}

func (c *Client) DeleteWebhook(ctx context.Context, dropPending bool) error {
	return c.call(ctx, "deleteWebhook", map[string]any{
		"drop_pending_updates": dropPending,
	}, nil)
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{
		"commands": cmds,
	}, nil)
}

func (c *Client) GetUpdates(ctx context.Context, req GetUpdatesRequest) ([]Update, error) {
	var out []Update
	if err := c.call(ctx, "getUpdates", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error) {
	var out Message
	if err := c.call(ctx, "sendMessage", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EditMessageText updates an existing message. Telegram's "message is not
// modified" response is treated as success.
func (c *Client) EditMessageText(ctx context.Context, req EditMessageTextRequest) error {
	err := c.call(ctx, "editMessageText", req, nil)
	if IsMessageNotModified(err) {
		return nil
	}
	return err
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, req AnswerCallbackQueryRequest) error {
	return c.call(ctx, "answerCallbackQuery", req, nil)
}

func (c *Client) call(ctx context.Context, method string, payload any, result any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode telegram %s request: %w", method, err)
		}
	} else {
		body = []byte("{}")
	}

	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	target := strings.TrimRight(endpoint, "/") + "/bot" + c.Token + "/" + method

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = sleepCtx
	}

	// Retry once on 429 honoring Telegram's retry_after.
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return redact(err, c.Token)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return redact(err, c.Token)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()

		var envelope struct {
			OK          bool            `json:"ok"`
			Result      json.RawMessage `json:"result"`
			ErrorCode   int             `json:"error_code"`
			Description string          `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
		}
		if envelope.OK {
			if result != nil && len(envelope.Result) > 0 {
				if err := json.Unmarshal(envelope.Result, result); err != nil {
					return fmt.Errorf("decode telegram %s result: %w", method, err)
				}
			}
			return nil
		}
		if envelope.ErrorCode == http.StatusTooManyRequests && attempt == 0 {
			wait := time.Duration(min(max(envelope.Parameters.RetryAfter, 1), 30)) * time.Second
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return &APIError{
			Method:      method,
			ErrorCode:   envelope.ErrorCode,
			Description: envelope.Description,
			RetryAfter:  envelope.Parameters.RetryAfter,
		}
	}
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
