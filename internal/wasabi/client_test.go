package wasabi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func day(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func rec(i int, bucket string) map[string]any {
	return map[string]any{
		"StartTime":              day("2026-09-01").AddDate(0, 0, i%30).Format(time.RFC3339),
		"EndTime":                day("2026-09-02").AddDate(0, 0, i%30).Format(time.RFC3339),
		"Bucket":                 bucket,
		"Region":                 "us-east-1",
		"PaddedStorageSizeBytes": 1000 + i,
		"NumGETCalls":            7,
	}
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New("AK", "SK", WithEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestAccountUsagePagedObject(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "AK:SK" {
			t.Errorf("Authorization = %q, want AK:SK", got)
		}
		if r.URL.Path != "/v1/standalone/utilizations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("from") != "2026-09-01" || q.Get("to") != "2026-09-30" || q.Get("pageSize") != "100" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		n, _ := strconv.Atoi(q.Get("pageNum"))
		count := 100
		if n == 1 {
			count = 30
		}
		var records []map[string]any
		for i := 0; i < count; i++ {
			records = append(records, rec(n*100+i, ""))
		}
		// RecordCount and PageCount are only reported on page 0.
		info := map[string]int{"RecordCount": 0, "PageCount": 0, "PageSize": 100, "PageNum": n}
		if n == 0 {
			info["RecordCount"], info["PageCount"] = 130, 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"PageInfo": info, "Records": records})
	})

	got, err := c.AccountUsage(context.Background(), day("2026-09-01"), day("2026-09-30"))
	if err != nil {
		t.Fatalf("AccountUsage: %v", err)
	}
	if len(got) != 130 || calls.Load() != 2 {
		t.Fatalf("got %d records in %d calls, want 130 in 2", len(got), calls.Load())
	}
	if !got[0].Day.Equal(day("2026-09-01")) || got[0].PaddedStorageSizeBytes != 1000 || got[0].NumGETCalls != 7 {
		t.Fatalf("first record = %+v", got[0])
	}
}

func TestBucketUsageBareArray(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/standalone/utilizations/bucket" {
			t.Errorf("path = %s", r.URL.Path)
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("pageNum"))
		count := 100
		if n == 1 {
			count = 5
		}
		var records []map[string]any
		for i := 0; i < count; i++ {
			records = append(records, rec(i, fmt.Sprintf("b%d", n)))
		}
		_ = json.NewEncoder(w).Encode(records)
	})

	got, err := c.BucketUsage(context.Background(), day("2026-09-01"), day("2026-09-30"))
	if err != nil {
		t.Fatalf("BucketUsage: %v", err)
	}
	if len(got) != 105 || got[0].Bucket != "b0" || got[104].Bucket != "b1" || got[0].Region != "us-east-1" {
		t.Fatalf("got %d records; first %+v", len(got), got[0])
	}
}

func TestErrorsAndRetries(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_ = json.NewEncoder(w).Encode([]any{})
		}
	})
	if _, err := c.AccountUsage(context.Background(), day("2026-09-01"), day("2026-09-01")); err != nil || calls.Load() != 3 {
		t.Fatalf("after 429 and 502: err %v, calls %d", err, calls.Load())
	}

	denied := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Access denied"}`))
	})
	_, err := denied.AccountUsage(context.Background(), day("2026-09-01"), day("2026-09-01"))
	if !IsAuthError(err) {
		t.Fatalf("403 err = %v, want an auth error", err)
	}
	if want := "wasabi: GET /v1/standalone/utilizations: 403 Access denied"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestNewRequiresKeys(t *testing.T) {
	if _, err := New("", "SK"); err == nil {
		t.Fatal("New without an access key: expected error")
	}
}
