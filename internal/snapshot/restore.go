package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// WriteAPI is the slice of the NocoDB client a restore needs.
type WriteAPI interface {
	CreateBase(ctx context.Context, title, description string) (string, error)
	CreateTable(ctx context.Context, baseID string, def any) (*nocodb.Table, error)
	GetTable(ctx context.Context, baseID, tableID string) (*nocodb.Table, error)
	UpdateTable(ctx context.Context, baseID, tableID string, patch any) error
	CreateField(ctx context.Context, baseID, tableID string, def any) (*nocodb.Field, error)
	UpdateField(ctx context.Context, baseID, fieldID string, patch any) error
	InsertRecords(ctx context.Context, baseID, tableID string, records []map[string]json.RawMessage) ([]json.RawMessage, error)
	LinkRecords(ctx context.Context, baseID, tableID, fieldID, recordID string, ids []json.RawMessage) error
	ListBaseUserEmails(ctx context.Context, baseID string) ([]string, error)
	UploadFiles(ctx context.Context, files []nocodb.UploadFile) ([]json.RawMessage, error)
	UpdateRecordsV2(ctx context.Context, tableID string, rows []map[string]json.RawMessage) error
	ListViews(ctx context.Context, tableID string) ([]json.RawMessage, error)
	ListViewColumns(ctx context.Context, viewID string) ([]json.RawMessage, error)
	CreateView(ctx context.Context, tableID string, viewType int, body any) (string, error)
	UpdateView(ctx context.Context, viewID string, patch any) error
	UpdateViewSettings(ctx context.Context, viewType int, viewID string, patch any) error
	UpdateViewColumn(ctx context.Context, viewID, columnID string, patch any) error
	UpdateGridColumn(ctx context.Context, columnID string, patch any) error
	UpdateFormColumn(ctx context.Context, columnID string, patch any) error
	CreateSort(ctx context.Context, viewID string, body any) error
	CreateFilter(ctx context.Context, viewID string, body any) (string, error)
}

// RestoreOptions tune a restore.
type RestoreOptions struct {
	// Title of the new Base. Required.
	Title string
	// BatchSize is the number of records per insert request; NocoDB's
	// default cap is 10.
	BatchSize int
	// LinkBatchSize is the number of linked IDs per link request.
	LinkBatchSize int
	// Progress receives human-readable status updates. May be nil.
	Progress func(msg string)
	// OnBaseCreated is called as soon as the new Base exists, before
	// anything else can fail. May be nil.
	OnBaseCreated func(baseID string)
	// OpenFile opens a stored attachment file by sha256. Nil leaves
	// attachments out, as for a snapshot without files.
	OpenFile func(ctx context.Context, sha256 string) (io.ReadCloser, error)
}

// Warning codes: what a restore could not bring back.
const (
	WarnFieldSkipped       = "field_skipped"
	WarnAttachmentsSkipped = "attachments_skipped"
	WarnUserValuesDropped  = "user_values_dropped"
	WarnRecordsFailed      = "records_failed"
	WarnLinksFailed        = "links_failed"
	WarnDisplayField       = "display_field_not_set"
	WarnRelationGuessed    = "relation_guessed"
	// WarnViewSkipped: a whole view was not created.
	WarnViewSkipped = "view_skipped"
	// WarnViewSettingSkipped: part of a view (a filter, a sort, a
	// setting) was not restored.
	WarnViewSettingSkipped = "view_setting_skipped"
)

// Warning groups one kind of loss in one table (and view and field, when
// they apply).
type Warning struct {
	Code    string `json:"code"`
	Table   string `json:"table,omitempty"`
	Field   string `json:"field,omitempty"`
	Count   int64  `json:"count"`
	Message string `json:"message"`
	View    string `json:"view,omitempty"`
}

// RestoreReport summarizes a restore. It is returned even when the restore
// fails, holding what was done so far.
type RestoreReport struct {
	BaseID      string
	BaseTitle   string
	Tables      []TableStats
	RecordCount int64
	LinkCount   int64
	FileCount   int64
	ViewCount   int
	Warnings    []Warning
}

