package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// Views are restored last, through v2 meta (v3 views are licence-gated in
// OSS). The new table's default grid view takes the snapshot's default
// view's settings; every other view is created by type. Then each view
// gets its common and type-specific settings, column settings, sorts and
// filters, with field IDs remapped. Share links, row colouring and form
// images are not restored.

// srcView is a captured view with the fields a restore reads.
type srcView struct {
	View
	id, title string
	viewType  int
	order     float64
	createdAt string
	settings  json.RawMessage // the type-specific "view" object
}

func parseView(v View) srcView {
	var head struct {
		ID        string          `json:"id"`
		Title     string          `json:"title"`
		Type      int             `json:"type"`
		Order     float64         `json:"order"`
		CreatedAt string          `json:"created_at"`
		View      json.RawMessage `json:"view"`
	}
	_ = json.Unmarshal(v.View, &head)
	return srcView{View: v, id: head.ID, title: head.Title, viewType: head.Type, order: head.Order, createdAt: head.CreatedAt, settings: head.View}
}

// defaultView picks the view NocoDB created with the table: the one
// flagged is_default, else the earliest-created grid.
func defaultView(views []srcView) int {
	best := -1
	for i, v := range views {
		if jsonBool(v.View.View, "is_default") {
			return i
		}
		if v.viewType != nocodb.ViewTypeGrid {
			continue
		}
		if best < 0 || v.createdAt < views[best].createdAt ||
			(v.createdAt == views[best].createdAt && v.order < views[best].order) {
			best = i
		}
	}
	return best
}

func (r *restorer) restoreViews(ctx context.Context) error {
	n := 0
	_, err := r.read(func(st *Table) error {
		n++
		t := r.byID[st.ID]
		if t == nil || t.newID == "" || len(st.Views) == 0 {
			return nil
		}
		r.opts.Progress(fmt.Sprintf("[%d/%d] %s: views", n, len(r.tables), t.title))
		if err := r.restoreTableViews(ctx, t, st.Views); err != nil {
			return fmt.Errorf("table %q: views: %w", t.title, err)
		}
		return nil
	})
	return err
}

func (r *restorer) restoreTableViews(ctx context.Context, t *planTable, captured []View) error {
	views := make([]srcView, len(captured))
	for i, v := range captured {
		views[i] = parseView(v)
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].order < views[j].order })
	def := defaultView(views)

	existing, err := r.api.ListViews(ctx, t.newID)
	if err != nil {
		return err
	}
	var target srcView
	for _, raw := range existing {
		if v := parseView(View{View: raw}); v.viewType == nocodb.ViewTypeGrid {
			target = v
			break
		}
	}

	for i, v := range views {
		id := ""
		rename := false
		if i == def && target.id != "" {
			id, rename = target.id, v.title != target.title
		} else {
			if id, err = r.createView(ctx, t, v); err != nil {
				return err
			}
			if id == "" {
				continue
			}
		}
		t.stats.ViewCount++
		if err := r.restoreView(ctx, t, v, id, rename); err != nil {
			return err
		}
	}
	return nil
}

// createView creates v on the new table and returns its ID, or "" when the
// view can't be created (with a warning).
func (r *restorer) createView(ctx context.Context, t *planTable, v srcView) (string, error) {
	if nocodb.ViewKind(v.viewType) == "" {
		r.warnView(WarnViewSkipped, t.title, v.title, "", fmt.Sprintf("view type %d can't be created through the API", v.viewType))
		return "", nil
	}
	body := map[string]any{"title": v.title}
	need := func(key, what string) bool {
		old := jsonString(v.settings, key)
		if r.ids[old] == "" {
			r.warnView(WarnViewSkipped, t.title, v.title, r.fieldTitle(t, old), "its "+what+" field was not restored")
			return false
		}
		body[key] = r.ids[old]
		return true
	}
	switch v.viewType {
	case nocodb.ViewTypeKanban:
		if !need("fk_grp_col_id", "grouping") {
			return "", nil
		}
	case nocodb.ViewTypeMap:
		if !need("fk_geo_data_col_id", "location") {
			return "", nil
		}
	case nocodb.ViewTypeCalendar:
		ranges, ok := r.calendarRanges(t, v)
		if !ok {
			return "", nil
		}
		body["calendar_range"] = ranges
	}
	id, err := r.api.CreateView(ctx, t.newID, v.viewType, body)
	if err != nil {
		if !rejected(err) {
			return "", fmt.Errorf("create view %q: %w", v.title, err)
		}
		r.warnView(WarnViewSkipped, t.title, v.title, "", apiMessage(err))
		return "", nil
	}
	return id, nil
}

