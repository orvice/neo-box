// Package porkbun is a read-only client for the Porkbun v3 JSON API
// (domains, nameservers, DNS records, URL forwarding, DNSSEC and glue records)
// plus Porkbun connection settings.
//
// Requests authenticate via both X-API-Key / X-Secret-API-Key headers and the
// standard POST JSON body fields, share a rate limiter per connection, and
// retry reads on 429, 5xx and transient network errors.
//
// API: https://porkbun.com/api/json/v3/documentation
package porkbun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// DefaultEndpoint is the Porkbun API v3 base URL.
const DefaultEndpoint = "https://api.porkbun.com/api/json/v3"

const (
	// Porkbun allows ~60 requests/minute with short bursts. Stay conservative
	// so repeated page loads do not trigger 429 responses.
	defaultRPS   = 1
	defaultBurst = 5
	maxRetries   = 3
	maxRetryWait = 30 * time.Second
	maxPages     = 50
	listPageSize = 1000
	userAgent    = "neo-box"
	maxErrorBody = 2048
)

// Client talks to the Porkbun API v3 with one API key pair.
type Client struct {
	http         *http.Client
	endpoint     string
	apiKey       string
	secretAPIKey string
	limiter      *rate.Limiter
	baseDelay    time.Duration
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

// WithLimiter shares a rate limiter across all requests of one connection.
func WithLimiter(l *rate.Limiter) Option { return func(c *Client) { c.limiter = l } }

// WithRetryDelay sets the first retry delay; later ones double it.
func WithRetryDelay(d time.Duration) Option { return func(c *Client) { c.baseDelay = d } }

// NewLimiter returns a conservative limiter suited to Porkbun's API rate.
func NewLimiter() *rate.Limiter { return rate.NewLimiter(defaultRPS, defaultBurst) }

// New builds a Porkbun API client for one API key pair.
func New(apiKey, secretAPIKey string, opts ...Option) (*Client, error) {
	apiKey, err := NormalizeAPIKey(apiKey)
	if err != nil {
		return nil, err
	}
	secretAPIKey = strings.TrimSpace(secretAPIKey)
	if secretAPIKey == "" {
		return nil, errors.New("porkbun: secret api key is required")
	}
	c := &Client{
		http:         &http.Client{Timeout: 30 * time.Second},
		endpoint:     DefaultEndpoint,
		apiKey:       apiKey,
		secretAPIKey: secretAPIKey,
		limiter:      NewLimiter(),
		baseDelay:    500 * time.Millisecond,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// --- errors ---

// APIError is a failure response returned by Porkbun.
type APIError struct {
	StatusCode   int
	Method       string
	Path         string
	Code         string
	Message      string
	TTLRemaining int
}

func (e *APIError) Error() string {
	msg := e.DisplayMessage()
	if e.Code != "" {
		return fmt.Sprintf("porkbun: %s %s: %d %s (%s)", e.Method, e.Path, e.StatusCode, msg, e.Code)
	}
	return fmt.Sprintf("porkbun: %s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// DisplayMessage is Porkbun's message for showing to a user.
func (e *APIError) DisplayMessage() string {
	if s := strings.TrimSpace(e.Message); s != "" {
		return s
	}
	return http.StatusText(e.StatusCode)
}

// IsAuthError reports whether Porkbun rejected the API key pair itself
// (invalid, expired or revoked). Classic Porkbun v3 endpoints may return HTTP
// 400 with an "Invalid API key" message in addition to HTTP 401.
func IsAuthError(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	if e.StatusCode == http.StatusUnauthorized {
		return true
	}
	code := strings.ToUpper(e.Code)
	if strings.HasPrefix(code, "INVALID_API_KEY") || code == "API_KEY_REQUIRED" || code == "AUTHENTICATION_FAILED" {
		return true
	}
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "api key is invalid") ||
		strings.Contains(msg, "invalid secret") ||
		strings.Contains(msg, "authentication failed")
}

// IsPermissionDenied reports whether the key pair was accepted but lacks the
// scope, IP allowlist or domain allowlist needed for this endpoint.
func IsPermissionDenied(err error) bool {
	var e *APIError
	if !errors.As(err, &e) || IsAuthError(err) {
		return false
	}
	if e.StatusCode == http.StatusForbidden {
		return true
	}
	switch strings.ToUpper(e.Code) {
	case "INSUFFICIENT_SCOPE", "DOMAIN_NOT_ALLOWED", "IP_NOT_ALLOWED":
		return true
	}
	return false
}

// IsAPIAccessDisabled reports whether Porkbun refused a domain-level call
// because API access is not opted in for that domain.
func IsAPIAccessDisabled(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	if strings.EqualFold(e.Code, "API_ACCESS_DISABLED") || strings.EqualFold(e.Code, "DOMAIN_API_ACCESS_DISABLED") {
		return true
	}
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "not opted in to api") ||
		strings.Contains(msg, "opted in to api access") ||
		strings.Contains(msg, "api access is not enabled") ||
		strings.Contains(msg, "api access disabled")
}

// IsRateLimited reports whether Porkbun throttled the request.
func IsRateLimited(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.StatusCode == http.StatusTooManyRequests || strings.EqualFold(e.Code, "RATE_LIMIT_EXCEEDED"))
}

// IsNotFound reports whether Porkbun reported that the domain or resource does
// not exist in the account.
func IsNotFound(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	if e.StatusCode == http.StatusNotFound || strings.EqualFold(e.Code, "DOMAIN_NOT_FOUND") || strings.EqualFold(e.Code, "INVALID_DOMAIN") {
		return true
	}
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "invalid domain") ||
		strings.Contains(msg, "domain not found") ||
		strings.Contains(msg, "does not exist")
}

// --- low-level transport ---

type statusEnvelope struct {
	Status       string  `json:"status"`
	Code         string  `json:"code"`
	Message      string  `json:"message"`
	TTLRemaining flexInt `json:"ttlRemaining"`
	Sandbox      bool    `json:"sandbox"`
}

func (c *Client) postRead(ctx context.Context, path string, extra map[string]any, dst any) (http.Header, error) {
	payload := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		payload[k] = v
	}
	payload["apikey"] = c.apiKey
	payload["secretapikey"] = c.secretAPIKey

	rawBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("porkbun: encode request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(rawBody))
		if err != nil {
			return nil, fmt.Errorf("porkbun: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("X-API-Key", c.apiKey)
		req.Header.Set("X-Secret-API-Key", c.secretAPIKey)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("porkbun: POST %s: %w", path, err)
			if attempt == maxRetries || ctx.Err() != nil {
				return nil, lastErr
			}
			if err := sleepContext(ctx, c.backoff(attempt, nil, 0)); err != nil {
				return nil, err
			}
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("porkbun: read POST %s: %w", path, readErr)
			if attempt == maxRetries || ctx.Err() != nil {
				return nil, lastErr
			}
			if err := sleepContext(ctx, c.backoff(attempt, resp.Header, 0)); err != nil {
				return nil, err
			}
			continue
		}

		var env statusEnvelope
		_ = json.Unmarshal(body, &env)

		isErrStatus := resp.StatusCode < 200 || resp.StatusCode >= 300
		isErrPayload := strings.EqualFold(env.Status, "ERROR")
		if isErrStatus || isErrPayload {
			msg := strings.TrimSpace(env.Message)
			if msg == "" && len(body) > 0 && !json.Valid(body) {
				msg = truncateString(strings.TrimSpace(string(body)), maxErrorBody)
			}
			apiErr := &APIError{
				StatusCode:   resp.StatusCode,
				Method:       http.MethodPost,
				Path:         path,
				Code:         strings.TrimSpace(env.Code),
				Message:      msg,
				TTLRemaining: int(env.TTLRemaining),
			}
			retryable := resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode >= 500 ||
				strings.EqualFold(apiErr.Code, "RATE_LIMIT_EXCEEDED")
			if retryable && attempt < maxRetries {
				lastErr = apiErr
				if err := sleepContext(ctx, c.backoff(attempt, resp.Header, apiErr.TTLRemaining)); err != nil {
					return nil, err
				}
				continue
			}
			return resp.Header, apiErr
		}

		if dst != nil {
			if err := json.Unmarshal(body, dst); err != nil {
				return resp.Header, fmt.Errorf("porkbun: decode POST %s: %w", path, err)
			}
		}
		return resp.Header, nil
	}
	return nil, lastErr
}

func (c *Client) backoff(attempt int, headers http.Header, ttlRemaining int) time.Duration {
	if headers != nil {
		if ra := strings.TrimSpace(headers.Get("Retry-After")); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				return min(time.Duration(secs)*time.Second, maxRetryWait)
			}
			if t, err := http.ParseTime(ra); err == nil {
				if wait := time.Until(t); wait > 0 {
					return min(wait, maxRetryWait)
				}
			}
		}
	}
	if ttlRemaining > 0 {
		return min(time.Duration(ttlRemaining)*time.Second, maxRetryWait)
	}
	d := c.baseDelay << attempt
	return min(d, maxRetryWait)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
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