// Restore rebuilds the snapshot document returned by open into a new Base.
// open is called once per pass (plan, records, links), so it must return
// the document from the start each time.
//
// The order of work is fixed by what NocoDB accepts: tables with their
// plain fields, then relations (from one side; NocoDB creates the inverse),
// then records, then links between the new record IDs, then lookups,
// rollups and formulas in dependency order, then display fields, then
// attachment files, then views.
func Restore(ctx context.Context, api WriteAPI, open func() (io.ReadCloser, error), opts RestoreOptions) (*RestoreReport, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 10
	}
	if opts.LinkBatchSize <= 0 {
		opts.LinkBatchSize = 100
	}
	if opts.Progress == nil {
		opts.Progress = func(string) {}
	}
	if strings.TrimSpace(opts.Title) == "" {
		return nil, errors.New("snapshot: restore needs a base title")
	}
	r := &restorer{
		api:       api,
		open:      open,
		opts:      opts,
		ids:       map[string]string{},
		recordIDs: map[string]map[string]json.RawMessage{},
		warnings:  map[string]*Warning{},
		report:    &RestoreReport{BaseTitle: opts.Title},
	}
	err := r.run(ctx)
	r.finishReport()
	return r.report, err
}

type fieldKind int

const (
	kindSkip fieldKind = iota
	kindID
	// kindPlain fields are created with their table and their values are
	// written.
	kindPlain
	// kindSystemValued fields are created with their table, but NocoDB
	// fills their values.
	kindSystemValued
	kindLink
	kindVirtual
)

// Field types by how a restore treats them.
var (
	systemValuedTypes = []string{"CreatedTime", "LastModifiedTime", "CreatedBy", "LastModifiedBy", "AutoNumber"}
	linkTypes         = []string{"Links", "LinkToAnotherRecord"}
	virtualTypes      = []string{"Lookup", "Rollup", "Formula", "Button", "Barcode", "QrCode"}
	plainTypes        = []string{
		"SingleLineText", "LongText", "Email", "PhoneNumber", "URL",
		"Number", "Decimal", "Currency", "Percent", "Duration", "Rating",
		"Checkbox", "Date", "DateTime", "Time", "Year",
		"SingleSelect", "MultiSelect", "JSON", "Geometry",
		"Attachment", "User",
	}
)

// Keys of a v3 field definition that are sent back when recreating it.
var fieldDefKeys = []string{"title", "type", "description", "default_value", "unique", "options"}

type planField struct {
	nocodb.Field
	def  map[string]json.RawMessage
	kind fieldKind
	// skipReason is set when a field is not (or could not be) restored.
	skipReason string
}

func (f *planField) restored(ids map[string]string) bool {
	return f.kind != kindSkip && f.kind != kindID && f.skipReason == "" && ids[f.ID] != ""
}

type planTable struct {
	id, title    string
	description  json.RawMessage
	displayField string
	fields       []*planField
	newID        string
	// pkTitle is the title of the new table's ID field, which v2 record
	// updates name the record by.
	pkTitle string
	// known holds the new IDs of every field the restore has seen on the
	// new table, so an auto-created inverse link can be told apart.
	known map[string]bool
	stats TableStats
}

func (t *planTable) field(id string) *planField {
	for _, f := range t.fields {
		if f.ID == id {
			return f
		}
	}
	return nil
}

type restorer struct {
	api  WriteAPI
	open func() (io.ReadCloser, error)
	opts RestoreOptions

	header *Header
	tables []*planTable
	byID   map[string]*planTable
	// ids maps snapshot table and field IDs to their new IDs.
	ids map[string]string
	// sides holds the snapshot link fields relations were created from;
	// links are written through these only.
	sides map[string]bool
	// recordIDs maps snapshot table ID -> snapshot record ID -> new ID.
	recordIDs map[string]map[string]json.RawMessage
	members   map[string]bool

	baseID       string
	warnings     map[string]*Warning
	warningOrder []string
	report       *RestoreReport
}

