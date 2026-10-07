package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// fakeNoco is an in-memory NocoDB that behaves like the real one where a
// restore depends on it (checked against 2026.09.1): relations get an
// auto-created inverse (except self-links), has-many is stored as om/mo,
// inserts are capped at 10 and fail as a whole, read-only and unknown
// fields are rejected, and lookups/rollups/formulas must reference fields
// that exist.
type fakeNoco struct {
	n       int
	tables  map[string]*fakeTable
	order   []string
	members []string

	tableDefs  []map[string]any
	renamed    map[string]string // new title -> NocoDB's title
	display    map[string]string
	linkCalls  []string // "table/field/record"
	failInsert func(fields map[string]json.RawMessage) bool
	failAfter  int // fail with a 500 after this many inserts (0: never)
	inserts    int
	uploads    [][]string // titles per upload request
	updates    []map[string]json.RawMessage
	failUpload func(files []nocodb.UploadFile) bool
}

type fakeTable struct {
	id, title string
	fields    []nocodb.Field
	records   []string
	links     map[string]map[string][]string // field -> record -> ids
}

func newFakeNoco() *fakeNoco {
	return &fakeNoco{tables: map[string]*fakeTable{}, renamed: map[string]string{}, display: map[string]string{}, members: []string{"admin@x.test"}}
}

func (f *fakeNoco) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s%d", prefix, f.n)
}

func badRequest(msg string) error {
	return &nocodb.APIError{StatusCode: 422, Method: "POST", Path: "/fake", Message: msg}
}

func (f *fakeNoco) CreateBase(context.Context, string, string) (string, error) { return "pNEW", nil }

func (f *fakeNoco) CreateTable(_ context.Context, _ string, def any) (*nocodb.Table, error) {
	raw, _ := json.Marshal(def)
	var d struct {
		Title  string `json:"title"`
		Fields []struct {
			Title   string          `json:"title"`
			Type    string          `json:"type"`
			Options json.RawMessage `json:"options"`
		} `json:"fields"`
	}
	_ = json.Unmarshal(raw, &d)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.tableDefs = append(f.tableDefs, m)
	t := &fakeTable{id: f.id("nt"), title: d.Title, links: map[string]map[string][]string{}}
	t.fields = append(t.fields, nocodb.Field{ID: f.id("nf"), Title: "Id", Type: "ID"})
	for _, fd := range d.Fields {
		if bytes.Contains(fd.Options, []byte(`"id"`)) {
			return nil, &nocodb.APIError{StatusCode: 500, Message: "A UNIQUE constraint was violated"}
		}
		t.fields = append(t.fields, nocodb.Field{ID: f.id("nf"), Title: fd.Title, Type: fd.Type, Options: fd.Options})
	}
	f.tables[t.id] = t
	f.order = append(f.order, t.id)
	return f.table(t), nil
}

func (f *fakeNoco) table(t *fakeTable) *nocodb.Table {
	raw, _ := json.Marshal(map[string]any{"id": t.id, "title": t.title, "fields": t.fields})
	return &nocodb.Table{ID: t.id, Title: t.title, Fields: slices.Clone(t.fields), Raw: raw}
}

func (f *fakeNoco) GetTable(_ context.Context, _, tableID string) (*nocodb.Table, error) {
	return f.table(f.tables[tableID]), nil
}

func (f *fakeNoco) UpdateTable(_ context.Context, _, tableID string, patch any) error {
	f.display[tableID] = patch.(map[string]string)["display_field_id"]
	return nil
}

func (f *fakeNoco) fieldByID(id string) (*fakeTable, *nocodb.Field) {
	for _, t := range f.tables {
		for i := range t.fields {
			if t.fields[i].ID == id {
				return t, &t.fields[i]
			}
		}
	}
	return nil, nil
}

var formulaRef = regexp.MustCompile(`\{([^}]+)\}`)

