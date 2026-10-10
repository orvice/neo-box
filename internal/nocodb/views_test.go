package nocodb

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestViewRequests(t *testing.T) {
	var got []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		switch r.URL.Path {
		case "/api/v2/meta/tables/m1/views", "/api/v2/meta/filters/f1/children":
			_, _ = w.Write([]byte(`{"list":[{"id":"x"}]}`))
		case "/api/v2/meta/tables/m1/kanbans", "/api/v2/meta/views/v1/filters":
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"new"}`))
				return
			}
			_, _ = w.Write([]byte(`{"list":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	ctx := context.Background()
	if views, err := c.ListViews(ctx, "m1"); err != nil || len(views) != 1 {
		t.Fatalf("ListViews = %s, %v", views, err)
	}
	if children, err := c.ListFilterChildren(ctx, "f1"); err != nil || len(children) != 1 {
		t.Fatalf("ListFilterChildren = %s, %v", children, err)
	}
	if id, err := c.CreateView(ctx, "m1", ViewTypeKanban, map[string]string{"title": "Board"}); err != nil || id != "new" {
		t.Fatalf("CreateView = %q, %v", id, err)
	}
	if _, err := c.CreateView(ctx, "m1", 9, nil); err == nil {
		t.Fatal("CreateView of an unknown type should fail")
	}
	if id, err := c.CreateFilter(ctx, "v1", map[string]bool{"is_group": true}); err != nil || id != "new" {
		t.Fatalf("CreateFilter = %q, %v", id, err)
	}
	for _, err := range []error{
		c.UpdateView(ctx, "v1", map[string]string{"lock_type": "locked"}),
		c.UpdateViewSettings(ctx, ViewTypeForm, "v1", map[string]string{"heading": "h"}),
		c.UpdateViewColumn(ctx, "v1", "c1", map[string]int{"order": 2}),
		c.UpdateGridColumn(ctx, "c1", map[string]string{"width": "1px"}),
		c.UpdateFormColumn(ctx, "c2", map[string]bool{"required": true}),
		c.CreateSort(ctx, "v1", map[string]string{"direction": "asc"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"GET /api/v2/meta/tables/m1/views ",
		"GET /api/v2/meta/filters/f1/children ",
		`POST /api/v2/meta/tables/m1/kanbans {"title":"Board"}`,
		`POST /api/v2/meta/views/v1/filters {"is_group":true}`,
		`PATCH /api/v2/meta/views/v1 {"lock_type":"locked"}`,
		`PATCH /api/v2/meta/forms/v1 {"heading":"h"}`,
		`PATCH /api/v2/meta/views/v1/columns/c1 {"order":2}`,
		`PATCH /api/v2/meta/grid-columns/c1 {"width":"1px"}`,
		`PATCH /api/v2/meta/form-columns/c2 {"required":true}`,
		`POST /api/v2/meta/views/v1/sorts {"direction":"asc"}`,
	}
	if len(got) != len(want) {
		t.Fatalf("requests:\n%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %s, want %s", i, got[i], want[i])
		}
	}
}
