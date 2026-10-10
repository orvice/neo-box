package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// TestLiveRestoreRoundTrip builds a fixture Base on a real NocoDB, snapshots
// it, restores the snapshot into a new Base, snapshots that, and compares
// the two snapshots. It is skipped unless NEOBOX_TEST_NOCODB_URL,
// NEOBOX_TEST_NOCODB_TOKEN and NEOBOX_TEST_NOCODB_EMAIL (the token owner's
// email, used for a User field) are set; an OSS nocodb/nocodb container is
// enough. Both Bases are deleted afterwards unless NEOBOX_TEST_NOCODB_KEEP
// is set.
func TestLiveRestoreRoundTrip(t *testing.T) {
	baseURL, token := os.Getenv("NEOBOX_TEST_NOCODB_URL"), os.Getenv("NEOBOX_TEST_NOCODB_TOKEN")
	if baseURL == "" || token == "" || os.Getenv("NEOBOX_TEST_NOCODB_EMAIL") == "" {
		t.Skip("NEOBOX_TEST_NOCODB_URL / NEOBOX_TEST_NOCODB_TOKEN / NEOBOX_TEST_NOCODB_EMAIL are not set")
	}
	c, err := nocodb.New(baseURL, token, nocodb.WithRateLimit(0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cleanup := func(baseID string) {
		if os.Getenv("NEOBOX_TEST_NOCODB_KEEP") != "" {
			t.Logf("keeping base %s", baseID)
			return
		}
		if err := c.DeleteBase(context.Background(), baseID); err != nil {
			t.Logf("delete base %s: %v", baseID, err)
		}
	}

	suffix := time.Now().Format("150405")
	srcID := buildFixture(ctx, t, c, "neobox-restore-src-"+suffix)
	t.Cleanup(func() { cleanup(srcID) })

	files := &memFiles{c: c, content: map[string][]byte{}}
	var src bytes.Buffer
	stats, err := Build(ctx, c, Source{BaseURL: baseURL, BaseID: srcID}, &src, Options{Files: files})
	if err != nil {
		t.Fatalf("build source snapshot: %v", err)
	}
	if stats.FileCount != 2 || stats.FilesMissing != 0 {
		t.Fatalf("source files: %d stored, %d missing", stats.FileCount, stats.FilesMissing)
	}
	report, err := Restore(ctx, c, func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(src.Bytes())), nil
	}, RestoreOptions{
		Title:    "neobox-restore-dst-" + suffix,
		Progress: func(msg string) { t.Log(msg) },
		OpenFile: files.open,
	})
	if report != nil && report.BaseID != "" {
		t.Cleanup(func() { cleanup(report.BaseID) })
	}
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, w := range report.Warnings {
		t.Logf("warning: %+v", w)
	}

	var dst bytes.Buffer
	if _, err := Build(ctx, c, Source{BaseURL: baseURL, BaseID: report.BaseID}, &dst, Options{Files: files}); err != nil {
		t.Fatalf("build restored snapshot: %v", err)
	}
	compareSnapshots(t, src.Bytes(), dst.Bytes())

	if report.FileCount != 2 {
		t.Errorf("restored %d files, want 2", report.FileCount)
	}
	for _, w := range report.Warnings {
		t.Errorf("unexpected warning %+v", w)
	}
}

// memFiles keeps attachment files in memory by sha256, downloading them
// the way the backup manager does.
type memFiles struct {
	c       *nocodb.Client
	mu      sync.Mutex
	content map[string][]byte
}