func (f *fakeNoco) CreateField(_ context.Context, _, tableID string, def any) (*nocodb.Field, error) {
	t := f.tables[tableID]
	var d nocodb.Field
	raw, _ := json.Marshal(def)
	_ = json.Unmarshal(raw, &d)
	if d.ID != "" {
		return nil, badRequest("unexpected id")
	}
	opts := map[string]string{}
	_ = json.Unmarshal(d.Options, &opts)
	switch d.Type {
	case "Links":
		rt := f.tables[opts["related_table_id"]]
		if rt == nil {
			return nil, badRequest("related table not found")
		}
		mirror := map[string]string{"hm": "om", "bt": "mo", "mm": "mm", "om": "om", "mo": "mo"}
		relType := mirror[opts["relation_type"]]
		d.Options, _ = json.Marshal(map[string]string{"relation_type": relType, "related_table_id": rt.id})
		d.ID = f.id("nf")
		t.fields = append(t.fields, d)
		if rt != t {
			inv, _ := json.Marshal(map[string]string{"relation_type": mirrorRelation(relType), "related_table_id": t.id})
			rt.fields = append(rt.fields, nocodb.Field{ID: f.id("nf"), Title: t.title + " (auto)", Type: "Links", Options: inv})
		}
		return &d, nil
	case "Lookup", "Rollup":
		for _, k := range []string{"related_field_id", "related_table_lookup_field_id", "related_table_rollup_field_id"} {
			if v, ok := opts[k]; ok {
				if _, fld := f.fieldByID(v); fld == nil {
					return nil, badRequest(k + " not found")
				}
			}
		}
	case "Formula":
		for _, m := range formulaRef.FindAllStringSubmatch(opts["formula"], -1) {
			if !slices.ContainsFunc(t.fields, func(x nocodb.Field) bool { return x.Title == m[1] }) {
				return nil, badRequest("Field '" + m[1] + "' not found")
			}
		}
	}
	d.ID = f.id("nf")
	t.fields = append(t.fields, d)
	return &d, nil
}

func (f *fakeNoco) UpdateField(_ context.Context, _, fieldID string, patch any) error {
	_, fld := f.fieldByID(fieldID)
	title := patch.(map[string]string)["title"]
	f.renamed[title] = fld.Title
	fld.Title = title
	return nil
}

var writableTypes = []string{"SingleLineText", "SingleSelect", "Decimal", "User", "Attachment"}

func (f *fakeNoco) InsertRecords(_ context.Context, _, tableID string, records []map[string]json.RawMessage) ([]json.RawMessage, error) {
	t := f.tables[tableID]
	f.inserts++
	if f.failAfter > 0 && f.inserts > f.failAfter {
		return nil, &nocodb.APIError{StatusCode: 500, Message: "boom"}
	}
	if len(records) > 10 {
		return nil, badRequest("max 10 records")
	}
	for _, r := range records {
		for title := range r {
			i := slices.IndexFunc(t.fields, func(x nocodb.Field) bool { return x.Title == title })
			if i < 0 || !slices.Contains(writableTypes, t.fields[i].Type) {
				return nil, badRequest("field " + title + " cannot be written")
			}
		}
		if f.failInsert != nil && f.failInsert(r) {
			return nil, badRequest("invalid value")
		}
	}
	ids := make([]json.RawMessage, len(records))
	for i := range records {
		t.records = append(t.records, fmt.Sprint(len(t.records)+100))
		ids[i] = json.RawMessage(t.records[len(t.records)-1])
	}
	return ids, nil
}

func (f *fakeNoco) LinkRecords(_ context.Context, _, tableID, fieldID, recordID string, ids []json.RawMessage) error {
	t := f.tables[tableID]
	f.linkCalls = append(f.linkCalls, tableID+"/"+fieldID+"/"+recordID)
	if t.links[fieldID] == nil {
		t.links[fieldID] = map[string][]string{}
	}
	for _, id := range ids {
		t.links[fieldID][recordID] = append(t.links[fieldID][recordID], nocodb.RawIDString(id))
	}
	return nil
}

