// Package nocodb is a minimal REST client for self-hosted (OSS) NocoDB.
//
// It speaks the subset of the API that snapshots need: v2 for listing Bases
// (OSS has no workspaces, and the v3 base list is workspace-scoped) and v3 for
// base/table schema, paged records, and linked records. Requests are
// authenticated with the `xc-token` header, rate limited per client, and
// retried on 429 / 5xx.
package nocodb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultRPS        = 5
	defaultMaxRetries = 4
	defaultPageSize   = 200
	userAgent         = "neo-box"
	maxErrorBody      = 2048
)

// Client talks to one NocoDB instance with one API token.
type Client struct {
	baseURL    string
	token      string
	http       *http.Client
	limiter    *rate.Limiter
	maxRetries int
	// sleep is swapped in tests so retry backoff doesn't slow them down.
	sleep func(ctx context.Context, d time.Duration) error
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client (tests, custom transports).
func WithHTTPClient(c *http.Client) Option { return func(cl *Client) { cl.http = c } }

// WithRateLimit sets the maximum requests per second. <= 0 disables limiting.
func WithRateLimit(rps float64) Option {
	return func(cl *Client) {
		if rps <= 0 {
			cl.limiter = nil
			return
		}
		cl.limiter = rate.NewLimiter(rate.Limit(rps), 1)
	}
}

// New builds a client. baseURL is the instance root, e.g.
// "https://nocodb.example.com" (a trailing slash or /dashboard suffix is
// tolerated).
func New(baseURL, token string, opts ...Option) (*Client, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("nocodb: api token is required")
	}
	c := &Client{
		baseURL:    normalized,
		token:      strings.TrimSpace(token),
		http:       &http.Client{Timeout: 60 * time.Second},
		limiter:    rate.NewLimiter(defaultRPS, 1),
		maxRetries: defaultMaxRetries,
		sleep:      sleepCtx,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// NormalizeBaseURL validates and canonicalizes an instance URL.
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("nocodb: invalid base url %q (expected http(s)://host)", raw)
	}
	path := strings.TrimRight(u.Path, "/")
	// People paste the dashboard URL; the API lives at the root.
	path = strings.TrimSuffix(path, "/dashboard")
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// APIError is a non-2xx response from NocoDB.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("nocodb: %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound reports whether err is a 404 from NocoDB.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// Base is a NocoDB Base as listed by the v2 API.
type Base struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ListBases returns every Base visible to the token.
func (c *Client) ListBases(ctx context.Context) ([]Base, error) {
	var out struct {
		List []Base `json:"list"`
	}
	if err := c.getJSON(ctx, "/api/v2/meta/bases/", nil, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// GetBaseRaw returns the v3 base meta verbatim.
func (c *Client) GetBaseRaw(ctx context.Context, baseID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.getJSON(ctx, "/api/v3/meta/bases/"+url.PathEscape(baseID), nil, &raw)
	return raw, err
}

// TableSummary is one entry of the v3 table list.
type TableSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ListTables lists the tables of a Base.
func (c *Client) ListTables(ctx context.Context, baseID string) ([]TableSummary, error) {
	var out struct {
		List []TableSummary `json:"list"`
	}
	if err := c.getJSON(ctx, "/api/v3/meta/bases/"+url.PathEscape(baseID)+"/tables", nil, &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// Field is the part of a v3 field definition snapshots reason about.
type Field struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// IsLink reports whether the field holds relations to other records.
func (f Field) IsLink() bool {
	return f.Type == "Links" || f.Type == "LinkToAnotherRecord"
}

// Table is a v3 table schema: the parsed fields plus the verbatim JSON.
type Table struct {
	ID     string
	Title  string
	Fields []Field
	Raw    json.RawMessage
}

// GetTable returns the full v3 schema of one table.
func (c *Client) GetTable(ctx context.Context, baseID, tableID string) (*Table, error) {
	var raw json.RawMessage
	path := "/api/v3/meta/bases/" + url.PathEscape(baseID) + "/tables/" + url.PathEscape(tableID)
	if err := c.getJSON(ctx, path, nil, &raw); err != nil {
		return nil, err
	}
	var parsed struct {
		ID     string  `json:"id"`
		Title  string  `json:"title"`
		Fields []Field `json:"fields"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("nocodb: decode table %s: %w", tableID, err)
	}
	return &Table{ID: parsed.ID, Title: parsed.Title, Fields: parsed.Fields, Raw: raw}, nil
}

// Record is one v3 record: {"id": ..., "fields": {...}}. ID is kept raw
// because the primary key may be a number or a string.
type Record struct {
	ID     json.RawMessage            `json:"id"`
	Fields map[string]json.RawMessage `json:"fields"`
}

// IDString renders the record ID for use in URL paths.
func (r Record) IDString() string { return RawIDString(r.ID) }

// RawIDString renders a JSON-encoded record ID (number or string) as text.
func RawIDString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// RecordPage is one page of records.
type RecordPage struct {
	Records []Record
	HasNext bool
}

// ListRecords fetches one 1-based page of records.
func (c *Client) ListRecords(ctx context.Context, baseID, tableID string, page, pageSize int) (*RecordPage, error) {
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(pageSize))
	var out struct {
		Records []Record `json:"records"`
		Next    *string  `json:"next"`
	}
	path := "/api/v3/data/" + url.PathEscape(baseID) + "/" + url.PathEscape(tableID) + "/records"
	if err := c.getJSON(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &RecordPage{Records: out.Records, HasNext: out.Next != nil && *out.Next != ""}, nil
}

// ListLinkedIDs returns the IDs of every record linked to recordID through
// linkFieldID, following pagination.
func (c *Client) ListLinkedIDs(ctx context.Context, baseID, tableID, linkFieldID, recordID string) ([]json.RawMessage, error) {
	path := "/api/v3/data/" + url.PathEscape(baseID) + "/" + url.PathEscape(tableID) +
		"/links/" + url.PathEscape(linkFieldID) + "/" + url.PathEscape(recordID)
	var ids []json.RawMessage
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(defaultPageSize))
		var out struct {
			Records []struct {
				ID json.RawMessage `json:"id"`
			} `json:"records"`
			Next *string `json:"next"`
		}
		if err := c.getJSON(ctx, path, q, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Records {
			ids = append(ids, r.ID)
		}
		if out.Next == nil || *out.Next == "" || len(out.Records) == 0 {
			return ids, nil
		}
	}
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("nocodb: decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("xc-token", c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("nocodb: %s %s: %w", method, path, err)
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("nocodb: read %s: %w", path, readErr)
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return body, nil
			} else {
				apiErr := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Message: errorMessage(body)}
				if !retryable(resp.StatusCode) {
					return nil, apiErr
				}
				lastErr = apiErr
				if attempt < c.maxRetries {
					if err := c.sleep(ctx, retryDelay(resp, attempt)); err != nil {
						return nil, err
					}
					continue
				}
			}
		}
		if attempt >= c.maxRetries {
			return nil, lastErr
		}
		if err := c.sleep(ctx, backoff(attempt)); err != nil {
			return nil, err
		}
	}
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// retryDelay honors Retry-After; NocoDB's documented 429 cool-down is 30s.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if secs, err := strconv.Atoi(s); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return 30 * time.Second
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * 500 * time.Millisecond
}

func errorMessage(body []byte) string {
	var parsed struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		for _, s := range []string{parsed.Msg, parsed.Message, parsed.Error} {
			if s != "" {
				return s
			}
		}
	}
	body = bytes.TrimSpace(body)
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	return string(body)
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
