package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

func TestBuildCapturesHooks(t *testing.T) {
	api := &fakeAPI{
		tables: []nocodb.TableSummary{{ID: "m1", Title: "Tasks"}, {ID: "m2", Title: "Empty"}},
		schemas: map[string]*nocodb.Table{
			"m1": schema("m1", "Tasks", `[{"id":"c1","title":"Name","type":"SingleLineText"}]`),
			"m2": schema("m2", "Empty", `[]`),
		},
		hooks: map[string][]json.RawMessage{"m1": raws(
			`{"id":"h1","title":"Changed","active":true,"version":"v3","notification":"{\"type\":\"URL\",\"payload\":{\"headers\":[{\"name\":\"Authorization\",\"value\":\"secret\"}]}}"}`,
			`{"id":"h2","title":"Chat","notification":"{\"type\":\"Slack\"}"}`,
		)},
		hookFilters: map[string][]json.RawMessage{"h1": raws(`{"id":"f1","is_group":true}`, `{"id":"f4","fk_column_id":"c1"}`)},
		children: map[string][]json.RawMessage{
			"f1": raws(`{"id":"f2","fk_parent_id":"f1","is_group":1}`),
			"f2": raws(`{"id":"f3","fk_parent_id":"f2","fk_column_id":"c1","comparison_op":"eq","value":"Ada"}`),
		},
	}
	var buf bytes.Buffer
	stats, err := Build(t.Context(), api, Source{BaseID: "p1"}, &buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, tb, err := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Hooks) != 2 {
		t.Fatalf("hooks = %+v, want 2", tb.Hooks)
	}
	for i, h := range tb.Hooks {
		if !bytes.Equal(h.Hook, api.hooks["m1"][i]) {
			t.Errorf("hook %d = %s, want verbatim %s", i, h.Hook, api.hooks["m1"][i])
		}
	}
	var ids []string
	for _, f := range tb.Hooks[0].Filters {
		ids = append(ids, jsonString(f, "id"))
	}
	if !slices.Equal(ids, []string{"f1", "f2", "f3", "f4"}) || !bytes.Equal(tb.Hooks[0].Filters[2], api.children["f2"][0]) {
		t.Fatalf("hook filters = %s", tb.Hooks[0].Filters)
	}
	if stats.HookCount != 2 || stats.Tables[0].HookCount != 2 || stats.Tables[1].HookCount != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	_, empty, err := ReadTable(bytes.NewReader(buf.Bytes()), "m2")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Hooks == nil || len(empty.Hooks) != 0 {
		t.Fatalf("captured empty hooks = %+v, want [] (not absent)", empty.Hooks)
	}
}