func (r *restorer) run(ctx context.Context) error {
	r.opts.Progress("reading snapshot")
	if err := r.plan(); err != nil {
		return err
	}

	r.opts.Progress("creating base")
	desc := jsonString(r.header.Base, "description")
	baseID, err := r.api.CreateBase(ctx, r.opts.Title, desc)
	if err != nil {
		return fmt.Errorf("create base: %w", err)
	}
	r.baseID = baseID
	r.report.BaseID = baseID
	if r.opts.OnBaseCreated != nil {
		r.opts.OnBaseCreated(baseID)
	}

	for i, t := range r.tables {
		r.opts.Progress(fmt.Sprintf("[%d/%d] %s: creating table", i+1, len(r.tables), t.title))
		if err := r.createTable(ctx, t); err != nil {
			return fmt.Errorf("table %q: %w", t.title, err)
		}
	}
	r.opts.Progress("creating relations")
	if err := r.createRelations(ctx); err != nil {
		return err
	}

	emails, err := r.api.ListBaseUserEmails(ctx, baseID)
	if err != nil {
		return fmt.Errorf("list base members: %w", err)
	}
	r.members = map[string]bool{}
	for _, e := range emails {
		r.members[strings.ToLower(e)] = true
	}
	if err := r.restoreRecords(ctx); err != nil {
		return err
	}
	if err := r.restoreLinks(ctx); err != nil {
		return err
	}

	// Computed fields come after the data: they need none written, and
	// NocoDB (2026.09.1) fails many-to-many link writes with a 500 on a
	// table that already has a lookup through another relation.
	r.opts.Progress("creating lookups, rollups and formulas")
	if err := r.createVirtualFields(ctx); err != nil {
		return err
	}
	if err := r.setDisplayFields(ctx); err != nil {
		return err
	}
	if err := r.restoreFiles(ctx); err != nil {
		return err
	}
	if err := r.restoreViews(ctx); err != nil {
		return err
	}
	r.opts.Progress("done")
	return nil
}

// plan reads every table's schema and classifies its fields. Records and
// links are dropped; later passes read them again.
func (r *restorer) plan() error {
	r.byID = map[string]*planTable{}
	header, err := r.read(func(t *Table) error {
		pt, err := planTableFrom(t)
		if err != nil {
			return fmt.Errorf("table %q: %w", t.Title, err)
		}
		r.tables = append(r.tables, pt)
		r.byID[pt.id] = pt
		return nil
	})
	if err != nil {
		return err
	}
	if header.Version < MinFormatVersion || header.Version > FormatVersion {
		return fmt.Errorf("snapshot: format version %d is not supported", header.Version)
	}
	r.header = header
	return nil
}

func planTableFrom(t *Table) (*planTable, error) {
	var schema struct {
		Description    json.RawMessage              `json:"description"`
		DisplayFieldID string                       `json:"display_field_id"`
		Fields         []map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(t.Schema, &schema); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	pt := &planTable{
		id: t.ID, title: t.Title,
		description:  schema.Description,
		displayField: schema.DisplayFieldID,
		known:        map[string]bool{},
	}
	for _, def := range schema.Fields {
		var f nocodb.Field
		raw, _ := json.Marshal(def)
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("decode field: %w", err)
		}
		pf := &planField{Field: f, def: def, kind: classify(f.Type)}
		if pf.kind == kindSkip {
			pf.skipReason = "type not supported by restore"
		}
		if pf.kind == kindVirtual && f.Type == "Button" {
			if typ := jsonString(f.Options, "type"); typ != "" && typ != "url" {
				pf.skipReason = fmt.Sprintf("%s buttons reference automations that are not in snapshots", typ)
			}
		}
		pt.fields = append(pt.fields, pf)
	}
	return pt, nil
}