// calendarRanges remaps a calendar's date ranges; ok is false (with a
// warning) when a range's field was not restored.
func (r *restorer) calendarRanges(t *planTable, v srcView) ([]map[string]string, bool) {
	var parsed struct {
		Ranges []struct {
			From string `json:"fk_from_column_id"`
			To   string `json:"fk_to_column_id"`
		} `json:"calendar_range"`
	}
	_ = json.Unmarshal(v.settings, &parsed)
	var out []map[string]string
	for _, rg := range parsed.Ranges {
		for _, id := range []string{rg.From, rg.To} {
			if id != "" && r.ids[id] == "" {
				r.warnView(WarnViewSkipped, t.title, v.title, r.fieldTitle(t, id), "its date field was not restored")
				return nil, false
			}
		}
		m := map[string]string{"fk_from_column_id": r.ids[rg.From]}
		if rg.To != "" {
			m["fk_to_column_id"] = r.ids[rg.To]
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		r.warnView(WarnViewSkipped, t.title, v.title, "", "it has no date field")
		return nil, false
	}
	return out, true
}

// restoreView applies v's settings, column settings, sorts and filters to
// the new view id.
func (r *restorer) restoreView(ctx context.Context, t *planTable, v srcView, id string, rename bool) error {
	// setting notes a setting NocoDB refused and goes on; other errors
	// stop the restore.
	setting := func(what string, err error) error {
		switch {
		case err == nil:
			return nil
		case rejected(err):
			r.warnView(WarnViewSettingSkipped, t.title, v.title, "", what+": "+apiMessage(err))
			return nil
		}
		return fmt.Errorf("view %q %s: %w", v.title, what, err)
	}

	common := map[string]any{}
	if rename {
		common["title"] = v.title
	}
	for _, key := range []string{"order", "lock_type", "description", "show_system_fields"} {
		if val := jsonValue(v.View.View, key); val != nil {
			common[key] = val
		}
	}
	if meta := r.viewMeta(jsonRaw(v.View.View, "meta")); meta != nil {
		common["meta"] = meta
	}
	if len(common) > 0 {
		if err := setting("settings", r.api.UpdateView(ctx, id, common)); err != nil {
			return err
		}
	}
	patch, err := r.viewTypeSettings(ctx, t, v)
	if err != nil {
		return err
	}
	if len(patch) > 0 {
		if err := setting(nocodb.ViewKind(v.viewType)+" settings", r.api.UpdateViewSettings(ctx, v.viewType, id, patch)); err != nil {
			return err
		}
	}

	// What the restore leaves out on purpose.
	if jsonString(v.View.View, "uuid") != "" {
		r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "the view was shared; share links are not restored")
	}
	if jsonValue(v.View.View, "row_coloring_mode") != nil {
		r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "row colouring is not restored")
	}
	if v.viewType == nocodb.ViewTypeForm &&
		(jsonValue(v.settings, "banner_image_url") != nil || jsonValue(v.settings, "logo_url") != nil) {
		r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "form banner and logo images are not restored")
	}

	if err := r.restoreViewColumns(ctx, t, v, id, setting); err != nil {
		return err
	}
	if err := r.restoreSorts(ctx, t, v, id); err != nil {
		return err
	}
	return r.restoreFilters(ctx, t, v, id)
}

// Type-specific settings copied as they are; field references are
// remapped separately.
var viewSettingKeys = map[int][]string{
	nocodb.ViewTypeGrid: {"row_height"},
	nocodb.ViewTypeForm: {"heading", "subheading", "success_msg", "redirect_url", "redirect_after_secs", "email",
		"submit_another_form", "show_blank_form", "meta", "save_draft_to_browser", "starts_at", "expires_at"},
	nocodb.ViewTypeGallery:  {"meta"},
	nocodb.ViewTypeCalendar: {"meta"},
	nocodb.ViewTypeMap:      {"meta"},
}

func (r *restorer) viewTypeSettings(ctx context.Context, t *planTable, v srcView) (map[string]any, error) {
	patch := map[string]any{}
	for _, key := range viewSettingKeys[v.viewType] {
		if val := jsonValue(v.settings, key); val != nil {
			patch[key] = val
		}
	}
	switch v.viewType {
	case nocodb.ViewTypeGallery, nocodb.ViewTypeKanban, nocodb.ViewTypeCalendar:
		if old := jsonString(v.settings, "fk_cover_image_col_id"); old != "" && r.ids[old] != "" {
			patch["fk_cover_image_col_id"] = r.ids[old]
		}
	}
	if v.viewType == nocodb.ViewTypeKanban {
		meta, err := r.kanbanMeta(ctx, t, v)
		if err != nil {
			return nil, err
		}
		if meta != nil {
			patch["meta"] = meta
		}
	}
	return patch, nil
}