func (f *fakeNoco) ListBaseUserEmails(context.Context, string) ([]string, error) {
	return f.members, nil
}

func (f *fakeNoco) UploadFiles(_ context.Context, files []nocodb.UploadFile) ([]json.RawMessage, error) {
	if f.failUpload != nil && f.failUpload(files) {
		return nil, &nocodb.APIError{StatusCode: 413, Message: "too large"}
	}
	var titles []string
	var out []json.RawMessage
	for _, file := range files {
		titles = append(titles, file.Title+"="+string(file.Content))
		raw, _ := json.Marshal(map[string]any{"path": "download/new/" + file.Title, "title": file.Title, "mimetype": file.Mimetype, "size": len(file.Content)})
		out = append(out, raw)
	}
	f.uploads = append(f.uploads, titles)
	return out, nil
}

func (f *fakeNoco) UpdateRecordsV2(_ context.Context, _ string, rows []map[string]json.RawMessage) error {
	f.updates = append(f.updates, rows...)
	return nil
}

func (f *fakeNoco) tableByTitle(title string) *fakeTable {
	for _, id := range f.order {
		if f.tables[id].title == title {
			return f.tables[id]
		}
	}
	return nil
}

func (t *fakeTable) fieldID(title string) string {
	for _, f := range t.fields {
		if f.Title == title {
			return f.ID
		}
	}
	return ""
}

