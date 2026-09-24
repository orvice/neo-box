package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

type fakeAPI struct {
	tables  []nocodb.TableSummary
	schemas map[string]*nocodb.Table
	records map[string][]nocodb.Record
	links   map[string][]json.RawMessage // table/field/record -> ids
	calls   map[string]int
}

func (f *fakeAPI) GetBaseRaw(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"id":"p1","title":"CRM"}`), nil
}

func (f *fakeAPI) ListTables(context.Context, string) ([]nocodb.TableSummary, error) {
	return f.tables, nil
}

func (f *fakeAPI) GetTable(_ context.Context, _, tableID string) (*nocodb.Table, error) {
	return f.schemas[tableID], nil
}

func (f *fakeAPI) ListRecords(_ context.Context, _, tableID string, page, pageSize int) (*nocodb.RecordPage, error) {
	all := f.records[tableID]
	start := min((page-1)*pageSize, len(all))
	end := min(start+pageSize, len(all))
	return &nocodb.RecordPage{Records: all[start:end], HasNext: end < len(all)}, nil
}

func (f *fakeAPI) ListLinkedIDs(_ context.Context, _, tableID, fieldID, recordID string) ([]json.RawMessage, error) {
	key := tableID + "/" + fieldID + "/" + recordID
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[key]++
	return f.links[key], nil
}

func rec(id int, fields string) nocodb.Record {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fields), &m); err != nil {
		panic(err)
	}
	return nocodb.Record{ID: json.RawMessage(fmt.Sprint(id)), Fields: m}
}

func schema(id, title, fields string) *nocodb.Table {
	raw := json.RawMessage(fmt.Sprintf(`{"id":%q,"title":%q,"fields":%s}`, id, title, fields))
	var parsed struct {
		Fields []nocodb.Field `json:"fields"`
	}
	_ = json.Unmarshal([]byte(fields), &parsed.Fields)
	return &nocodb.Table{ID: id, Title: title, Fields: parsed.Fields, Raw: raw}
}

func TestBuildAndReadRoundTrip(t *testing.T) {
	api := &fakeAPI{
		tables: []nocodb.TableSummary{{ID: "m1", Title: "Customers"}, {ID: "m2", Title: "Orders"}},
		schemas: map[string]*nocodb.Table{
			"m1": schema("m1", "Customers", `[{"id":"c1","title":"Name","type":"SingleLineText"},{"id":"c2","title":"Orders","type":"Links"}]`),
			"m2": schema("m2", "Orders", `[{"id":"c3","title":"Amount","type":"Number"}]`),
		},
		records: map[string][]nocodb.Record{
			"m1": {rec(1, `{"Name":"Ada","Orders":2}`), rec(2, `{"Name":"Bob","Orders":0}`), rec(3, `{"Name":"Cy","Orders":null}`)},
			"m2": {rec(10, `{"Amount":5}`), rec(11, `{"Amount":7}`)},
		},
		links: map[string][]json.RawMessage{
			"m1/c2/1": {json.RawMessage("10"), json.RawMessage("11")},
		},
	}

	var buf bytes.Buffer
	stats, err := Build(context.Background(), api, Source{BaseURL: "https://noco", BaseID: "p1"}, &buf, Options{
		PageSize: 2,
		Now:      func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.RecordCount != 5 || stats.LinkCount != 2 || len(stats.Tables) != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.Tables[0].FieldCount != 2 {
		t.Fatalf("field count = %d", stats.Tables[0].FieldCount)
	}
	// Only the record whose link count is non-zero is looked up.
	if len(api.calls) != 1 || api.calls["m1/c2/1"] != 1 {
		t.Fatalf("link lookups = %v", api.calls)
	}

	header, table, err := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if header.Format != FormatName || header.Version != FormatVersion || header.Source.BaseID != "p1" {
		t.Fatalf("header = %+v", header)
	}
	if string(header.Base) != `{"id":"p1","title":"CRM"}` {
		t.Fatalf("base = %s", header.Base)
	}
	if table == nil || len(table.Records) != 3 || len(table.Links) != 1 || table.LinkCount() != 2 {
		t.Fatalf("table = %+v", table)
	}
	if got := len(table.Fields()); got != 2 {
		t.Fatalf("fields = %d", got)
	}

	_, orders, err := ReadTable(bytes.NewReader(buf.Bytes()), "m2")
	if err != nil || orders == nil || len(orders.Records) != 2 || len(orders.Links) != 0 {
		t.Fatalf("orders = %+v, %v", orders, err)
	}
	_, missing, err := ReadTable(bytes.NewReader(buf.Bytes()), "nope")
	if err != nil || missing != nil {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
}

func TestEmptyBase(t *testing.T) {
	var buf bytes.Buffer
	stats, err := Build(context.Background(), &fakeAPI{}, Source{BaseID: "p1"}, &buf, Options{})
	if err != nil || stats.RecordCount != 0 || len(stats.Tables) != 0 {
		t.Fatalf("stats = %+v, err = %v", stats, err)
	}
	header, table, err := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if err != nil || header == nil || table != nil {
		t.Fatalf("read = %+v %+v %v", header, table, err)
	}
}

func TestHasLinks(t *testing.T) {
	cases := map[string]bool{
		``: false, `null`: false, `0`: false, `3`: true, `"2"`: true, `"0"`: false, `""`: false,
		`[]`: false, `[{"id":1}]`: true, `{"id":1}`: true,
	}
	for in, want := range cases {
		if got := hasLinks(json.RawMessage(in)); got != want {
			t.Errorf("hasLinks(%s) = %v, want %v", in, got, want)
		}
	}
}