// viewMeta copies a view's meta with field IDs remapped. The kanban
// grouping column NocoDB caches there is rebuilt by NocoDB.
func (r *restorer) viewMeta(raw json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil
	}
	delete(m, "groupingFieldColumn")
	if len(m) == 0 {
		return nil
	}
	return remapValue(m, r.ids).(map[string]any)
}

// kanbanMeta rewrites a kanban's stacks (order, colour, collapsed) for
// the new grouping field: the stack list is keyed by the field's ID and
// each stack by its choice's ID, matched here by choice title.
func (r *restorer) kanbanMeta(ctx context.Context, t *planTable, v srcView) (map[string]any, error) {
	var meta map[string]any
	if json.Unmarshal(jsonRaw(v.settings, "meta"), &meta) != nil {
		return nil, nil
	}
	oldField := jsonString(v.settings, "fk_grp_col_id")
	newField := r.ids[oldField]
	table, err := r.api.GetTable(ctx, r.baseID, t.newID)
	if err != nil {
		return nil, fmt.Errorf("view %q: read choices: %w", v.title, err)
	}
	choices := map[string]string{} // title -> new choice id
	for _, f := range table.Fields {
		if f.ID != newField {
			continue
		}
		var opts struct {
			Choices []struct{ ID, Title string } `json:"choices"`
		}
		_ = json.Unmarshal(f.Options, &opts)
		for _, c := range opts.Choices {
			choices[c.Title] = c.ID
		}
	}
	out := map[string]any{}
	for key, val := range meta {
		stacks, ok := val.([]any)
		if key != oldField || !ok {
			out[key] = remapValue(val, r.ids)
			continue
		}
		var kept []any
		for _, s := range stacks {
			stack, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if stack["id"] != "uncategorized" {
				title, _ := stack["title"].(string)
				id, ok := choices[title]
				if !ok {
					continue
				}
				stack["id"] = id
				stack["fk_column_id"] = newField
			}
			delete(stack, "base_id")
			delete(stack, "fk_workspace_id")
			kept = append(kept, stack)
		}
		out[newField] = kept
	}
	return out, nil
}

// Column settings copied per view type, besides show and order.
var columnSettingKeys = map[int][]string{
	nocodb.ViewTypeGrid: {"width", "group_by", "group_by_order", "group_by_sort", "aggregation"},
	nocodb.ViewTypeForm: {"label", "help", "description", "required", "meta", "enable_scanner"},
}

// restoreViewColumns copies show, order and the type's column settings
// where they differ from the new view's defaults. Columns of NocoDB's
// hidden system fields (not in the snapshot schema) and of fields the
// restore skipped are left as they are.
func (r *restorer) restoreViewColumns(ctx context.Context, t *planTable, v srcView, id string, setting func(string, error) error) error {
	if len(v.Columns) == 0 {
		return nil
	}
	current, err := r.api.ListViewColumns(ctx, id)
	if err != nil {
		return fmt.Errorf("view %q columns: %w", v.title, err)
	}
	byField := map[string]json.RawMessage{}
	for _, c := range current {
		byField[jsonString(c, "fk_column_id")] = c
	}
	for _, c := range v.Columns {
		f := t.field(jsonString(c, "fk_column_id"))
		if f == nil || !f.restored(r.ids) {
			continue
		}
		cur, ok := byField[r.ids[f.ID]]
		if !ok {
			continue
		}
		colID := jsonString(cur, "id")
		basic := diff(c, cur, "show", "order")
		if len(basic) > 0 {
			if err := setting("column "+f.Title, r.api.UpdateViewColumn(ctx, id, colID, basic)); err != nil {
				return err
			}
		}
		extra := diff(c, cur, columnSettingKeys[v.viewType]...)
		if len(extra) == 0 {
			continue
		}
		switch v.viewType {
		case nocodb.ViewTypeGrid:
			err = r.api.UpdateGridColumn(ctx, colID, extra)
		case nocodb.ViewTypeForm:
			err = r.api.UpdateFormColumn(ctx, colID, extra)
		}
		if err := setting("column "+f.Title, err); err != nil {
			return err
		}
	}
	return nil
}