func (m *memFiles) Store(ctx context.Context, a nocodb.Attachment) (string, error) {
	var lastErr error
	for _, ref := range a.DownloadRefs() {
		rc, err := m.c.Download(ctx, ref)
		if err != nil {
			lastErr = err
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(b)
		sha := hex.EncodeToString(sum[:])
		m.mu.Lock()
		m.content[sha] = b
		m.mu.Unlock()
		return sha, nil
	}
	return "", fmt.Errorf("%w: %v", ErrFileUnavailable, lastErr)
}

func (m *memFiles) open(_ context.Context, sha string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.content[sha]
	if !ok {
		return nil, ErrFileUnavailable
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// buildFixture creates a Base covering every field class a restore treats
// differently: plain fields, system-valued fields, has-many, many-to-many
// and self links, a lookup, a rollup, formulas over both, attachments.
func buildFixture(ctx context.Context, t *testing.T, c *nocodb.Client, title string) string {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	baseID, err := c.CreateBase(ctx, title, "restore fixture")
	must(err)

	customers, err := c.CreateTable(ctx, baseID, map[string]any{
		"title": "Customers",
		"fields": []map[string]any{
			{"title": "Name", "type": "SingleLineText"},
			{"title": "Email", "type": "Email"},
			{"title": "Notes", "type": "LongText"},
			{"title": "Score", "type": "Number"},
			{"title": "Balance", "type": "Currency"},
			{"title": "Active", "type": "Checkbox"},
			{"title": "Joined", "type": "Date"},
			{"title": "Tier", "type": "SingleSelect", "options": map[string]any{
				"choices": []map[string]any{{"title": "Gold", "color": "#ffd700"}, {"title": "Silver"}},
			}},
			{"title": "Tags", "type": "MultiSelect", "options": map[string]any{
				"choices": []map[string]any{{"title": "vip"}, {"title": "new"}, {"title": "churn"}},
			}},
			{"title": "Meta", "type": "JSON"},
			{"title": "Stars", "type": "Rating"},
			{"title": "Docs", "type": "Attachment"},
			{"title": "Created", "type": "CreatedTime"},
			{"title": "Owner", "type": "User"},
		},
	})
	must(err)
	orders, err := c.CreateTable(ctx, baseID, map[string]any{
		"title": "Orders",
		"fields": []map[string]any{
			{"title": "Title", "type": "SingleLineText"},
			{"title": "Amount", "type": "Decimal"},
		},
	})
	must(err)
	products, err := c.CreateTable(ctx, baseID, map[string]any{
		"title":  "Products",
		"fields": []map[string]any{{"title": "Name", "type": "SingleLineText"}},
	})
	must(err)

	link := func(table *nocodb.Table, title, relType string, related *nocodb.Table, inverseTitle string) *nocodb.Field {
		t.Helper()
		before, err := c.GetTable(ctx, baseID, related.ID)
		must(err)
		known := map[string]bool{}
		for _, f := range before.Fields {
			known[f.ID] = true
		}
		f, err := c.CreateField(ctx, baseID, table.ID, map[string]any{
			"title": title, "type": "Links",
			"options": map[string]any{"relation_type": relType, "related_table_id": related.ID},
		})
		must(err)
		after, err := c.GetTable(ctx, baseID, related.ID)
		must(err)
		for _, rf := range after.Fields {
			if rf.IsLink() && !known[rf.ID] && rf.ID != f.ID {
				must(c.UpdateField(ctx, baseID, rf.ID, map[string]string{"title": inverseTitle}))
			}
		}
		return f
	}
	custOrders := link(customers, "Orders", "hm", orders, "Customer")
	// A second has-many between the same tables: only the links tell the
	// two inverse fields apart.
	reviewed := link(customers, "Reviewed", "hm", orders, "Reviewer")
	ordProducts := link(orders, "Products", "mm", products, "Orders")
	referrals := link(customers, "Referrals", "mm", customers, "Referred By")

	owner := os.Getenv("NEOBOX_TEST_NOCODB_EMAIL")
	custIDs, err := c.InsertRecords(ctx, baseID, customers.ID, []map[string]json.RawMessage{
		raw(`{"Owner":[{"email":"` + owner + `"}],"Name":"Ada","Email":"ada@example.com","Notes":"line1\nline2","Score":42,"Balance":10.5,"Active":true,"Joined":"2026-01-02","Tier":"Gold","Tags":["vip","new"],"Meta":{"k":[1,2]},"Stars":4}`),
		raw(`{"Name":"Bob","Score":7,"Active":false,"Tier":"Silver","Tags":["churn"]}`),
		raw(`{"Name":"Cy"}`),
	})
	must(err)
	ordIDs, err := c.InsertRecords(ctx, baseID, orders.ID, []map[string]json.RawMessage{
		raw(`{"Title":"o1","Amount":5.25}`), raw(`{"Title":"o2","Amount":7}`), raw(`{"Title":"o3","Amount":1}`), raw(`{"Title":"o4"}`),
	})
	must(err)
	prodIDs, err := c.InsertRecords(ctx, baseID, products.ID, []map[string]json.RawMessage{raw(`{"Name":"p1"}`), raw(`{"Name":"p2"}`)})
	must(err)

	// Attachments go in through v2: storage upload, then a record update.
	docs, err := c.UploadFiles(ctx, []nocodb.UploadFile{
		{Title: "notes \"v1\".txt", Mimetype: "text/plain", Content: []byte("hello attachment\n")},
		{Title: "blob.bin", Mimetype: "application/octet-stream", Content: bytes.Repeat([]byte{0, 1, 2, 254, 255}, 400)},
	})
	must(err)
	docsValue, _ := json.Marshal(docs)
	must(c.UpdateRecordsV2(ctx, customers.ID, []map[string]json.RawMessage{{"Id": custIDs[0], "Docs": docsValue}}))

	id := func(ids []json.RawMessage, i int) string { return nocodb.RawIDString(ids[i]) }
	must(c.LinkRecords(ctx, baseID, customers.ID, custOrders.ID, id(custIDs, 0), ordIDs[0:2]))
	must(c.LinkRecords(ctx, baseID, customers.ID, custOrders.ID, id(custIDs, 1), ordIDs[2:3]))
	must(c.LinkRecords(ctx, baseID, orders.ID, ordProducts.ID, id(ordIDs, 0), prodIDs))
	must(c.LinkRecords(ctx, baseID, orders.ID, ordProducts.ID, id(ordIDs, 2), prodIDs[1:]))
	must(c.LinkRecords(ctx, baseID, customers.ID, referrals.ID, id(custIDs, 0), custIDs[1:3]))
	must(c.LinkRecords(ctx, baseID, customers.ID, reviewed.ID, id(custIDs, 2), ordIDs[3:4]))
	must(c.LinkRecords(ctx, baseID, customers.ID, reviewed.ID, id(custIDs, 1), ordIDs[0:1]))

	// Computed fields last: NocoDB 2026.09.1 fails many-to-many link
	// writes on a table that has a lookup through another relation.
	orders, err = c.GetTable(ctx, baseID, orders.ID)
	must(err)
	customerLink := fieldByTitle(t, orders, "Customer")
	nameField := fieldByTitle(t, customers, "Name")
	amountField := fieldByTitle(t, orders, "Amount")

	_, err = c.CreateField(ctx, baseID, orders.ID, map[string]any{
		"title": "Customer Name", "type": "Lookup",
		"options": map[string]any{"related_field_id": customerLink.ID, "related_table_lookup_field_id": nameField.ID},
	})
	must(err)
	_, err = c.CreateField(ctx, baseID, customers.ID, map[string]any{
		"title": "Total", "type": "Rollup",
		"options": map[string]any{"related_field_id": custOrders.ID, "related_table_rollup_field_id": amountField.ID, "rollup_function": "sum"},
	})
	must(err)
	_, err = c.CreateField(ctx, baseID, customers.ID, map[string]any{
		"title": "Label", "type": "Formula",
		"options": map[string]any{"formula": `CONCAT({Name}, " / ", {Total})`},
	})
	must(err)
	_, err = c.CreateField(ctx, baseID, orders.ID, map[string]any{
		"title": "Who", "type": "Formula",
		"options": map[string]any{"formula": `{Customer Name}`},
	})
	must(err)

	return baseID
}

func raw(s string) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		panic(err)
	}
	return m
}

func fieldByTitle(t *testing.T, table *nocodb.Table, title string) nocodb.Field {
	t.Helper()
	for _, f := range table.Fields {
		if f.Title == title {
			return f
		}
	}
	t.Fatalf("table %q has no field %q", table.Title, title)
	return nocodb.Field{}
}

// compareSnapshots checks that dst holds src's tables, field titles and
// types, record values and links. Records are matched by position (a
// restore inserts them in snapshot order); values NocoDB regenerates are
// not compared.
func compareSnapshots(t *testing.T, src, dst []byte) {
	t.Helper()
	load := func(doc []byte) []*Table {
		var out []*Table
		if _, err := ReadTables(bytes.NewReader(doc), func(tb *Table) error {
			out = append(out, tb)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	srcTables, dstTables := load(src), load(dst)
	if len(srcTables) != len(dstTables) {
		t.Fatalf("tables: %d restored, want %d", len(dstTables), len(srcTables))
	}
	// position of each record ID, per table title
	pos := func(tables []*Table) map[string]map[string]int {
		out := map[string]map[string]int{}
		for _, tb := range tables {
			out[tb.Title] = map[string]int{}
			for i, r := range tb.Records {
				out[tb.Title][r.IDString()] = i
			}
		}
		return out
	}
	srcPos, dstPos := pos(srcTables), pos(dstTables)
	titleByID := func(tables []*Table) map[string]string {
		out := map[string]string{}
		for _, tb := range tables {
			out[tb.ID] = tb.Title
		}
		return out
	}
	srcTitles, dstTitles := titleByID(srcTables), titleByID(dstTables)

	for i, st := range srcTables {
		dt := dstTables[i]
		if st.Title != dt.Title {
			t.Errorf("table %d: title %q, want %q", i, dt.Title, st.Title)
			continue
		}
		sf, df := st.Fields(), dt.Fields()
		fieldKey := func(fs []nocodb.Field) []string {
			var out []string
			for _, f := range fs {
				out = append(out, f.Title+":"+f.Type)
			}
			slices.Sort(out)
			return out
		}
		if a, b := fieldKey(sf), fieldKey(df); !slices.Equal(a, b) {
			t.Errorf("%s fields:\n got %v\nwant %v", st.Title, b, a)
		}
		if len(st.Records) != len(dt.Records) {
			t.Errorf("%s: %d records, want %d", st.Title, len(dt.Records), len(st.Records))
			continue
		}
		for j, sr := range st.Records {
			dr := dt.Records[j]
			for _, f := range sf {
				switch classify(f.Type) {
				case kindPlain, kindVirtual:
				default:
					continue
				}
				if f.Type == "Attachment" {
					a, b := attachments(st, sr.Fields[f.Title]), attachments(dt, dr.Fields[f.Title])
					if !slices.Equal(a, b) {
						t.Errorf("%s record %d %q = %v, want %v", st.Title, j, f.Title, b, a)
					}
					continue
				}
				a, b := normalize(sr.Fields[f.Title]), normalize(dr.Fields[f.Title])
				if a != b {
					t.Errorf("%s record %d %q = %s, want %s", st.Title, j, f.Title, b, a)
				}
			}
		}
		// Links as (field title, from position, to position) triples.
		links := func(tb *Table, p map[string]map[string]int, titles map[string]string) []string {
			var out []string
			for _, l := range tb.Links {
				var field nocodb.Field
				for _, f := range tb.Fields() {
					if f.ID == l.FieldID {
						field = f
					}
				}
				related := titles[jsonString(field.Options, "related_table_id")]
				for _, to := range l.LinkedIDs {
					out = append(out, fmt.Sprintf("%s:%d->%d", field.Title,
						p[tb.Title][nocodb.RawIDString(l.RecordID)], p[related][nocodb.RawIDString(to)]))
				}
			}
			slices.Sort(out)
			return out
		}
		if a, b := links(st, srcPos, srcTitles), links(dt, dstPos, dstTitles); !slices.Equal(a, b) {
			t.Errorf("%s links:\n got %v\nwant %v", st.Title, b, a)
		}
	}
}

// attachments describes an Attachment value by what a restore keeps: each
// file's title, type, size and content (as stored in the table's files).
func attachments(tb *Table, v json.RawMessage) []string {
	shas := map[string]string{}
	for _, f := range tb.Files {
		shas[FileKey(f.Source, f.Size)] = f.SHA256
	}
	var out []string
	for _, a := range nocodb.ParseAttachments(v) {
		out = append(out, fmt.Sprintf("%s|%s|%d|%s", a.Title, a.Mimetype, a.Size, shas[FileKey(a.Source(), a.Size)]))
	}
	return out
}

func normalize(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return "null"
	}
	var x any
	if json.Unmarshal(v, &x) != nil {
		return string(v)
	}
	out, _ := json.Marshal(x)
	return string(out)
}