func truncateString(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars]
}

// --- API endpoints (read-only) ---

// Ping verifies the API key pair against POST /ping.
func (c *Client) Ping(ctx context.Context) (PingResult, error) {
	var resp struct {
		Status  string `json:"status"`
		YourIP  string `json:"yourIp"`
		Sandbox bool   `json:"sandbox"`
	}
	headers, err := c.postRead(ctx, "/ping", nil, &resp)
	if err != nil {
		return PingResult{}, err
	}
	sandbox := resp.Sandbox || strings.EqualFold(headers.Get("X-Porkbun-Sandbox"), "true") || IsSandboxKey(c.apiKey)
	return PingResult{YourIP: strings.TrimSpace(resp.YourIP), Sandbox: sandbox}, nil
}

type rawDomainLabel struct {
	ID    flexString `json:"id"`
	Title string     `json:"title"`
	Color string     `json:"color"`
}

type rawDomain struct {
	Domain       string           `json:"domain"`
	Status       string           `json:"status"`
	TLD          string           `json:"tld"`
	CreateDate   string           `json:"createDate"`
	ExpireDate   string           `json:"expireDate"`
	SecurityLock flexBool         `json:"securityLock"`
	WhoisPrivacy flexBool         `json:"whoisPrivacy"`
	AutoRenew    flexBool         `json:"autoRenew"`
	NotLocal     flexBool         `json:"notLocal"`
	APIAccess    *flexBool        `json:"apiAccess"`
	Labels       []rawDomainLabel `json:"labels"`
}