func classify(typ string) fieldKind {
	switch {
	case typ == "ID":
		return kindID
	case slices.Contains(plainTypes, typ):
		return kindPlain
	case slices.Contains(systemValuedTypes, typ):
		return kindSystemValued
	case slices.Contains(linkTypes, typ):
		return kindLink
	case slices.Contains(virtualTypes, typ):
		return kindVirtual
	}
	return kindSkip
}

// createTable creates t with its plain and system-valued fields. When
// NocoDB rejects the table as a whole, it is created bare and the fields
// are added one by one, so one bad field doesn't cost the others.
func (r *restorer) createTable(ctx context.Context, t *planTable) error {
	var defs []map[string]json.RawMessage
	var withTable []*planField
	for _, f := range t.fields {
		if f.skipReason == "" && (f.kind == kindPlain || f.kind == kindSystemValued) {
			defs = append(defs, r.fieldDef(f))
			withTable = append(withTable, f)
		}
	}
	def := map[string]any{"title": t.title, "fields": defs}
	if s := rawString(t.description); s != "" {
		def["description"] = s
	}
	created, err := r.api.CreateTable(ctx, r.baseID, def)
	if err != nil && nocodb.IsBadRequest(err) && len(defs) > 0 {
		def["fields"] = []any{}
		created, err = r.api.CreateTable(ctx, r.baseID, def)
		if err != nil {
			return err
		}
		t.newID = created.ID
		r.ids[t.id] = created.ID
		r.learn(t, created)
		for _, f := range withTable {
			if err := r.createField(ctx, t, f); err != nil {
				return err
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	t.newID = created.ID
	r.ids[t.id] = created.ID
	r.learn(t, created)
	for _, f := range withTable {
		if r.ids[f.ID] == "" {
			f.skipReason = "NocoDB did not create it"
		}
	}
	return nil
}

// learn records the fields of a freshly created table: snapshot fields are
// matched by title (the ID field by type), and every new field ID is
// remembered as known.
func (r *restorer) learn(t *planTable, created *nocodb.Table) {
	for _, nf := range created.Fields {
		t.known[nf.ID] = true
		if nf.Type == "ID" && t.pkTitle == "" {
			t.pkTitle = nf.Title
		}
		for _, f := range t.fields {
			if r.ids[f.ID] != "" {
				continue
			}
			switch {
			case f.kind == kindID && nf.Type == "ID",
				(f.kind == kindPlain || f.kind == kindSystemValued) && f.Title == nf.Title:
				r.ids[f.ID] = nf.ID
			}
		}
	}
}

// createField adds one field. A rejected field is skipped with its error;
// any other failure aborts the restore.
func (r *restorer) createField(ctx context.Context, t *planTable, f *planField) error {
	nf, err := r.api.CreateField(ctx, r.baseID, t.newID, r.fieldDef(f))
	if err != nil {
		if nocodb.IsBadRequest(err) {
			f.skipReason = apiMessage(err)
			return nil
		}
		return fmt.Errorf("field %q: %w", f.Title, err)
	}
	r.ids[f.ID] = nf.ID
	t.known[nf.ID] = true
	return nil
}

// fieldDef is the definition sent to recreate f: the writable keys of the
// snapshot definition, with snapshot IDs in its options replaced by new
// ones and select-choice IDs dropped.
func (r *restorer) fieldDef(f *planField) map[string]json.RawMessage {
	def := map[string]json.RawMessage{}
	for _, k := range fieldDefKeys {
		if v, ok := f.def[k]; ok {
			def[k] = v
		}
	}
	if opts, ok := def["options"]; ok {
		def["options"] = remapOptions(opts, r.ids)
	}
	return def
}

// remapOptions replaces every string in a field's options that is a known
// snapshot ID with its new ID, and drops "id" from select choices (NocoDB
// would otherwise try to reuse them).
func remapOptions(raw json.RawMessage, ids map[string]string) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	if m, ok := v.(map[string]any); ok {
		if choices, ok := m["choices"].([]any); ok {
			for _, c := range choices {
				if cm, ok := c.(map[string]any); ok {
					delete(cm, "id")
				}
			}
		}
	}
	out, err := json.Marshal(remapValue(v, ids))
	if err != nil {
		return raw
	}
	return out
}

func remapValue(v any, ids map[string]string) any {
	switch x := v.(type) {
	case string:
		if n, ok := ids[x]; ok {
			return n
		}
	case map[string]any:
		for k, e := range x {
			x[k] = remapValue(e, ids)
		}
	case []any:
		for i, e := range x {
			x[i] = remapValue(e, ids)
		}
	}
	return v
}

// unresolvedRefs lists the snapshot IDs a field's options reference that
// have no new ID yet.
func (r *restorer) unresolvedRefs(f *planField) []string {
	var v any
	if json.Unmarshal(f.Options, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if r.isSnapshotID(x) && r.ids[x] == "" {
				out = append(out, x)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

func (r *restorer) isSnapshotID(s string) bool {
	if _, ok := r.byID[s]; ok {
		return true
	}
	for _, t := range r.tables {
		if t.field(s) != nil {
			return true
		}
	}
	return false
}

func rawString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func jsonString(raw json.RawMessage, key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return rawString(m[key])
}

// jsonBool reads a NocoDB flag, which v2 meta returns as true/false, 1/0
// or null.
func jsonBool(raw json.RawMessage, key string) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v == "1" || v == "true"
	}
	return false
}

func apiMessage(err error) string {
	var apiErr *nocodb.APIError
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		return apiErr.Message
	}
	return err.Error()
}

func (r *restorer) read(fn func(*Table) error) (*Header, error) {
	rc, err := r.open()
	if err != nil {
		return nil, fmt.Errorf("snapshot: open: %w", err)
	}
	defer rc.Close()
	return ReadTables(rc, fn)
}

// warn adds n to the warning group (code, table, field). The first message
// of a group is kept.
func (r *restorer) warn(code, table, field string, n int64, msg string) {
	key := code + "\x00" + table + "\x00" + field
	if w, ok := r.warnings[key]; ok {
		w.Count += n
		return
	}
	r.warnings[key] = &Warning{Code: code, Table: table, Field: field, Count: n, Message: msg}
	r.warningOrder = append(r.warningOrder, key)
}

// warnView counts one loss in a view. Unlike warn, the message is part of
// the group: one view can lose several different things.
func (r *restorer) warnView(code, table, view, field, msg string) {
	key := strings.Join([]string{code, table, view, field, msg}, "\x00")
	if w, ok := r.warnings[key]; ok {
		w.Count++
		return
	}
	r.warnings[key] = &Warning{Code: code, Table: table, View: view, Field: field, Count: 1, Message: msg}
	r.warningOrder = append(r.warningOrder, key)
}

func (r *restorer) finishReport() {
	for _, t := range r.tables {
		for _, f := range t.fields {
			if f.skipReason != "" {
				r.warn(WarnFieldSkipped, t.title, f.Title, 1, fmt.Sprintf("%s field not restored: %s", f.Type, f.skipReason))
			}
		}
	}
	r.report.Tables = r.report.Tables[:0]
	r.report.RecordCount, r.report.LinkCount, r.report.FileCount, r.report.ViewCount = 0, 0, 0, 0
	for _, t := range r.tables {
		if t.newID == "" {
			continue
		}
		t.stats.ID, t.stats.Title = t.newID, t.title
		t.stats.FieldCount = 0
		for _, f := range t.fields {
			if f.restored(r.ids) {
				t.stats.FieldCount++
			}
		}
		r.report.Tables = append(r.report.Tables, t.stats)
		r.report.RecordCount += t.stats.RecordCount
		r.report.LinkCount += t.stats.LinkCount
		r.report.FileCount += t.stats.FileCount
		r.report.ViewCount += t.stats.ViewCount
	}
	r.report.Warnings = r.report.Warnings[:0]
	for _, k := range r.warningOrder {
		r.report.Warnings = append(r.report.Warnings, *r.warnings[k])
	}
}
