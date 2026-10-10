// Package cloudflare is a client for the parts of the Cloudflare API Neo Box
// uses (Zones, DNS records, Workers, Pages) plus the Cloudflare-specific
// pieces of Neo Box: connection settings, DNS record input, and the
// addresses of Workers and Pages projects.
//
// Requests carry the API token as a bearer token, share a rate limiter per
// connection, and time out. Reads are retried on 429, 5xx and network
// errors. Writes are retried only on 429 (Cloudflare refused them before
// doing anything); any other failure without a clear answer is an
// UncertainError and is never sent again, so a change is never made twice.
//
// API: https://developers.cloudflare.com/api/
package cloudflare

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

// DefaultEndpoint is the API v4 base URL.
const DefaultEndpoint = "https://api.cloudflare.com/client/v4"

const (
	// Cloudflare allows 1200 requests per 5 minutes per user, i.e. 4/s.
	defaultRPS   = 4
	defaultBurst = 50
	maxRetries   = 3
	maxRetryWait = 30 * time.Second
	maxPages     = 100
	userAgent    = "neo-box"
	maxErrorBody = 2048
)

// Client talks to the Cloudflare API with one API token.
type Client struct {
	http      *http.Client
	endpoint  string
	token     string
	limiter   *rate.Limiter
	baseDelay time.Duration
}

type Option func(*Client)

// WithEndpoint overrides DefaultEndpoint.
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		if endpoint != "" {
			c.endpoint = strings.TrimRight(endpoint, "/")
		}
	}
}

// WithHTTPClient replaces the default HTTP client (30s timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithLimiter shares a rate limiter, so several clients of one connection
// stay within Cloudflare's rate together.
func WithLimiter(l *rate.Limiter) Option { return func(c *Client) { c.limiter = l } }

// WithRetryDelay sets the first retry's delay; later ones double it.
func WithRetryDelay(d time.Duration) Option { return func(c *Client) { c.baseDelay = d } }

// NewLimiter returns a limiter at Cloudflare's per-user API rate.
func NewLimiter() *rate.Limiter { return rate.NewLimiter(defaultRPS, defaultBurst) }

func New(token string, opts ...Option) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("cloudflare: api token is required")
	}
	c := &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		endpoint:  DefaultEndpoint,
		token:     token,
		limiter:   NewLimiter(),
		baseDelay: 500 * time.Millisecond,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// --- errors ---

// ErrorDetail is one entry of Cloudflare's errors array.
type ErrorDetail struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// APIError is a response Cloudflare answered with failure.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Errors     []ErrorDetail
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cloudflare: %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message())
}

// Message is Cloudflare's explanation, for showing to a user.
func (e *APIError) Message() string {
	if len(e.Errors) == 0 {
		return http.StatusText(e.StatusCode)
	}
	parts := make([]string, 0, len(e.Errors))
	for _, d := range e.Errors {
		parts = append(parts, fmt.Sprintf("%s (%d)", d.Message, d.Code))
	}
	return strings.Join(parts, "; ")
}

func (e *APIError) hasCode(codes ...int) bool {
	for _, d := range e.Errors {
		for _, c := range codes {
			if d.Code == c {
				return true
			}
		}
	}
	return false
}

// Codes Cloudflare uses for a token it does not accept at all.
var invalidTokenCodes = []int{1000, 6003, 6111, 9106}

// IsAuthError reports whether Cloudflare rejected the token itself: it is
// invalid, expired or revoked.
func IsAuthError(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.StatusCode == http.StatusUnauthorized || e.hasCode(invalidTokenCodes...))
}

// IsPermissionDenied reports whether the token was accepted but may not do
// this (403).
func IsPermissionDenied(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == http.StatusForbidden && !IsAuthError(err)
}

// IsNotFound reports whether the object does not exist (or is not visible
// to the token).
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.StatusCode == http.StatusNotFound || e.hasCode(7003, 81044))
}