// ListDomains retrieves all domains in the account by paging /domain/listAll
// in chunks of 1,000.
func (c *Client) ListDomains(ctx context.Context) ([]Domain, error) {
	var all []Domain
	seen := make(map[string]struct{})
	for page := 0; page < maxPages; page++ {
		start := page * listPageSize
		var resp struct {
			Status  string      `json:"status"`
			Domains []rawDomain `json:"domains"`
		}
		_, err := c.postRead(ctx, "/domain/listAll", map[string]any{
			"start":         strconv.Itoa(start),
			"includeLabels": "yes",
		}, &resp)
		if err != nil {
			return nil, err
		}
		for _, rd := range resp.Domains {
			name := strings.ToLower(strings.TrimSpace(rd.Domain))
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			labels := make([]DomainLabel, 0, len(rd.Labels))
			for _, l := range rd.Labels {
				labels = append(labels, DomainLabel{
					ID:    string(l.ID),
					Title: strings.TrimSpace(l.Title),
					Color: strings.TrimSpace(l.Color),
				})
			}
			// When Porkbun omits apiAccess in older responses, default to true
			// so detail queries are not prematurely blocked; explicit 0/false
			// is preserved.
			apiAccess := true
			if rd.APIAccess != nil {
				apiAccess = bool(*rd.APIAccess)
			}
			all = append(all, Domain{
				Domain:       name,
				Status:       strings.TrimSpace(rd.Status),
				TLD:          strings.ToLower(strings.TrimSpace(rd.TLD)),
				CreatedAt:    parsePorkbunTime(rd.CreateDate),
				ExpiresAt:    parsePorkbunTime(rd.ExpireDate),
				SecurityLock: bool(rd.SecurityLock),
				WhoisPrivacy: bool(rd.WhoisPrivacy),
				AutoRenew:    bool(rd.AutoRenew),
				NotLocal:     bool(rd.NotLocal),
				APIAccess:    apiAccess,
				Labels:       labels,
			})
		}
		if len(resp.Domains) < listPageSize {
			break
		}
	}
	return all, nil
}