func hooksFixture(t *testing.T, hooks ...Hook) []byte {
	t.Helper()
	tb := &Table{
		ID: "m1", Title: "Tasks", Schema: json.RawMessage(`{"id":"m1","title":"Tasks","fields":[
			{"id":"c0","title":"Id","type":"ID"},
			{"id":"c1","title":"Name","type":"SingleLineText"},
			{"id":"c2","title":"Due","type":"Date"},
			{"id":"c3","title":"Where","type":"GeoData"}
		]}`),
		Records: []nocodb.Record{rec(1, `{"Name":"Ada"}`)},
		Views:   []View{{View: json.RawMessage(`{"id":"v1","title":"Tasks","type":3,"view":{"row_height":2}}`)}},
		Hooks:   hooks,
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteHeader(Header{Source: Source{BaseID: "p1"}}); err != nil {
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

func TestRestoreHooksDisabledWithRemappedConditions(t *testing.T) {
	doc := hooksFixture(t, Hook{
		Hook: json.RawMessage(`{"id":"h1","fk_model_id":"m1","title":"Changed","description":"notify on change","active":true,"event":"after","operation":["insert","update"],"notification":"{\"type\":\"URL\",\"payload\":{\"path\":\"https://example.com/hook\",\"headers\":[{\"name\":\"Authorization\",\"value\":\"secret\"}]}}","condition":true,"retries":4,"retry_interval":2000,"timeout":10000,"version":"v3","trigger_field":true,"trigger_fields":["c1","c2"],"created_at":"2026-01-01"}`),
		Filters: raws(
			`{"id":"f1","is_group":1,"logical_op":"and"}`,
			`{"id":"f2","is_group":true,"fk_parent_id":"f1","logical_op":"or"}`,
			`{"id":"f3","fk_parent_id":"f2","fk_column_id":"c1","comparison_op":"eq","value":"Ada","logical_op":"or"}`,
			`{"id":"f4","fk_column_id":"c2","comparison_op":"isWithin","comparison_sub_op":"pastNumberOfDays","value":"30","logical_op":"and","enabled":false}`,
		),
	})
	api := newFakeNoco()
	report, err := Restore(t.Context(), api, opener(doc), RestoreOptions{Title: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	tasks := api.tableByTitle("Tasks")
	if len(api.hooks[tasks.id]) != 1 {
		t.Fatalf("hooks = %+v, want 1", api.hooks[tasks.id])
	}
	h := api.hooks[tasks.id][0]
	name, due := tasks.fieldID("Name"), tasks.fieldID("Due")
	body, _ := json.Marshal(h.body)
	want := fmt.Sprintf(`{"active":false,"condition":true,"description":"notify on change","event":"after","notification":{"payload":{"headers":[{"name":"Authorization","value":"secret"}],"path":"https://example.com/hook"},"type":"URL"},"operation":["insert","update"],"retries":4,"retry_interval":2000,"timeout":10000,"title":"Changed","trigger_field":true,"trigger_fields":[%q,%q],"version":"v3"}`, name, due)
	if string(body) != want {
		t.Fatalf("hook body = %s, want %s", body, want)
	}
	if len(h.filters) != 4 {
		t.Fatalf("hook filters = %+v", h.filters)
	}
	group, nested, member, top := h.filters[0], h.filters[1], h.filters[2], h.filters[3]
	if group["is_group"] != true || nested["fk_parent_id"] != group["id"] ||
		member["fk_parent_id"] != nested["id"] || member["fk_column_id"] != name || member["value"] != "Ada" ||
		top["fk_column_id"] != due || top["comparison_sub_op"] != "pastNumberOfDays" || top["enabled"] != false {
		t.Fatalf("hook filters = %+v", h.filters)
	}
	if report.HookCount != 1 || report.Tables[0].HookCount != 1 {
		t.Fatalf("hook count = %d / %+v", report.HookCount, report.Tables)
	}
	var warnings []Warning
	for _, w := range report.Warnings {
		if w.Code == "hooks_disabled" {
			warnings = append(warnings, w)
		}
	}
	if len(warnings) != 1 || warnings[0].Table != "Tasks" || warnings[0].Count != 1 {
		t.Fatalf("disabled warnings = %+v", warnings)
	}
}

func TestRestoreHooksWarnForIntegrationsAndRejectedVersions(t *testing.T) {
	hook := func(title, typ, version string) Hook {
		return Hook{Hook: json.RawMessage(fmt.Sprintf(`{"title":%q,"active":false,"event":"after","operation":["update"],"notification":{"type":%q,"payload":{"token":"secret"}},"version":%q}`, title, typ, version))}
	}
	doc := hooksFixture(t, hook("URL", "URL", "v3"), hook("Chat", "Slack", "v3"), hook("Mail", "Email", "v3"), hook("Legacy", "URL", "v2"))
	api := newFakeNoco()
	report, err := Restore(t.Context(), api, opener(doc), RestoreOptions{Title: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	if report.HookCount != 3 {
		t.Fatalf("hook count = %d, want 3", report.HookCount)
	}
	var setup, skipped, disabled []Warning
	for _, w := range report.Warnings {
		switch w.Code {
		case "hook_needs_setup":
			setup = append(setup, w)
		case "hook_skipped":
			skipped = append(skipped, w)
		case "hooks_disabled":
			disabled = append(disabled, w)
		}
	}
	if len(setup) != 2 || setup[0].Message != `webhook "Chat": Slack notifications need the target instance's integration` ||
		setup[1].Message != `webhook "Mail": Email notifications need the target instance's integration` ||
		setup[0].Count != 1 || setup[1].Count != 1 {
		t.Fatalf("integration warnings = %+v", setup)
	}
	if len(skipped) != 1 || skipped[0].Message != `webhook "Legacy": hook version is deprecated / not supported anymore` {
		t.Fatalf("skipped warnings = %+v", skipped)
	}
	if len(disabled) != 1 || disabled[0].Count != 3 || disabled[0].Table != "Tasks" {
		t.Fatalf("disabled warnings = %+v", disabled)
	}
}

func TestRestoreHooksDropsConditionsOnSkippedFields(t *testing.T) {
	doc := hooksFixture(t, Hook{
		Hook: json.RawMessage(`{"title":"Changed","notification":{"type":"URL"},"version":"v3","condition":true}`),
		Filters: raws(
			`{"id":"f1","fk_column_id":"c3","comparison_op":"notblank"}`,
			`{"id":"f2","fk_column_id":"c1","comparison_op":"eq","value":"Ada"}`,
		),
	}, Hook{Hook: json.RawMessage(`{"title":"Location changed","notification":{"type":"URL"},"version":"v3","trigger_field":true,"trigger_fields":["c3"]}`)})
	api := newFakeNoco()
	report, err := Restore(t.Context(), api, opener(doc), RestoreOptions{Title: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	tasks := api.tableByTitle("Tasks")
	hooks := api.hooks[tasks.id]
	if report.HookCount != 1 || len(hooks) != 1 || len(hooks[0].filters) != 1 || hooks[0].filters[0]["fk_column_id"] != tasks.fieldID("Name") {
		t.Fatalf("report = %+v, hooks = %+v", report, hooks)
	}
	var skipped []Warning
	for _, w := range report.Warnings {
		if w.Code == "hook_skipped" {
			skipped = append(skipped, w)
		}
	}
	if len(skipped) != 2 || skipped[0].Message != `webhook "Changed": field Where: a filter on a field that was not restored` ||
		skipped[1].Message != `webhook "Location changed": a trigger field was not restored` {
		t.Fatalf("skipped hook warnings = %+v", skipped)
	}
}

func TestRestoreRejectedHookFilterGroupDropsItsMembers(t *testing.T) {
	doc := hooksFixture(t, Hook{
		Hook: json.RawMessage(`{"title":"Changed","notification":{"type":"URL"},"version":"v3","condition":true}`),
		Filters: raws(
			`{"id":"f1","is_group":true}`,
			`{"id":"f2","fk_parent_id":"f1","fk_column_id":"c1","comparison_op":"eq","value":"Ada"}`,
			`{"id":"f3","fk_column_id":"c1","comparison_op":"eq","value":"Bob"}`,
		),
	})
	api := &hookFailureAPI{fakeNoco: newFakeNoco(), rejectGroup: true}
	report, err := Restore(t.Context(), api, opener(doc), RestoreOptions{Title: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	hook := api.hooks[api.tableByTitle("Tasks").id][0]
	if report.HookCount != 1 || len(hook.filters) != 1 || hook.filters[0]["value"] != "Bob" || hook.body["active"] != false {
		t.Fatalf("report = %+v, hook = %+v", report, hook)
	}
	var n int64
	for _, w := range report.Warnings {
		if w.Code == "hook_skipped" {
			n += w.Count
		}
	}
	if n != 2 {
		t.Fatalf("skipped filters = %d, want 2", n)
	}
}

func TestRestoreHookOperationalErrorsAreFatal(t *testing.T) {
	doc := hooksFixture(t, Hook{
		Hook:    json.RawMessage(`{"title":"Changed","notification":{"type":"URL"},"version":"v3"}`),
		Filters: raws(`{"id":"f1","fk_column_id":"c1","comparison_op":"eq","value":"Ada"}`),
	})
	for _, stage := range []string{"hook", "filter"} {
		for _, failure := range []error{
			&nocodb.APIError{StatusCode: 401, Message: "unauthorized"},
			&nocodb.APIError{StatusCode: 500, Message: "server error"},
			context.Canceled,
		} {
			t.Run(stage+"/"+failure.Error(), func(t *testing.T) {
				api := &hookFailureAPI{fakeNoco: newFakeNoco()}
				if stage == "hook" {
					api.hookErr = failure
				} else {
					api.filterErr = failure
				}
				report, err := Restore(t.Context(), api, opener(doc), RestoreOptions{Title: "restored"})
				if !errors.Is(err, failure) || report.BaseID != "pNEW" {
					t.Fatalf("report = %+v, err = %v, want %v", report, err, failure)
				}
				want := 0
				if stage == "filter" {
					want = 1
				}
				if report.HookCount != want {
					t.Fatalf("partial hook count = %d, want %d", report.HookCount, want)
				}
			})
		}
	}
}

type hookFailureAPI struct {
	*fakeNoco
	hookErr, filterErr error
	rejectGroup        bool
}

func (f *hookFailureAPI) CreateHook(ctx context.Context, tableID string, body any) (string, error) {
	if f.hookErr != nil {
		return "", f.hookErr
	}
	return f.fakeNoco.CreateHook(ctx, tableID, body)
}

func (f *hookFailureAPI) CreateHookFilter(ctx context.Context, hookID string, body any) (string, error) {
	if f.filterErr != nil {
		return "", f.filterErr
	}
	if f.rejectGroup && asMap(body)["is_group"] == true {
		return "", badRequest("unsupported filter group")
	}
	return f.fakeNoco.CreateHookFilter(ctx, hookID, body)
}

type fakeHook struct {
	id      string
	body    map[string]any
	filters []map[string]any
}

func (f *fakeNoco) CreateHook(_ context.Context, tableID string, body any) (string, error) {
	b := asMap(body)
	if b["active"] != false {
		return "", badRequest("restored hooks must be inactive")
	}
	if b["version"] != "v3" {
		return "", badRequest("hook version is deprecated / not supported anymore")
	}
	// Hooks are the final pass, after data and view settings.
	if len(f.tables[tableID].records) == 0 || len(f.views[tableID][0].settings) == 0 {
		return "", fmt.Errorf("hook was created before records and views")
	}
	h := &fakeHook{id: f.id("hk"), body: b}
	f.hooks[tableID] = append(f.hooks[tableID], h)
	return h.id, nil
}

func (f *fakeNoco) CreateHookFilter(_ context.Context, hookID string, body any) (string, error) {
	for _, hs := range f.hooks {
		for _, h := range hs {
			if h.id == hookID {
				b := asMap(body)
				b["id"] = f.id("hf")
				h.filters = append(h.filters, b)
				return b["id"].(string), nil
			}
		}
	}
	return "", badRequest("hook not found")
}

// The fake exposes hooks through the same external API boundary as views.
func (f *fakeAPI) ListHooks(_ context.Context, tableID string) ([]json.RawMessage, error) {
	return f.hooks[tableID], nil
}

func (f *fakeAPI) ListHookFilters(_ context.Context, hookID string) ([]json.RawMessage, error) {
	return f.hookFilters[hookID], nil
}