// diff returns the keys whose value in src differs from cur, with src's
// values.
func diff(src, cur json.RawMessage, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if canonical(jsonRaw(src, k)) != canonical(jsonRaw(cur, k)) {
			out[k] = jsonValue(src, k)
		}
	}
	return out
}

// canonical renders a JSON value so equal values compare equal; absent is
// null.
func canonical(raw json.RawMessage) string {
	var v any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &v) != nil {
		return "null"
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// restoreSorts re-creates a view's sorts in order. A sort on a field the
// restore skipped is dropped with a warning.
func (r *restorer) restoreSorts(ctx context.Context, t *planTable, v srcView, id string) error {
	for _, s := range v.Sorts {
		body, missing := r.remapRefs(s, "fk_column_id", "fk_lookup_col_id")
		if missing != "" {
			r.warnView(WarnViewSettingSkipped, t.title, v.title, r.fieldTitle(t, missing), "a sort on a field that was not restored")
			continue
		}
		if dir := jsonValue(s, "direction"); dir != nil {
			body["direction"] = dir
		}
		if err := r.api.CreateSort(ctx, id, body); err != nil {
			if !rejected(err) {
				return fmt.Errorf("view %q sort: %w", v.title, err)
			}
			r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "sort: "+apiMessage(err))
		}
	}
	return nil
}

// Filter keys copied as they are.
var filterKeys = []string{"comparison_op", "comparison_sub_op", "value", "logical_op", "enabled", "meta"}

// restoreFilters re-creates a view's filters, groups before their members.
// A filter on a field the restore skipped is dropped with a warning, and
// so is everything in a group that could not be created: the view then
// shows more records than the snapshot's did.
func (r *restorer) restoreFilters(ctx context.Context, t *planTable, v srcView, id string) error {
	created := map[string]string{} // snapshot filter id -> new id
	for _, f := range v.Filters {
		parent := jsonString(f, "fk_parent_id")
		if parent != "" && created[parent] == "" {
			r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "a filter in a group that was not restored")
			continue
		}
		var body map[string]any
		if jsonBool(f, "is_group") {
			body = map[string]any{"is_group": true}
		} else {
			var missing string
			body, missing = r.remapRefs(f, "fk_column_id", "fk_link_col_id", "fk_value_col_id", "fk_parent_column_id")
			if missing != "" {
				r.warnView(WarnViewSettingSkipped, t.title, v.title, r.fieldTitle(t, missing), "a filter on a field that was not restored")
				continue
			}
		}
		for _, key := range filterKeys {
			if val := jsonValue(f, key); val != nil {
				body[key] = val
			}
		}
		if parent != "" {
			body["fk_parent_id"] = created[parent]
		}
		newID, err := r.api.CreateFilter(ctx, id, body)
		if err != nil {
			if !rejected(err) {
				return fmt.Errorf("view %q filter: %w", v.title, err)
			}
			r.warnView(WarnViewSettingSkipped, t.title, v.title, "", "filter: "+apiMessage(err))
			continue
		}
		created[jsonString(f, "id")] = newID
	}
	return nil
}

// remapRefs returns the given field references of raw that are set, with
// new IDs, or the first one that has no new ID.
func (r *restorer) remapRefs(raw json.RawMessage, keys ...string) (map[string]any, string) {
	out := map[string]any{}
	for _, k := range keys {
		old := jsonString(raw, k)
		if old == "" {
			continue
		}
		if r.ids[old] == "" {
			return nil, old
		}
		out[k] = r.ids[old]
	}
	return out, ""
}

// fieldTitle names a snapshot field of t (or any table) for a warning.
func (r *restorer) fieldTitle(t *planTable, id string) string {
	if f := t.field(id); f != nil {
		return f.Title
	}
	for _, other := range r.tables {
		if f := other.field(id); f != nil {
			return other.title + "." + f.Title
		}
	}
	return ""
}

func jsonRaw(raw json.RawMessage, key string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m[key]
}

// jsonValue returns raw[key] decoded, or nil when it is absent or null.
func jsonValue(raw json.RawMessage, key string) any {
	v := bytes.TrimSpace(jsonRaw(raw, key))
	if len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return nil
	}
	var out any
	if json.Unmarshal(v, &out) != nil {
		return nil
	}
	return out
}

// rejected reports whether NocoDB refused one request (bad input, or
// something it references is gone), so the restore can note it and go on.
func rejected(err error) bool {
	return nocodb.IsBadRequest(err) || nocodb.IsNotFound(err)
}
