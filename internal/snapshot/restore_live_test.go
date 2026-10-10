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
	"strings"
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
	if stats.ViewCount == 0 || report.ViewCount != stats.ViewCount {
		t.Errorf("restored %d views, want %d", report.ViewCount, stats.ViewCount)
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

	buildViews(ctx, t, c, baseID, customers.ID)
	return baseID
}

// buildViews gives Customers a view of each type with settings, column
// settings, sorts and a nested filter group.
func buildViews(ctx context.Context, t *testing.T, c *nocodb.Client, baseID, tableID string) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	table, err := c.GetTable(ctx, baseID, tableID)
	must(err)
	field := func(title string) string { return fieldByTitle(t, table, title).ID }
	column := func(viewID, title string) string {
		cols, err := c.ListViewColumns(ctx, viewID)
		must(err)
		for _, col := range cols {
			if jsonString(col, "fk_column_id") == field(title) {
				return jsonString(col, "id")
			}
		}
		t.Fatalf("view %s has no column %q", viewID, title)
		return ""
	}

	// The default view: a sort and a hidden column.
	views, err := c.ListViews(ctx, tableID)
	must(err)
	def := jsonString(views[0], "id")
	must(c.CreateSort(ctx, def, map[string]any{"fk_column_id": field("Name"), "direction": "asc"}))
	must(c.UpdateViewColumn(ctx, def, column(def, "Meta"), map[string]any{"show": false}))

	vip, err := c.CreateView(ctx, tableID, nocodb.ViewTypeGrid, map[string]any{"title": "VIP"})
	must(err)
	must(c.UpdateView(ctx, vip, map[string]any{"lock_type": "locked", "description": "gold or high score"}))
	must(c.UpdateViewSettings(ctx, nocodb.ViewTypeGrid, vip, map[string]any{"row_height": 1}))
	must(c.UpdateViewColumn(ctx, vip, column(vip, "Email"), map[string]any{"show": false}))
	must(c.UpdateViewColumn(ctx, vip, column(vip, "Score"), map[string]any{"order": 0.5}))
	must(c.UpdateGridColumn(ctx, column(vip, "Name"), map[string]any{"width": "250px"}))
	must(c.CreateSort(ctx, vip, map[string]any{"fk_column_id": field("Score"), "direction": "desc"}))
	group, err := c.CreateFilter(ctx, vip, map[string]any{"is_group": true, "logical_op": "and"})
	must(err)
	_, err = c.CreateFilter(ctx, vip, map[string]any{"fk_parent_id": group, "fk_column_id": field("Tier"), "comparison_op": "eq", "value": "Gold", "logical_op": "or"})
	must(err)
	_, err = c.CreateFilter(ctx, vip, map[string]any{"fk_parent_id": group, "fk_column_id": field("Score"), "comparison_op": "gt", "value": "10", "logical_op": "or"})
	must(err)
	_, err = c.CreateFilter(ctx, vip, map[string]any{"fk_column_id": field("Joined"), "comparison_op": "isWithin", "comparison_sub_op": "pastNumberOfDays", "value": "30", "logical_op": "and"})
	must(err)

	form, err := c.CreateView(ctx, tableID, nocodb.ViewTypeForm, map[string]any{"title": "Signup"})
	must(err)
	must(c.UpdateViewSettings(ctx, nocodb.ViewTypeForm, form, map[string]any{"heading": "Join us", "subheading": "it's quick", "success_msg": "Welcome!", "submit_another_form": true}))
	must(c.UpdateFormColumn(ctx, column(form, "Name"), map[string]any{"label": "Your name", "help": "as on your ID", "required": true}))

	gallery, err := c.CreateView(ctx, tableID, nocodb.ViewTypeGallery, map[string]any{"title": "Cards"})
	must(err)
	must(c.UpdateViewSettings(ctx, nocodb.ViewTypeGallery, gallery, map[string]any{"fk_cover_image_col_id": field("Docs")}))

	kanban, err := c.CreateView(ctx, tableID, nocodb.ViewTypeKanban, map[string]any{"title": "By tier", "fk_grp_col_id": field("Tier")})
	must(err)
	// Reverse the stacks and collapse one.
	views, err = c.ListViews(ctx, tableID)
	must(err)
	for _, v := range views {
		if jsonString(v, "id") != kanban {
			continue
		}
		var settings struct {
			Meta map[string][]map[string]any `json:"meta"`
		}
		_ = json.Unmarshal(jsonRaw(v, "view"), &settings)
		stacks := settings.Meta[field("Tier")]
		for i, st := range stacks {
			st["order"] = len(stacks) - i
			st["collapsed"] = st["title"] == "Silver"
		}
		must(c.UpdateViewSettings(ctx, nocodb.ViewTypeKanban, kanban, map[string]any{"meta": map[string]any{field("Tier"): stacks}}))
	}

	_, err = c.CreateView(ctx, tableID, nocodb.ViewTypeCalendar, map[string]any{"title": "Joined", "calendar_range": []map[string]string{{"fk_from_column_id": field("Joined")}}})
	must(err)
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
		t.Logf("%s views:\n     %s", st.Title, strings.Join(describeViews(dt), "\n     "))
		if a, b := describeViews(st), describeViews(dt); !slices.Equal(a, b) {
			t.Errorf("%s views:\n got %s\nwant %s", st.Title, strings.Join(b, "\n     "), strings.Join(a, "\n     "))
		}
	}
}

