package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

func raws(items ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(items))
	for i, s := range items {
		out[i] = json.RawMessage(s)
	}
	return out
}

// viewsFixture is one table with a view of every kind a restore treats
// differently: the default grid (settings, columns, a sort, a filter
// group with a filter on a skipped field), a kanban, a form, a shared
// grid, a calendar, a map on a skipped field and a view type v2 can't
// create.
func viewsFixture(t *testing.T) []byte {
	t.Helper()
	tb := &Table{ID: "m1", Title: "Tasks", Schema: json.RawMessage(`{"id":"m1","title":"Tasks","fields":[
		{"id":"c0","title":"Id","type":"ID"},
		{"id":"c1","title":"Name","type":"SingleLineText"},
		{"id":"c2","title":"Status","type":"SingleSelect","options":{"choices":[{"id":"s1","title":"Todo"},{"id":"s2","title":"Done"}]}},
		{"id":"c3","title":"Where","type":"GeoData"},
		{"id":"c4","title":"Due","type":"Date"}
	]}`)}
	tb.Records = []nocodb.Record{}
	tb.Views = []View{
		{
			// The default view is not first in order, but created first.
			View: json.RawMessage(`{"id":"v1","title":"All tasks","type":3,"order":2,"created_at":"2026-01-01 00:00:00+00:00","lock_type":"locked","description":"everything","meta":{"rowColoringInfo":null},"view":{"row_height":2}}`),
			Columns: raws(
				`{"id":"g1","fk_column_id":"c1","show":1,"order":1,"width":"300px","group_by":null}`,
				`{"id":"g2","fk_column_id":"c4","show":0,"order":2,"width":"200px"}`,
				`{"id":"g3","fk_column_id":"c3","show":0,"order":3,"width":"200px"}`,
				`{"id":"g4","fk_column_id":"sys1","show":0,"order":9,"width":"200px"}`,
			),
			Sorts: raws(`{"id":"so1","fk_column_id":"c1","direction":"desc"}`, `{"id":"so2","fk_column_id":"c3","direction":"asc"}`),
			Filters: raws(
				`{"id":"f1","is_group":1,"logical_op":"or","fk_parent_id":null}`,
				`{"id":"f2","fk_parent_id":"f1","fk_column_id":"c2","comparison_op":"eq","value":"Todo","logical_op":"or"}`,
				`{"id":"f3","fk_parent_id":"f1","fk_column_id":"c3","comparison_op":"blank","logical_op":"or"}`,
				`{"id":"f4","fk_column_id":"c4","comparison_op":"eq","comparison_sub_op":"today","logical_op":"and"}`,
			),
		},
		{View: json.RawMessage(`{"id":"v2","title":"Board","type":4,"order":1,"created_at":"2026-01-02 00:00:00+00:00","meta":{"groupingFieldColumn":{"id":"c2"}},"view":{"fk_grp_col_id":"c2","fk_cover_image_col_id":null,"meta":{"fk_cover_image_object_fit":"fit","c2":[{"id":"uncategorized","order":0},{"id":"s2","title":"Done","fk_column_id":"c2","base_id":"p1","collapsed":true,"order":1},{"id":"s1","title":"Todo","fk_column_id":"c2","order":2},{"id":"s9","title":"Gone","order":3}]}}}`)},
		{
			View:    json.RawMessage(`{"id":"v3","title":"Submit","type":1,"order":3,"created_at":"2026-01-03 00:00:00+00:00","view":{"heading":"Submit a task","success_msg":"thanks","banner_image_url":{"path":"x"},"logo_url":null}}`),
			Columns: raws(`{"id":"fc1","fk_column_id":"c1","show":1,"order":1,"label":"Task name","required":true}`),
		},
		{View: json.RawMessage(`{"id":"v4","title":"Shared","type":3,"order":4,"created_at":"2026-01-04 00:00:00+00:00","uuid":"abc","view":{}}`)},
		{View: json.RawMessage(`{"id":"v5","title":"Cal","type":6,"order":5,"created_at":"2026-01-05 00:00:00+00:00","view":{"calendar_range":[{"fk_from_column_id":"c4","fk_to_column_id":null}]}}`)},
		{View: json.RawMessage(`{"id":"v6","title":"Map","type":5,"order":6,"created_at":"2026-01-06 00:00:00+00:00","view":{"fk_geo_data_col_id":"c3"}}`)},
		{View: json.RawMessage(`{"id":"v7","title":"Timeline","type":9,"order":7,"created_at":"2026-01-07 00:00:00+00:00","view":{}}`)},
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteHeader(Header{CreatedAt: time.Unix(0, 0), Source: Source{BaseID: "p1"}, Base: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteTable(tb); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRestoreViews(t *testing.T) {
	api := newFakeNoco()
	report, err := Restore(context.Background(), api, opener(viewsFixture(t)), RestoreOptions{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	tasks := api.tableByTitle("Tasks")
	views := api.views[tasks.id]
	var titles []string
	for _, v := range views {
		titles = append(titles, fmt.Sprintf("%s:%d", v.title, v.viewType))
	}
	// The default view is reused and renamed; the others are created in
	// order; the map (skipped field) and the unknown type are not.
	if want := []string{"All tasks:3", "Board:4", "Submit:1", "Shared:3", "Cal:6"}; !slices.Equal(titles, want) {
		t.Fatalf("views = %v, want %v", titles, want)
	}
	if report.ViewCount != 5 || report.Tables[0].ViewCount != 5 {
		t.Fatalf("view count = %d / %+v", report.ViewCount, report.Tables)
	}
	name, status, due := tasks.fieldID("Name"), tasks.fieldID("Status"), tasks.fieldID("Due")
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	def := views[0]
	if got := js(def.settings); got != `[{"description":"everything","lock_type":"locked","meta":{"rowColoringInfo":null},"order":2,"title":"All tasks"},{"row_height":2}]` {
		t.Fatalf("default view settings = %s", got)
	}
	// Columns: Name moved first and widened, Due hidden; Where (skipped)
	// and the system column are left alone. The form's Name gets its
	// label and required flag.
	want := fmt.Sprintf(`{"%s/%s":{"label":"Task name","order":1,"required":true},"%s/%s":{"order":1,"width":"300px"},"%s/%s":{"order":2,"show":0}}`,
		views[2].id, name, def.id, name, def.id, due)
	if got := js(api.colPatches); got != want {
		t.Fatalf("column patches = %s", got)
	}
	if got := js(def.sorts); got != fmt.Sprintf(`[{"direction":"desc","fk_column_id":"%s"}]`, name) {
		t.Fatalf("sorts = %s", got)
	}
	// The group is created, then its member with the new parent; the
	// filter on Where is dropped.
	if len(def.filters) != 3 {
		t.Fatalf("filters = %s", js(def.filters))
	}
	group, member, top := def.filters[0], def.filters[1], def.filters[2]
	if group["is_group"] != true || group["logical_op"] != "or" || member["fk_parent_id"] != group["id"] ||
		member["fk_column_id"] != status || member["value"] != "Todo" ||
		top["fk_column_id"] != due || top["comparison_sub_op"] != "today" || top["fk_parent_id"] != nil {
		t.Fatalf("filters = %s", js(def.filters))
	}

	board := views[1]
	if board.body["fk_grp_col_id"] != status {
		t.Fatalf("kanban body = %v", board.body)
	}
	// Stacks keyed by the new field, matched by choice title; the stack of
	// a choice that no longer exists is dropped.
	if got := js(board.settings); got != fmt.Sprintf(`[{"order":1},{"meta":{"fk_cover_image_object_fit":"fit","%[1]s":[{"id":"uncategorized","order":0},{"collapsed":true,"fk_column_id":"%[1]s","id":"ch-Done","order":1,"title":"Done"},{"fk_column_id":"%[1]s","id":"ch-Todo","order":2,"title":"Todo"}]}}]`, status) {
		t.Fatalf("kanban settings = %s", got)
	}
	if got := js(views[2].settings); got != `[{"order":3},{"heading":"Submit a task","success_msg":"thanks"}]` {
		t.Fatalf("form settings = %s", got)
	}
	if got := js(views[4].body); got != fmt.Sprintf(`{"calendar_range":[{"fk_from_column_id":"%s"}],"title":"Cal"}`, due) {
		t.Fatalf("calendar body = %s", got)
	}

	var got []string
	for _, w := range report.Warnings {
		if strings.HasPrefix(w.Code, "view_") {
			got = append(got, fmt.Sprintf("%s|%s|%s|%d|%s", w.Code, w.View, w.Field, w.Count, w.Message))
		}
	}
	want = strings.Join([]string{
		"view_setting_skipped|All tasks|Where|1|a sort on a field that was not restored",
		"view_setting_skipped|All tasks|Where|1|a filter on a field that was not restored",
		"view_setting_skipped|Submit||1|form banner and logo images are not restored",
		"view_setting_skipped|Shared||1|the view was shared; share links are not restored",
		"view_skipped|Map|Where|1|its location field was not restored",
		"view_skipped|Timeline||1|view type 9 can't be created through the API",
	}, "\n")
	if strings.Join(got, "\n") != want {
		t.Fatalf("view warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestBuildCapturesViews(t *testing.T) {
	api := &fakeAPI{
		tables:  []nocodb.TableSummary{{ID: "m1", Title: "Tasks"}},
		schemas: map[string]*nocodb.Table{"m1": schema("m1", "Tasks", `[{"id":"c1","title":"Name","type":"SingleLineText"}]`)},
		views:   map[string][]json.RawMessage{"m1": raws(`{"id":"v1","title":"Tasks","type":3}`)},
		columns: map[string][]json.RawMessage{"v1": raws(`{"id":"g1","fk_column_id":"c1","show":1}`)},
		sorts:   map[string][]json.RawMessage{"v1": raws(`{"id":"so1","fk_column_id":"c1","direction":"asc"}`)},
		filters: map[string][]json.RawMessage{"v1": raws(`{"id":"f1","is_group":true}`, `{"id":"f4","fk_column_id":"c1"}`)},
		children: map[string][]json.RawMessage{
			"f1": raws(`{"id":"f2","fk_parent_id":"f1","is_group":1}`),
			"f2": raws(`{"id":"f3","fk_parent_id":"f2","fk_column_id":"c1"}`),
		},
	}
	var buf bytes.Buffer
	stats, err := Build(context.Background(), api, Source{BaseID: "p1"}, &buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.ViewCount != 1 || stats.Tables[0].ViewCount != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	_, tb, err := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if err != nil {
		t.Fatal(err)
	}
	v := tb.Views[0]
	var ids []string
	for _, f := range v.Filters {
		ids = append(ids, jsonString(f, "id"))
	}
	// Groups come before their members, depth first.
	if !slices.Equal(ids, []string{"f1", "f2", "f3", "f4"}) || len(v.Columns) != 1 || len(v.Sorts) != 1 {
		t.Fatalf("view = %+v (filters %v)", v, ids)
	}
}