// restoreFixture is a snapshot document covering what a restore handles:
// every field class, two has-many relations between the same tables that
// only their links tell apart, a self-link, a formula declared before the
// rollup it uses, unsupported fields, attachments, users, and a record
// NocoDB will reject.
func restoreFixture(t *testing.T, version int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteHeader(Header{CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Source: Source{BaseID: "p1"}, Base: json.RawMessage(`{"id":"p1","title":"CRM","description":"the crm"}`)}); err != nil {
		t.Fatal(err)
	}
	customers := &Table{ID: "m1", Title: "Customers", Schema: json.RawMessage(`{"id":"m1","title":"Customers","description":"people","display_field_id":"c10","fields":[
		{"id":"c0","table_id":"m1","title":"Id","type":"ID","system":0},
		{"id":"c1","table_id":"m1","title":"Name","type":"SingleLineText"},
		{"id":"c2","table_id":"m1","title":"Tier","type":"SingleSelect","options":{"choices":[{"id":"s1","title":"Gold","color":"#ffd700"}]}},
		{"id":"c3","table_id":"m1","title":"Docs","type":"Attachment","system":0},
		{"id":"c4","table_id":"m1","title":"Owner","type":"User","options":{"allow_multiple_users":true}},
		{"id":"c5","table_id":"m1","title":"Created","type":"CreatedTime","system":0},
		{"id":"c6","table_id":"m1","title":"Orders","type":"Links","options":{"relation_type":"om","related_table_id":"m2"}},
		{"id":"c7","table_id":"m1","title":"Reviewed","type":"Links","options":{"relation_type":"om","related_table_id":"m2"}},
		{"id":"c8","table_id":"m1","title":"Friends","type":"Links","options":{"relation_type":"mm","related_table_id":"m1"}},
		{"id":"c10","table_id":"m1","title":"Label","type":"Formula","options":{"formula":"CONCAT({Name}, {Total})"}},
		{"id":"c9","table_id":"m1","title":"Total","type":"Rollup","options":{"related_field_id":"c6","related_table_rollup_field_id":"c21","rollup_function":"sum"}},
		{"id":"c11","table_id":"m1","title":"Where","type":"GeoData"},
		{"id":"c12","table_id":"m1","title":"Notify","type":"Button","options":{"type":"webhook","label":"Go"}}
	]}`)}
	customers.Records = []nocodb.Record{
		rec(1, `{"Name":"Ada","Tier":"Gold","Docs":[{"url":"a"},{"url":"b"}],"Owner":[{"id":"u1","email":"Admin@x.test"},{"id":"u2","email":"stranger@x.test"}],"Created":"2026-01-01","Orders":2,"Reviewed":0,"Friends":2,"Label":"Ada12","Total":12,"CreatedAt":"2026-01-01"}`),
		rec(3, `{"Name":"BAD","Orders":1}`),
		rec(2, `{"Name":"Bob","Docs":[],"Owner":null,"Reviewed":1}`),
	}
	customers.Links = []Link{
		{FieldID: "c6", RecordID: json.RawMessage("1"), LinkedIDs: []json.RawMessage{json.RawMessage("10"), json.RawMessage("11")}},
		{FieldID: "c6", RecordID: json.RawMessage("3"), LinkedIDs: []json.RawMessage{json.RawMessage("12")}},
		{FieldID: "c7", RecordID: json.RawMessage("2"), LinkedIDs: []json.RawMessage{json.RawMessage("12")}},
		{FieldID: "c8", RecordID: json.RawMessage("1"), LinkedIDs: []json.RawMessage{json.RawMessage("2"), json.RawMessage("3"), json.RawMessage("2")}},
	}
	orders := &Table{ID: "m2", Title: "Orders", Schema: json.RawMessage(`{"id":"m2","title":"Orders","display_field_id":"c21","fields":[
		{"id":"c20","table_id":"m2","title":"Id","type":"ID"},
		{"id":"c21","table_id":"m2","title":"Amount","type":"Decimal"},
		{"id":"c23","table_id":"m2","title":"Reviewer","type":"Links","options":{"relation_type":"mo","related_table_id":"m1"}},
		{"id":"c22","table_id":"m2","title":"Customer","type":"Links","options":{"relation_type":"mo","related_table_id":"m1"}},
		{"id":"c24","table_id":"m2","title":"Customer Name","type":"Lookup","options":{"related_field_id":"c22","related_table_lookup_field_id":"c1"}},
		{"id":"c25","table_id":"m2","title":"Who","type":"Formula","options":{"formula":"{Customer Name}"}},
		{"id":"c26","table_id":"m2","title":"Where","type":"Lookup","options":{"related_field_id":"c22","related_table_lookup_field_id":"c11"}}
	]}`)}
	orders.Records = []nocodb.Record{rec(10, `{"Amount":5}`), rec(11, `{"Amount":7}`), rec(12, `{"Amount":1,"Customer Name":"Bob"}`)}
	orders.Links = []Link{
		{FieldID: "c22", RecordID: json.RawMessage("10"), LinkedIDs: []json.RawMessage{json.RawMessage("1")}},
		{FieldID: "c22", RecordID: json.RawMessage("11"), LinkedIDs: []json.RawMessage{json.RawMessage("1")}},
		{FieldID: "c22", RecordID: json.RawMessage("12"), LinkedIDs: []json.RawMessage{json.RawMessage("3")}},
		{FieldID: "c23", RecordID: json.RawMessage("12"), LinkedIDs: []json.RawMessage{json.RawMessage("2")}},
	}
	for _, tb := range []*Table{customers, orders} {
		if err := w.WriteTable(tb); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if version == FormatVersion {
		return buf.Bytes()
	}
	// Rewrite the version for the older and unsupported version tests.
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := io.ReadAll(zr)
	doc = bytes.Replace(doc, []byte(fmt.Sprintf(`"version":%d`, FormatVersion)), []byte(fmt.Sprintf(`"version":%d`, version)), 1)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	_, _ = zw.Write(doc)
	_ = zw.Close()
	return out.Bytes()
}

func opener(doc []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(doc)), nil }
}