// describeViews renders a table's views by what a restore keeps, with
// fields named by title: settings, visible columns in order with their
// settings, sorts, filters (nested by group) and kanban stacks.
func describeViews(tb *Table) []string {
	titles := map[string]string{}
	for _, f := range tb.Fields() {
		titles[f.ID] = f.Title
	}
	name := func(raw json.RawMessage, key string) string { return titles[jsonString(raw, key)] }
	pick := func(raw json.RawMessage, keys ...string) string {
		var parts []string
		for _, k := range keys {
			if v := jsonValue(raw, k); v != nil && v != false && v != float64(0) && v != "" {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
		}
		return strings.Join(parts, ",")
	}
	var out []string
	for _, v := range tb.Views {
		sv := parseView(v)
		line := fmt.Sprintf("%s(%d) %s", sv.title, sv.viewType, pick(v.View, "lock_type", "description"))
		line += " {" + pick(sv.settings, "row_height", "heading", "subheading", "success_msg", "submit_another_form") + "}"
		if id := jsonString(sv.settings, "fk_grp_col_id"); id != "" {
			line += " group=" + titles[id]
		}
		if id := jsonString(sv.settings, "fk_cover_image_col_id"); id != "" {
			line += " cover=" + titles[id]
		}
		var ranges []string
		for _, rg := range parseRanges(sv.settings) {
			ranges = append(ranges, titles[rg])
		}
		if len(ranges) > 0 {
			line += " range=" + strings.Join(ranges, "/")
		}
		if id := jsonString(sv.settings, "fk_grp_col_id"); id != "" {
			var meta map[string][]map[string]any
			_ = json.Unmarshal(jsonRaw(sv.settings, "meta"), &meta)
			stacks := meta[id]
			slices.SortFunc(stacks, func(a, b map[string]any) int { return int(a["order"].(float64) - b["order"].(float64)) })
			var names []string
			for _, st := range stacks {
				names = append(names, fmt.Sprintf("%v:%v", st["title"], st["collapsed"]))
			}
			line += " stacks=" + strings.Join(names, ",")
		}
		cols := slices.Clone(v.Columns)
		slices.SortStableFunc(cols, func(a, b json.RawMessage) int {
			oa, ob := jsonValue(a, "order"), jsonValue(b, "order")
			fa, _ := oa.(float64)
			fb, _ := ob.(float64)
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		})
		var shown []string
		for _, col := range cols {
			title := name(col, "fk_column_id")
			if title == "" || !jsonBool(col, "show") {
				continue
			}
			extra := pick(col, "label", "help", "required")
			if w := jsonString(col, "width"); w != "" && w != "200px" {
				extra += " width=" + w
			}
			shown = append(shown, strings.TrimSpace(title+" "+extra))
		}
		line += " cols=[" + strings.Join(shown, "; ") + "]"
		var sorts []string
		for _, so := range v.Sorts {
			sorts = append(sorts, name(so, "fk_column_id")+" "+jsonString(so, "direction"))
		}
		line += " sorts=[" + strings.Join(sorts, ", ") + "]"
		depth := map[string]int{}
		var filters []string
		for _, f := range v.Filters {
			d := 0
			if p := jsonString(f, "fk_parent_id"); p != "" {
				d = depth[p] + 1
			}
			depth[jsonString(f, "id")] = d
			if jsonBool(f, "is_group") {
				filters = append(filters, fmt.Sprintf("%d:group %s", d, jsonString(f, "logical_op")))
				continue
			}
			filters = append(filters, fmt.Sprintf("%d:%s %s %s %v %s", d, name(f, "fk_column_id"),
				jsonString(f, "comparison_op"), jsonString(f, "comparison_sub_op"), jsonValue(f, "value"), jsonString(f, "logical_op")))
		}
		line += " filters=[" + strings.Join(filters, ", ") + "]"
		out = append(out, line)
	}
	return out
}

func parseRanges(settings json.RawMessage) []string {
	var parsed struct {
		Ranges []struct {
			From string `json:"fk_from_column_id"`
		} `json:"calendar_range"`
	}
	_ = json.Unmarshal(settings, &parsed)
	var out []string
	for _, rg := range parsed.Ranges {
		out = append(out, rg.From)
	}
	return out
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
