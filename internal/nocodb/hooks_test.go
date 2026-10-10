package nocodb

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHookRequests(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xc-token") != "tok" {
			t.Error("missing API token")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/meta/tables/m1/hooks":
			_, _ = w.Write([]byte(`{"list":[{"id":"h1","notification":"{\"type\":\"URL\"}"}]}`))
		case "GET /api/v2/meta/hooks/h1/filters":
			_, _ = w.Write([]byte(`{"list":[{"id":"f1","fk_column_id":"c1"}]}`))
		case "POST /api/v2/meta/tables/m1/hooks":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["active"] != false || body["title"] != "Changed" {
				t.Errorf("hook body = %+v, %v", body, err)
			}
			_, _ = w.Write([]byte(`{"id":"h2"}`))
		case "POST /api/v2/meta/hooks/h2/filters":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["is_group"] != true {
				t.Errorf("filter body = %+v, %v", body, err)
			}
			_, _ = w.Write([]byte(`{"id":"f2"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := t.Context()
	if hooks, err := c.ListHooks(ctx, "m1"); err != nil || len(hooks) != 1 || string(hooks[0]) != `{"id":"h1","notification":"{\"type\":\"URL\"}"}` {
		t.Fatalf("ListHooks = %s, %v", hooks, err)
	}
	if filters, err := c.ListHookFilters(ctx, "h1"); err != nil || len(filters) != 1 {
		t.Fatalf("ListHookFilters = %s, %v", filters, err)
	}
	if id, err := c.CreateHook(ctx, "m1", map[string]any{"title": "Changed", "active": false}); err != nil || id != "h2" {
		t.Fatalf("CreateHook = %q, %v", id, err)
	}
	if id, err := c.CreateHookFilter(ctx, "h2", map[string]bool{"is_group": true}); err != nil || id != "f2" {
		t.Fatalf("CreateHookFilter = %q, %v", id, err)
	}
}

func TestHookCreatesRequireResponseIDs(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.CreateHook(t.Context(), "m1", map[string]bool{"active": false}); err == nil {
		t.Error("CreateHook accepted a response without an ID")
	}
	if _, err := c.CreateHookFilter(t.Context(), "h1", map[string]bool{"is_group": true}); err == nil {
		t.Error("CreateHookFilter accepted a response without an ID")
	}
}