// GetNameservers retrieves the authoritative nameservers for one domain.
func (c *Client) GetNameservers(ctx context.Context, domain string) ([]string, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string   `json:"status"`
		NS     []string `json:"ns"`
	}
	if _, err := c.postRead(ctx, "/domain/getNs/"+url.PathEscape(d), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.NS))
	for _, ns := range resp.NS {
		if trimmed := strings.TrimSpace(ns); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

type rawDNSRecord struct {
	ID      flexString `json:"id"`
	Name    string     `json:"name"`
	Type    string     `json:"type"`
	Content string     `json:"content"`
	TTL     flexInt    `json:"ttl"`
	Prio    flexString `json:"prio"`
	Notes   flexString `json:"notes"`
}

// ListDNSRecords retrieves all DNS records for one domain.
func (c *Client) ListDNSRecords(ctx context.Context, domain string) ([]DNSRecord, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status  string         `json:"status"`
		Records []rawDNSRecord `json:"records"`
	}
	if _, err := c.postRead(ctx, "/dns/retrieve/"+url.PathEscape(d), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]DNSRecord, 0, len(resp.Records))
	for _, r := range resp.Records {
		out = append(out, DNSRecord{
			ID:       string(r.ID),
			Name:     strings.TrimSpace(r.Name),
			Type:     strings.ToUpper(strings.TrimSpace(r.Type)),
			Content:  r.Content,
			TTL:      int(r.TTL),
			Priority: strings.TrimSpace(string(r.Prio)),
			Notes:    strings.TrimSpace(string(r.Notes)),
		})
	}
	return out, nil
}

type rawURLForward struct {
	ID           flexString `json:"id"`
	Subdomain    string     `json:"subdomain"`
	Location     string     `json:"location"`
	Type         string     `json:"type"`
	RedirectType string     `json:"redirectType"`
	IncludePath  flexBool   `json:"includePath"`
	Wildcard     flexBool   `json:"wildcard"`
}

// ListURLForwards retrieves all URL forwarding rules for one domain.
func (c *Client) ListURLForwards(ctx context.Context, domain string) ([]URLForward, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status   string          `json:"status"`
		Forwards []rawURLForward `json:"forwards"`
	}
	if _, err := c.postRead(ctx, "/domain/getUrlForwarding/"+url.PathEscape(d), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]URLForward, 0, len(resp.Forwards))
	for _, f := range resp.Forwards {
		typ := strings.TrimSpace(f.RedirectType)
		if typ == "" {
			typ = strings.TrimSpace(f.Type)
		}
		out = append(out, URLForward{
			ID:          string(f.ID),
			Subdomain:   strings.TrimSpace(f.Subdomain),
			Location:    strings.TrimSpace(f.Location),
			Type:        typ,
			IncludePath: bool(f.IncludePath),
			Wildcard:    bool(f.Wildcard),
		})
	}
	return out, nil
}

type rawDNSSECRecord struct {
	KeyTag     flexString `json:"keyTag"`
	Alg        flexString `json:"alg"`
	Algorithm  flexString `json:"algorithm"`
	DigestType flexString `json:"digestType"`
	Digest     string     `json:"digest"`
}

