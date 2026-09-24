package nocodb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL+"/dashboard/", "tok", WithRateLimit(0))
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://noco.example.com/":          "https://noco.example.com",
		"https://noco.example.com/dashboard": "https://noco.example.com",
		"http://h:8080/sub/?x=1#frag":        "http://h:8080/sub",
	}
	for in, want := range cases {
		got, err := NormalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "noco.example.com", "ftp://x"} {
		if _, err := NormalizeBaseURL(bad); err == nil {
			t.Errorf("NormalizeBaseURL(%q) should fail", bad)
		}
	}
}

func TestListBasesSendsTokenAndDecodes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/meta/bases/" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("xc-token") != "tok" {
			t.Errorf("xc-token = %q", r.Header.Get("xc-token"))
		}
		_, _ = w.Write([]byte(`{"list":[{"id":"p1","title":"CRM"}],"pageInfo":{}}`))
	})
	bases, err := c.ListBases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 || bases[0].ID != "p1" || bases[0].Title != "CRM" {
		t.Fatalf("bases = %+v", bases)
	}
}

func TestRetriesOn429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"msg":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"list":[]}`))
	})
	if _, err := c.ListBases(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestClientErrorNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"msg":"Invalid token"}`))
	})
	_, err := c.ListBases(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 401 || apiErr.Message != "Invalid token" {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestListRecordsAndLinkedIDsPaginate(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch r.URL.Path {
		case "/api/v3/data/p1/m1/records":
			if page == "1" {
				_, _ = w.Write([]byte(`{"records":[{"id":1,"fields":{"Name":"a"}}],"next":"x?page=2"}`))
				return
			}
			_, _ = w.Write([]byte(`{"records":[{"id":"k2","fields":{}}],"next":null}`))
		case "/api/v3/data/p1/m1/links/c9/1":
			if page == "1" {
				_, _ = w.Write([]byte(`{"records":[{"id":7}],"next":"x?page=2"}`))
				return
			}
			_, _ = w.Write([]byte(`{"records":[{"id":8}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	p1, err := c.ListRecords(ctx, "p1", "m1", 1, 1)
	if err != nil || !p1.HasNext || p1.Records[0].IDString() != "1" {
		t.Fatalf("page1 = %+v, %v", p1, err)
	}
	p2, err := c.ListRecords(ctx, "p1", "m1", 2, 1)
	if err != nil || p2.HasNext || p2.Records[0].IDString() != "k2" {
		t.Fatalf("page2 = %+v, %v", p2, err)
	}
	ids, err := c.ListLinkedIDs(ctx, "p1", "m1", "c9", "1")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(ids)
	if string(got) != "[7,8]" {
		t.Fatalf("linked ids = %s", got)
	}
}