// IsRejected reports whether Cloudflare refused the request as invalid
// (400, 409, 422) for a reason other than the token.
func IsRejected(err error) bool {
	var e *APIError
	if !errors.As(err, &e) || IsAuthError(err) {
		return false
	}
	switch e.StatusCode {
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// UncertainError is a write whose outcome is unknown: it may have reached
// Cloudflare, but no clear answer came back.
type UncertainError struct{ Err error }

func (e *UncertainError) Error() string {
	return "cloudflare did not confirm the change, so it may or may not have been made: " + e.Err.Error()
}
func (e *UncertainError) Unwrap() error { return e.Err }

// IsUncertain reports whether err is an UncertainError.
func IsUncertain(err error) bool {
	var u *UncertainError
	return errors.As(err, &u)
}

// --- requests ---

type envelope struct {
	Success    bool            `json:"success"`
	Errors     []ErrorDetail   `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *PageInfo       `json:"result_info"`
}

// get decodes the result of a GET into out and returns result_info.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) (*PageInfo, error) {
	env, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("cloudflare: decode %s: %w", path, err)
		}
	}
	info := env.ResultInfo
	if info == nil {
		info = &PageInfo{}
	}
	return info, nil
}

// send sends a write and decodes its result into out, which may be nil. A
// result that cannot be decoded after a 2xx is uncertain too: the change
// was made, but Neo Box cannot tell what it looks like.
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("cloudflare: encode %s: %w", path, err)
		}
	}
	env, err := c.do(ctx, method, path, nil, payload)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return &UncertainError{Err: fmt.Errorf("decode %s: %w", path, err)}
		}
	}
	return nil
}

// do sends one request with retries. Reads retry 429, 5xx and network
// errors; writes retry only 429, and report anything else without a clear
// answer as an UncertainError.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, payload []byte) (*envelope, error) {
	target := c.endpoint + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	write := method != http.MethodGet
	var lastErr error
	for attempt := 0; ; attempt++ {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		var reqBody io.Reader
		if payload != nil {
			reqBody = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		delay := c.baseDelay << attempt
		resp, err := c.http.Do(req)
		if err != nil {
			// The transport error names the URL, never the token.
			lastErr = fmt.Errorf("cloudflare: %s %s: %w", method, path, err)
			if write {
				return nil, &UncertainError{Err: lastErr}
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		} else {
			env, err := c.read(resp, method, path)
			if err == nil {
				return env, nil
			}
			lastErr = err
			var apiErr *APIError
			switch {
			case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests:
				if d, ok := retryAfter(resp); ok {
					if d > maxRetryWait {
						return nil, err
					}
					delay = d
				}
			case write:
				if errors.As(err, &apiErr) && apiErr.StatusCode < 500 {
					return nil, err
				}
				return nil, &UncertainError{Err: err}
			case errors.As(err, &apiErr) && apiErr.StatusCode < 500:
				return nil, err
			}
		}
		if attempt >= maxRetries {
			return nil, lastErr
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// read decodes one response. A 2xx with success false is an APIError too.
func (c *Client) read(resp *http.Response, method, path string) (*envelope, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: read %s %s: %w", method, path, err)
	}
	var env envelope
	decodeErr := json.Unmarshal(body, &env)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if decodeErr != nil {
			return nil, fmt.Errorf("cloudflare: decode %s %s: %w", method, path, decodeErr)
		}
		if env.Success {
			return &env, nil
		}
	}
	apiErr := &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Errors: env.Errors}
	if decodeErr != nil && len(body) > 0 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > maxErrorBody {
			msg = msg[:maxErrorBody]
		}
		apiErr.Errors = []ErrorDetail{{Message: msg}}
	}
	return nil, apiErr
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func esc(s string) string { return url.PathEscape(s) }

// --- token and account ---

// VerifyToken checks that Cloudflare accepts the token: as a user token, or
// else as a token owned by accountID. It says nothing about permissions.
func (c *Client) VerifyToken(ctx context.Context, accountID string) error {
	var res struct {
		Status string `json:"status"`
	}
	_, userErr := c.get(ctx, "/user/tokens/verify", nil, &res)
	if userErr != nil {
		res.Status = ""
		if _, err := c.get(ctx, "/accounts/"+esc(accountID)+"/tokens/verify", nil, &res); err != nil {
			if IsAuthError(userErr) && (IsAuthError(err) || IsPermissionDenied(err) || IsNotFound(err)) {
				return userErr
			}
			return err
		}
	}
	if res.Status != "active" {
		return fmt.Errorf("cloudflare: the API token is %s", res.Status)
	}
	return nil
}

// Account is the part of an Account Neo Box reads.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GetAccount reads an Account. It needs one of several account-level
// permissions (e.g. Account Settings Read or Workers Scripts Read).
func (c *Client) GetAccount(ctx context.Context, id string) (*Account, error) {
	var a Account
	if _, err := c.get(ctx, "/accounts/"+esc(id), nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// --- zones ---

// ZoneQuery selects Zones. Name matches anywhere in the Zone name,
// case-insensitively.
type ZoneQuery struct {
	AccountID string
	Name      string
	Page      int
	PerPage   int
}

// ListZones returns one page of Zones, ordered by name.
func (c *Client) ListZones(ctx context.Context, zq ZoneQuery) ([]Zone, *PageInfo, error) {
	q := url.Values{}
	if zq.AccountID != "" {
		q.Set("account.id", zq.AccountID)
	}
	if name := strings.TrimSpace(zq.Name); name != "" {
		q.Set("name", "contains:"+name)
	}
	setPage(q, zq.Page, zq.PerPage)
	q.Set("order", "name")
	q.Set("direction", "asc")
	var zones []Zone
	info, err := c.get(ctx, "/zones", q, &zones)
	return zones, info, err
}

func (c *Client) GetZone(ctx context.Context, id string) (*Zone, error) {
	var z Zone
	if _, err := c.get(ctx, "/zones/"+esc(id), nil, &z); err != nil {
		return nil, err
	}
	return &z, nil
}

func setPage(q url.Values, page, perPage int) {
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if perPage > 0 {
		q.Set("per_page", strconv.Itoa(perPage))
	}
}

// --- DNS records ---

// RecordQuery selects a Zone's DNS records. Search is Cloudflare's search
// across a record's name, content, comment and tags.
type RecordQuery struct {
	Search  string
	Type    string
	Page    int
	PerPage int
}

// ListDNSRecords returns one page of a Zone's records, ordered by name.
func (c *Client) ListDNSRecords(ctx context.Context, zoneID string, rq RecordQuery) ([]DNSRecord, *PageInfo, error) {
	q := url.Values{}
	if s := strings.TrimSpace(rq.Search); s != "" {
		q.Set("search", s)
	}
	if rq.Type != "" {
		q.Set("type", rq.Type)
	}
	setPage(q, rq.Page, rq.PerPage)
	q.Set("order", "name")
	q.Set("direction", "asc")
	var records []DNSRecord
	info, err := c.get(ctx, "/zones/"+esc(zoneID)+"/dns_records", q, &records)
	return records, info, err
}

func (c *Client) GetDNSRecord(ctx context.Context, zoneID, id string) (*DNSRecord, error) {
	var r DNSRecord
	if _, err := c.get(ctx, "/zones/"+esc(zoneID)+"/dns_records/"+esc(id), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateDNSRecord creates the record in. Validate it first.
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, in RecordInput) (*DNSRecord, error) {
	var r DNSRecord
	if err := c.send(ctx, http.MethodPost, "/zones/"+esc(zoneID)+"/dns_records", in.CreateBody(), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// EditDNSRecord changes the fields the form shows for in's type, with a
// partial update that keeps every other attribute of the record. before is
// the record as read; the proxy status is sent only when it is proxiable.
func (c *Client) EditDNSRecord(ctx context.Context, zoneID, id string, in RecordInput, before *DNSRecord) (*DNSRecord, error) {
	var r DNSRecord
	path := "/zones/" + esc(zoneID) + "/dns_records/" + esc(id)
	if err := c.send(ctx, http.MethodPatch, path, in.EditBody(before.Proxiable), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SetDNSRecordProxied changes only the record's proxy status.
func (c *Client) SetDNSRecordProxied(ctx context.Context, zoneID, id string, proxied bool) (*DNSRecord, error) {
	var r DNSRecord
	path := "/zones/" + esc(zoneID) + "/dns_records/" + esc(id)
	if err := c.send(ctx, http.MethodPatch, path, ProxiedBody(proxied), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, id string) error {
	return c.send(ctx, http.MethodDelete, "/zones/"+esc(zoneID)+"/dns_records/"+esc(id), nil, nil)
}

// --- Workers ---

// WorkerRoute is a route pattern that runs a Worker.
type WorkerRoute struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Script  string `json:"script"`
}

// WorkerScript is one Worker as listed.
type WorkerScript struct {
	ID         string        `json:"id"`
	CreatedOn  time.Time     `json:"created_on"`
	ModifiedOn time.Time     `json:"modified_on"`
	Routes     []WorkerRoute `json:"routes"`
}

// ListWorkerScripts returns every Worker of the Account (the API does not
// page them).
func (c *Client) ListWorkerScripts(ctx context.Context, accountID string) ([]WorkerScript, error) {
	var out []WorkerScript
	_, err := c.get(ctx, "/accounts/"+esc(accountID)+"/workers/scripts", nil, &out)
	return out, err
}

// WorkersSubdomain returns the Account's workers.dev subdomain, or "" when
// it has none.
func (c *Client) WorkersSubdomain(ctx context.Context, accountID string) (string, error) {
	var res struct {
		Subdomain string `json:"subdomain"`
	}
	if _, err := c.get(ctx, "/accounts/"+esc(accountID)+"/workers/subdomain", nil, &res); err != nil {
		if IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return res.Subdomain, nil
}

// ScriptOnWorkersDev reports whether the Worker is served on the Account's
// workers.dev subdomain.
func (c *Client) ScriptOnWorkersDev(ctx context.Context, accountID, script string) (bool, error) {
	var res struct {
		Enabled bool `json:"enabled"`
	}
	path := "/accounts/" + esc(accountID) + "/workers/scripts/" + esc(script) + "/subdomain"
	if _, err := c.get(ctx, path, nil, &res); err != nil {
		return false, err
	}
	return res.Enabled, nil
}

// WorkerDomain is a custom domain attached to a Worker.
type WorkerDomain struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	ZoneID   string `json:"zone_id"`
	// Enabled is nil when Cloudflare does not say, which means enabled.
	Enabled *bool `json:"enabled"`
}

// ListWorkerDomains returns every Worker custom domain of the Account.
func (c *Client) ListWorkerDomains(ctx context.Context, accountID string) ([]WorkerDomain, error) {
	var all []WorkerDomain
	err := c.eachPage(ctx, "/accounts/"+esc(accountID)+"/workers/domains", 100, func(raw json.RawMessage) error {
		var page []WorkerDomain
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// ListWorkerRoutes returns a Zone's Worker routes.
func (c *Client) ListWorkerRoutes(ctx context.Context, zoneID string) ([]WorkerRoute, error) {
	var out []WorkerRoute
	_, err := c.get(ctx, "/zones/"+esc(zoneID)+"/workers/routes", nil, &out)
	return out, err
}

// --- Pages ---

// PagesProject is the part of a Pages project Neo Box shows.
type PagesProject struct {
	Name             string    `json:"name"`
	Subdomain        string    `json:"subdomain"`
	Domains          []string  `json:"domains"`
	ProductionBranch string    `json:"production_branch"`
	CreatedOn        time.Time `json:"created_on"`
	// CanonicalDeployment is the newest production deployment.
	CanonicalDeployment *struct {
		URL string `json:"url"`
	} `json:"canonical_deployment"`
}

// ListPagesProjects returns every Pages project of the Account.
func (c *Client) ListPagesProjects(ctx context.Context, accountID string) ([]PagesProject, error) {
	var all []PagesProject
	err := c.eachPage(ctx, "/accounts/"+esc(accountID)+"/pages/projects", 10, func(raw json.RawMessage) error {
		var page []PagesProject
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// PagesDomain is a custom domain of a Pages project.
type PagesDomain struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (c *Client) ListPagesDomains(ctx context.Context, accountID, project string) ([]PagesDomain, error) {
	var out []PagesDomain
	path := "/accounts/" + esc(accountID) + "/pages/projects/" + esc(project) + "/domains"
	_, err := c.get(ctx, path, nil, &out)
	return out, err
}

// eachPage walks a paged list until total_pages (or a short page).
func (c *Client) eachPage(ctx context.Context, path string, perPage int, fn func(json.RawMessage) error) error {
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		setPage(q, page, perPage)
		env, err := c.do(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return err
		}
		if err := fn(env.Result); err != nil {
			return fmt.Errorf("cloudflare: decode %s: %w", path, err)
		}
		info := env.ResultInfo
		if info == nil || info.TotalPages <= page || info.Count == 0 {
			return nil
		}
	}
	return fmt.Errorf("cloudflare: %s: more than %d pages", path, maxPages)
}