// ListDNSSECRecords retrieves all DNSSEC DS records for one domain.
// Porkbun returns `records` either as a map keyed by keyTag or as an array
// (often [] when empty); both shapes are handled.
func (c *Client) ListDNSSECRecords(ctx context.Context, domain string) ([]DNSSECRecord, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status  string          `json:"status"`
		Records json.RawMessage `json:"records"`
	}
	if _, err := c.postRead(ctx, "/dns/getDnssecRecords/"+url.PathEscape(d), nil, &resp); err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace(resp.Records)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var parsed []rawDNSSECRecord
	switch raw[0] {
	case '[':
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("porkbun: decode dnssec records array: %w", err)
		}
	case '{':
		var byKey map[string]rawDNSSECRecord
		if err := json.Unmarshal(raw, &byKey); err != nil {
			return nil, fmt.Errorf("porkbun: decode dnssec records map: %w", err)
		}
		for k, v := range byKey {
			if strings.TrimSpace(string(v.KeyTag)) == "" {
				v.KeyTag = flexString(k)
			}
			parsed = append(parsed, v)
		}
	default:
		return nil, fmt.Errorf("porkbun: unexpected dnssec records shape")
	}

	out := make([]DNSSECRecord, 0, len(parsed))
	for _, r := range parsed {
		alg := strings.TrimSpace(string(r.Alg))
		if alg == "" {
			alg = strings.TrimSpace(string(r.Algorithm))
		}
		out = append(out, DNSSECRecord{
			KeyTag:     strings.TrimSpace(string(r.KeyTag)),
			Algorithm:  alg,
			DigestType: strings.TrimSpace(string(r.DigestType)),
			Digest:     strings.TrimSpace(r.Digest),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].KeyTag != out[j].KeyTag {
			return out[i].KeyTag < out[j].KeyTag
		}
		return out[i].Digest < out[j].Digest
	})
	return out, nil
}

// ListGlueRecords retrieves custom nameserver glue records for one domain.
// Porkbun returns `hosts` as either modern host objects ({host, v4, v6}) or
// classic 2-element tuples ([host, {v4, v6}]); both shapes are handled.
func (c *Client) ListGlueRecords(ctx context.Context, domain string) ([]GlueHost, error) {
	d, err := NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string          `json:"status"`
		Hosts  json.RawMessage `json:"hosts"`
		Glue   json.RawMessage `json:"glue"`
	}
	if _, err := c.postRead(ctx, "/domain/getGlue/"+url.PathEscape(d), nil, &resp); err != nil {
		return nil, err
	}

	out, err := parseGlueHosts(d, resp.Hosts, resp.Glue)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

func parseGlueHosts(domain string, hostsRaw, glueRaw json.RawMessage) ([]GlueHost, error) {
	raw := bytes.TrimSpace(hostsRaw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = bytes.TrimSpace(glueRaw)
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("porkbun: decode glue hosts: %w", err)
	}

	out := make([]GlueHost, 0, len(items))
	for _, item := range items {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 {
			continue
		}
		switch trimmed[0] {
		case '[':
			// Classic Porkbun tuple: ["ns1.example.com", {"v4": [...], "v6": [...]}]
			var tuple []json.RawMessage
			if err := json.Unmarshal(trimmed, &tuple); err != nil || len(tuple) < 2 {
				continue
			}
			var host string
			if err := json.Unmarshal(tuple[0], &host); err != nil {
				continue
			}
			var addrs struct {
				V4 []string `json:"v4"`
				V6 []string `json:"v6"`
			}
			_ = json.Unmarshal(tuple[1], &addrs)
			out = append(out, GlueHost{
				Host: strings.TrimSpace(host),
				IPv4: cleanStrings(addrs.V4),
				IPv6: cleanStrings(addrs.V6),
			})
		case '{':
			var obj struct {
				Host      string   `json:"host"`
				Subdomain string   `json:"subdomain"`
				V4        []string `json:"v4"`
				V6        []string `json:"v6"`
				IPs       []string `json:"ips"`
			}
			if err := json.Unmarshal(trimmed, &obj); err != nil {
				return nil, fmt.Errorf("porkbun: decode glue host object: %w", err)
			}
			host := strings.TrimSpace(obj.Host)
			if host == "" && strings.TrimSpace(obj.Subdomain) != "" {
				host = strings.TrimSpace(obj.Subdomain) + "." + domain
			}
			v4, v6 := cleanStrings(obj.V4), cleanStrings(obj.V6)
			if len(v4) == 0 && len(v6) == 0 && len(obj.IPs) > 0 {
				v4, v6 = splitIPs(obj.IPs)
			}
			if host != "" {
				out = append(out, GlueHost{Host: host, IPv4: v4, IPv6: v6})
			}
		}
	}
	return out, nil
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
