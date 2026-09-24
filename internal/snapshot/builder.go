package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// API is the slice of the NocoDB client the builder needs.
type API interface {
	GetBaseRaw(ctx context.Context, baseID string) (json.RawMessage, error)
	ListTables(ctx context.Context, baseID string) ([]nocodb.TableSummary, error)
	GetTable(ctx context.Context, baseID, tableID string) (*nocodb.Table, error)
	ListRecords(ctx context.Context, baseID, tableID string, page, pageSize int) (*nocodb.RecordPage, error)
	ListLinkedIDs(ctx context.Context, baseID, tableID, linkFieldID, recordID string) ([]json.RawMessage, error)
}

// TableStats summarizes one captured table.
type TableStats struct {
	ID          string
	Title       string
	RecordCount int64
	FieldCount  int
	LinkCount   int64
}

// Stats summarizes a captured Base.
type Stats struct {
	Tables      []TableStats
	RecordCount int64
	LinkCount   int64
}

// Options tune a build.
type Options struct {
	PageSize int
	// LinkConcurrency bounds parallel linked-record lookups. The client's
	// rate limiter still applies; concurrency only hides request latency.
	LinkConcurrency int
	// Progress receives human-readable status updates. May be nil.
	Progress func(msg string)
	Now      func() time.Time
}

const maxPagesPerTable = 1_000_000

// Build captures baseID from api into w as a snapshot document.
func Build(ctx context.Context, api API, src Source, w io.Writer, opts Options) (*Stats, error) {
	if opts.PageSize <= 0 {
		opts.PageSize = 200
	}
	if opts.LinkConcurrency <= 0 {
		opts.LinkConcurrency = 4
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}

	progress("reading base schema")
	baseRaw, err := api.GetBaseRaw(ctx, src.BaseID)
	if err != nil {
		return nil, fmt.Errorf("get base: %w", err)
	}
	tables, err := api.ListTables(ctx, src.BaseID)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}

	sw := NewWriter(w)
	if err := sw.WriteHeader(Header{CreatedAt: opts.Now().UTC(), Source: src, Base: baseRaw}); err != nil {
		return nil, err
	}

	stats := &Stats{}
	for i, summary := range tables {
		prefix := fmt.Sprintf("[%d/%d] %s", i+1, len(tables), summary.Title)
		t, err := captureTable(ctx, api, src.BaseID, summary, opts, func(msg string) { progress(prefix + ": " + msg) })
		if err != nil {
			return nil, fmt.Errorf("table %q: %w", summary.Title, err)
		}
		if err := sw.WriteTable(t); err != nil {
			return nil, err
		}
		ts := TableStats{
			ID:          t.ID,
			Title:       t.Title,
			RecordCount: int64(len(t.Records)),
			FieldCount:  len(t.Fields()),
			LinkCount:   t.LinkCount(),
		}
		stats.Tables = append(stats.Tables, ts)
		stats.RecordCount += ts.RecordCount
		stats.LinkCount += ts.LinkCount
	}
	if err := sw.Close(); err != nil {
		return nil, err
	}
	progress("done")
	return stats, nil
}

func captureTable(ctx context.Context, api API, baseID string, summary nocodb.TableSummary, opts Options, progress func(string)) (*Table, error) {
	progress("reading schema")
	schema, err := api.GetTable(ctx, baseID, summary.ID)
	if err != nil {
		return nil, fmt.Errorf("get schema: %w", err)
	}
	title := schema.Title
	if title == "" {
		title = summary.Title
	}
	t := &Table{ID: summary.ID, Title: title, Schema: schema.Raw}

	for page := 1; page <= maxPagesPerTable; page++ {
		res, err := api.ListRecords(ctx, baseID, summary.ID, page, opts.PageSize)
		if err != nil {
			return nil, fmt.Errorf("list records page %d: %w", page, err)
		}
		t.Records = append(t.Records, res.Records...)
		progress(strconv.Itoa(len(t.Records)) + " records")
		if !res.HasNext || len(res.Records) == 0 {
			break
		}
	}

	var linkFields []nocodb.Field
	for _, f := range schema.Fields {
		if f.IsLink() {
			linkFields = append(linkFields, f)
		}
	}
	if len(linkFields) == 0 {
		return t, nil
	}

	type job struct {
		field  nocodb.Field
		record nocodb.Record
	}
	var jobs []job
	for _, f := range linkFields {
		for _, r := range t.Records {
			if hasLinks(r.Fields[f.Title]) {
				jobs = append(jobs, job{field: f, record: r})
			}
		}
	}
	if len(jobs) == 0 {
		return t, nil
	}

	results := make([]Link, len(jobs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.LinkConcurrency)
	for i, j := range jobs {
		g.Go(func() error {
			ids, err := api.ListLinkedIDs(gctx, baseID, summary.ID, j.field.ID, j.record.IDString())
			if err != nil {
				return fmt.Errorf("links %q of record %s: %w", j.field.Title, j.record.IDString(), err)
			}
			results[i] = Link{FieldID: j.field.ID, RecordID: j.record.ID, LinkedIDs: ids}
			if (i+1)%50 == 0 {
				progress(fmt.Sprintf("%d records, links %d/%d", len(t.Records), i+1, len(jobs)))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for _, l := range results {
		if len(l.LinkedIDs) > 0 {
			t.Links = append(t.Links, l)
		}
	}
	return t, nil
}

// hasLinks reports whether a record's value for a link field indicates at
// least one linked record. NocoDB returns a count for Links fields and the
// linked record(s) for LinkToAnotherRecord fields.
func hasLinks(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	switch raw[0] {
	case '[':
		var arr []json.RawMessage
		return json.Unmarshal(raw, &arr) == nil && len(arr) > 0
	case '{':
		return true
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			return false
		}
		n, err := strconv.ParseFloat(s, 64)
		return err != nil || n > 0
	default:
		n, err := strconv.ParseFloat(string(raw), 64)
		return err == nil && n > 0
	}
}