func TestRestore(t *testing.T) {
	api := newFakeNoco()
	api.failInsert = func(fields map[string]json.RawMessage) bool { return string(fields["Name"]) == `"BAD"` }
	var created string
	report, err := Restore(context.Background(), api, opener(restoreFixture(t, FormatVersion)), RestoreOptions{
		Title:         "CRM (restored)",
		BatchSize:     2,
		OnBaseCreated: func(id string) { created = id },
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != "pNEW" || report.BaseID != "pNEW" || report.BaseTitle != "CRM (restored)" {
		t.Fatalf("base = %q / %+v", created, report)
	}

	cust, ord := api.tableByTitle("Customers"), api.tableByTitle("Orders")

	// Tables are created with their plain and system-valued fields only,
	// from the writable keys, without choice IDs.
	def, _ := json.Marshal(api.tableDefs[0])
	if string(def) != `{"description":"people","fields":[{"title":"Name","type":"SingleLineText"},{"options":{"choices":[{"color":"#ffd700","title":"Gold"}]},"title":"Tier","type":"SingleSelect"},{"title":"Docs","type":"Attachment"},{"options":{"allow_multiple_users":true},"title":"Owner","type":"User"},{"title":"Created","type":"CreatedTime"}],"title":"Customers"}` {
		t.Fatalf("customers def = %s", def)
	}

	// Each relation is created once; the inverses NocoDB added are
	// renamed, and the links decide which inverse is which.
	if api.renamed["Customer"] != "Customers (auto)" || api.renamed["Reviewer"] != "Customers (auto)" || len(api.renamed) != 2 {
		t.Fatalf("renamed = %v", api.renamed)
	}
	if got := fieldTitles(cust); !slices.Equal(got, []string{"Id", "Name", "Tier", "Docs", "Owner", "Created", "Orders", "Reviewed", "Friends", "Total", "Label"}) {
		t.Fatalf("customers fields = %v", got)
	}
	if got := fieldTitles(ord); !slices.Equal(got, []string{"Id", "Amount", "Customer", "Reviewer", "Customer Name", "Who"}) {
		t.Fatalf("orders fields = %v", got)
	}
	// Orders/Customer is the inverse of Customers/Orders: Ada's orders.
	ada, bob := cust.records[0], cust.records[1]
	if got := cust.links[cust.fieldID("Orders")][ada]; !slices.Equal(got, []string{ord.records[0], ord.records[1]}) {
		t.Fatalf("Ada's orders = %v (orders %v)", got, ord.records)
	}
	if got := cust.links[cust.fieldID("Reviewed")][bob]; !slices.Equal(got, []string{ord.records[2]}) {
		t.Fatalf("Bob reviewed = %v", got)
	}
	// Links are written from the created side only, duplicates dropped.
	if got := cust.links[cust.fieldID("Friends")][ada]; !slices.Equal(got, []string{bob}) {
		t.Fatalf("Ada's friends = %v", got)
	}
	for _, call := range api.linkCalls {
		if strings.HasPrefix(call, ord.id+"/") {
			t.Fatalf("link written from the inverse side: %s", call)
		}
	}

	// Lookups and rollups point at the new fields; display fields follow.
	_, total := api.fieldByID(cust.fieldID("Total"))
	if want := fmt.Sprintf(`{"related_field_id":%q,"related_table_rollup_field_id":%q,"rollup_function":"sum"}`, cust.fieldID("Orders"), ord.fieldID("Amount")); string(total.Options) != want {
		t.Fatalf("rollup options = %s, want %s", total.Options, want)
	}
	if api.display[cust.id] != cust.fieldID("Label") || api.display[ord.id] != ord.fieldID("Amount") {
		t.Fatalf("display = %v", api.display)
	}

	if len(report.Tables) != 2 || report.Tables[0].RecordCount != 2 || report.Tables[1].RecordCount != 3 {
		t.Fatalf("tables = %+v", report.Tables)
	}
	if report.RecordCount != 5 || report.LinkCount != 4 {
		t.Fatalf("counts = %d records, %d links", report.RecordCount, report.LinkCount)
	}
	if report.Tables[0].FieldCount != 10 {
		t.Fatalf("customers field count = %d", report.Tables[0].FieldCount)
	}

	got := map[string]Warning{}
	for _, w := range report.Warnings {
		got[w.Code+"/"+w.Table+"/"+w.Field] = w
	}
	want := map[string]int64{
		WarnRecordsFailed + "/Customers/":          1,
		WarnAttachmentsSkipped + "/Customers/Docs": 2,
		WarnUserValuesDropped + "/Customers/Owner": 1,
		WarnLinksFailed + "/Customers/Orders":      1,
		WarnLinksFailed + "/Customers/Friends":     1,
		WarnFieldSkipped + "/Customers/Where":      1,
		WarnFieldSkipped + "/Customers/Notify":     1,
		WarnFieldSkipped + "/Orders/Where":         1,
	}
	if len(got) != len(want) {
		t.Errorf("warnings = %+v", report.Warnings)
	}
	for k, n := range want {
		if got[k].Count != n {
			t.Errorf("warning %s = %+v, want count %d", k, got[k], n)
		}
	}
	if msg := got[WarnRecordsFailed+"/Customers/"].Message; msg != "invalid value (e.g. record 3)" {
		t.Errorf("records_failed message = %q", msg)
	}
	if msg := got[WarnFieldSkipped+"/Orders/Where"].Message; !strings.Contains(msg, "depends on a field that was not restored") {
		t.Errorf("Orders/Where message = %q", msg)
	}
}

func TestRestoreOwnerKeptForMembers(t *testing.T) {
	api := newFakeNoco()
	var sent []string
	api.failInsert = func(fields map[string]json.RawMessage) bool {
		if v, ok := fields["Owner"]; ok {
			sent = append(sent, string(v))
		}
		return false
	}
	if _, err := Restore(context.Background(), api, opener(restoreFixture(t, FormatVersion)), RestoreOptions{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sent, []string{`[{"email":"Admin@x.test"}]`}) {
		t.Fatalf("owner values sent = %v", sent)
	}
}

func TestRestoreFailureKeepsReport(t *testing.T) {
	api := newFakeNoco()
	api.failAfter = 1
	report, err := Restore(context.Background(), api, opener(restoreFixture(t, FormatVersion)), RestoreOptions{Title: "x"})
	var apiErr *nocodb.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v", err)
	}
	if report == nil || report.BaseID != "pNEW" || len(report.Tables) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRestoreReadsVersion1(t *testing.T) {
	api := newFakeNoco()
	report, err := Restore(context.Background(), api, opener(restoreFixture(t, 1)), RestoreOptions{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordCount != 6 || len(api.uploads) != 0 {
		t.Fatalf("records = %d, uploads = %v", report.RecordCount, api.uploads)
	}
	for _, w := range report.Warnings {
		if w.Code == WarnAttachmentsSkipped && (w.Count != 2 || w.Message != "the snapshot holds no attachment files") {
			t.Fatalf("attachments warning = %+v", w)
		}
	}
}

func TestRestoreRejectsUnknownVersion(t *testing.T) {
	api := newFakeNoco()
	_, err := Restore(context.Background(), api, opener(restoreFixture(t, FormatVersion+1)), RestoreOptions{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("version %d", FormatVersion+1)) {
		t.Fatalf("err = %v", err)
	}
	if len(api.tables) != 0 {
		t.Fatal("restore created tables for an unsupported snapshot")
	}
}

func TestRemapOptions(t *testing.T) {
	got := remapOptions(json.RawMessage(`{"related_field_id":"c1","nested":["c2","x"],"choices":[{"id":"s1","title":"A"}]}`), map[string]string{"c1": "n1", "c2": "n2"})
	if string(got) != `{"choices":[{"title":"A"}],"nested":["n2","x"],"related_field_id":"n1"}` {
		t.Fatalf("remap = %s", got)
	}
}

func fieldTitles(t *fakeTable) []string {
	var out []string
	for _, f := range t.fields {
		out = append(out, f.Title)
	}
	return out
}
