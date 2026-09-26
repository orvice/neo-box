// Package wasabi is a client for the Wasabi Stats API (daily utilization of
// a standalone account and its buckets) plus the Wasabi-specific pieces of
// Neo Box: connection settings and the cost estimate.
//
// API: https://docs.wasabi.com/apidocs/wasabi-stats-api
package wasabi

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
)

// DefaultEndpoint is the Stats API host the reference pages use. (The
// overview page names api.stats.wasabisys.com, which did not answer TLS
// when checked.)
const DefaultEndpoint = "https://stats.wasabisys.com"

const (
	pageSize   = 100 // the API maximum
	maxPages   = 10000
	maxRetries = 3
	userAgent  = "neo-box"
)

// Client reads one account's utilization.
type Client struct {
	http      *http.Client
	endpoint  string
	accessKey string
	secretKey string
	sleep     func(context.Context, time.Duration) error
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

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

func New(accessKey, secretKey string, opts ...Option) (*Client, error) {
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("wasabi: access key and secret key are required")
	}
	c := &Client{
		http:      &http.Client{Timeout: 60 * time.Second},
		endpoint:  DefaultEndpoint,
		accessKey: accessKey,
		secretKey: secretKey,
		sleep:     sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// APIError is a non-2xx response from the Stats API.
type APIError struct {
	StatusCode int
	Path       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wasabi: GET %s: %d %s", e.Path, e.StatusCode, e.Message)
}

// IsAuthError reports whether err is a 401 or 403: the keys are wrong or
// lack Stats access.
func IsAuthError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

// AccountUsage returns the account's daily utilization for the days from
// through to (inclusive, UTC).
func (c *Client) AccountUsage(ctx context.Context, from, to time.Time) ([]Usage, error) {
	return c.list(ctx, "/v1/standalone/utilizations", from, to)
}

// BucketUsage returns every bucket's daily utilization for the days from
// through to (inclusive, UTC).
func (c *Client) BucketUsage(ctx context.Context, from, to time.Time) ([]Usage, error) {
	return c.list(ctx, "/v1/standalone/utilizations/bucket", from, to)
}

// record is one utilization record as the API returns it.
type record struct {
	StartTime string `json:"StartTime"`
	Bucket    string `json:"Bucket"`
	Region    string `json:"Region"`

	NumBillableObjects        int64 `json:"NumBillableObjects"`
	NumBillableDeletedObjects int64 `json:"NumBillableDeletedObjects"`
	RawStorageSizeBytes       int64 `json:"RawStorageSizeBytes"`
	PaddedStorageSizeBytes    int64 `json:"PaddedStorageSizeBytes"`
	MetadataStorageSizeBytes  int64 `json:"MetadataStorageSizeBytes"`
	DeletedStorageSizeBytes   int64 `json:"DeletedStorageSizeBytes"`
	OrphanedStorageSizeBytes  int64 `json:"OrphanedStorageSizeBytes"`
	MinStorageChargeBytes     int64 `json:"MinStorageChargeBytes"`
	NumAPICalls               int64 `json:"NumAPICalls"`
	UploadBytes               int64 `json:"UploadBytes"`
	DownloadBytes             int64 `json:"DownloadBytes"`
	StorageWroteBytes         int64 `json:"StorageWroteBytes"`
	StorageReadBytes          int64 `json:"StorageReadBytes"`
	DeleteBytes               int64 `json:"DeleteBytes"`
	NumGETCalls               int64 `json:"NumGETCalls"`
	NumPUTCalls               int64 `json:"NumPUTCalls"`
	NumDELETECalls            int64 `json:"NumDELETECalls"`
	NumLISTCalls              int64 `json:"NumLISTCalls"`
	NumHEADCalls              int64 `json:"NumHEADCalls"`
}

// page is one response. The docs show both a paged object and, for the
// bucket endpoint, a bare array; decodePage accepts either.
type page struct {
	PageInfo struct {
		RecordCount int `json:"RecordCount"`
		PageCount   int `json:"PageCount"`
	} `json:"PageInfo"`
	Records []record `json:"Records"`
}

func decodePage(body []byte) (*page, error) {
	var p page
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		err := json.Unmarshal(trimmed, &p.Records)
		return &p, err
	}
	err := json.Unmarshal(body, &p)
	return &p, err
}

// list pages through an endpoint until a short page. PageCount, when the
// first page reports it, bounds the walk.
func (c *Client) list(ctx context.Context, path string, from, to time.Time) ([]Usage, error) {
	var out []Usage
	pageCount := 0
	for n := 0; n < maxPages; n++ {
		q := url.Values{}
		q.Set("from", from.UTC().Format(DateLayout))
		q.Set("to", to.UTC().Format(DateLayout))
		q.Set("pageNum", strconv.Itoa(n))
		q.Set("pageSize", strconv.Itoa(pageSize))
		body, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		p, err := decodePage(body)
		if err != nil {
			return nil, fmt.Errorf("wasabi: decode %s: %w", path, err)
		}
		if n == 0 {
			pageCount = p.PageInfo.PageCount
		}
		for _, r := range p.Records {
			u, err := r.usage()
			if err != nil {
				return nil, fmt.Errorf("wasabi: %s: %w", path, err)
			}
			out = append(out, u)
		}
		if len(p.Records) < pageSize || (pageCount > 0 && n+1 >= pageCount) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("wasabi: %s: more than %d pages", path, maxPages)
}

func (r record) usage() (Usage, error) {
	start, err := time.Parse(time.RFC3339, r.StartTime)
	if err != nil {
		return Usage{}, fmt.Errorf("record StartTime %q: %w", r.StartTime, err)
	}
	return Usage{
		Day: DayOf(start), Bucket: r.Bucket, Region: r.Region,
		NumBillableObjects: r.NumBillableObjects, NumBillableDeletedObjects: r.NumBillableDeletedObjects,
		RawStorageSizeBytes: r.RawStorageSizeBytes, PaddedStorageSizeBytes: r.PaddedStorageSizeBytes,
		MetadataStorageSizeBytes: r.MetadataStorageSizeBytes, DeletedStorageSizeBytes: r.DeletedStorageSizeBytes,
		OrphanedStorageSizeBytes: r.OrphanedStorageSizeBytes, MinStorageChargeBytes: r.MinStorageChargeBytes,
		NumAPICalls: r.NumAPICalls, UploadBytes: r.UploadBytes, DownloadBytes: r.DownloadBytes,
		StorageWroteBytes: r.StorageWroteBytes, StorageReadBytes: r.StorageReadBytes, DeleteBytes: r.DeleteBytes,
		NumGETCalls: r.NumGETCalls, NumPUTCalls: r.NumPUTCalls, NumDELETECalls: r.NumDELETECalls,
		NumLISTCalls: r.NumLISTCalls, NumHEADCalls: r.NumHEADCalls,
	}, nil
}

// get sends one request, retrying 429s and 5xx.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	target := c.endpoint + path + "?" + q.Encode()
	var lastErr error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		// The Stats API takes the raw key pair; nothing is signed.
		req.Header.Set("Authorization", c.accessKey+":"+c.secretKey)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)

		delay := backoff(attempt)
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("wasabi: GET %s: %w", path, err)
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case readErr != nil:
				lastErr = fmt.Errorf("wasabi: read %s: %w", path, readErr)
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				return body, nil
			default:
				apiErr := &APIError{StatusCode: resp.StatusCode, Path: path, Message: errorMessage(body)}
				if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
					return nil, apiErr
				}
				lastErr = apiErr
				if d, ok := retryAfter(resp); ok {
					delay = d
				}
			}
		}
		if attempt >= maxRetries {
			return nil, lastErr
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// errorMessage pulls a message out of an error body, which may be JSON.
func errorMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &v) == nil {
		for _, m := range []string{v.Message, v.Msg, v.Error} {
			if m != "" {
				return m
			}
		}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
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
