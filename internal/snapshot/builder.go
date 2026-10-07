package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Files stores the attachment files of a snapshot being built.
type Files interface {
	// Store makes the file behind att available under its sha256 and
	// returns it. An error wrapping ErrFileUnavailable is recorded on the
	// file and the build goes on; any other error fails the build.
	Store(ctx context.Context, att nocodb.Attachment) (sha256 string, err error)
}

// ErrFileUnavailable marks an attachment file that could not be read.
var ErrFileUnavailable = errors.New("file unavailable")

// TableStats summarizes one captured (or restored) table.
type TableStats struct {
	ID          string
	Title       string
	RecordCount int64
	FieldCount  int
	LinkCount   int64
	// FileCount and FileBytes count the attachment files captured (or
	// restored); FilesMissing those that could not be read.
	FileCount    int64
	FileBytes    int64
	FilesMissing int64
}

// Stats summarizes a captured Base.
type Stats struct {
	Tables       []TableStats
	RecordCount  int64
	LinkCount    int64
	FileCount    int64
	FileBytes    int64
	FilesMissing int64
}

// Options tune a build.
type Options struct {
	PageSize int
	// LinkConcurrency bounds parallel linked-record lookups. The client's
	// rate limiter still applies; concurrency only hides request latency.
	LinkConcurrency int
	// Files stores attachment files. Nil leaves them out: only their
	// metadata is captured, as in format version 1.
	Files Files
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
	header := Header{CreatedAt: opts.Now().UTC(), Source: src, Attachments: opts.Files != nil, Base: baseRaw}
	if err := sw.WriteHeader(header); err != nil {
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
		for _, f := range t.Files {
			if f.SHA256 == "" {
				ts.FilesMissing++
				continue
			}
			ts.FileCount++
			ts.FileBytes += f.Size
		}
		stats.Tables = append(stats.Tables, ts)
		stats.RecordCount += ts.RecordCount
		stats.LinkCount += ts.LinkCount
		stats.FileCount += ts.FileCount
		stats.FileBytes += ts.FileBytes
		stats.FilesMissing += ts.FilesMissing
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

	if err := captureLinks(ctx, api, baseID, schema, t, opts, progress); err != nil {
		return nil, err
	}
	if opts.Files != nil {
		if err := captureFiles(ctx, schema, t, opts, progress); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func captureLinks(ctx context.Context, api API, baseID string, schema *nocodb.Table, t *Table, opts Options, progress func(string)) error {
	var linkFields []nocodb.Field
	for _, f := range schema.Fields {
		if f.IsLink() {
			linkFields = append(linkFields, f)
		}
	}
	if len(linkFields) == 0 {
		return nil
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
		return nil
	}

	results := make([]Link, len(jobs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.LinkConcurrency)
	for i, j := range jobs {
		g.Go(func() error {
			ids, err := api.ListLinkedIDs(gctx, baseID, t.ID, j.field.ID, j.record.IDString())
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
		return err
	}
	for _, l := range results {
		if len(l.LinkedIDs) > 0 {
			t.Links = append(t.Links, l)
		}
	}
	return nil
}

// captureFiles stores every distinct file the table's Attachment values
// reference and lists them in t.Files. Tables without Attachment fields
// get no list.
func captureFiles(ctx context.Context, schema *nocodb.Table, t *Table, opts Options, progress func(string)) error {
	var fields []string
	for _, f := range schema.Fields {
		if f.Type == "Attachment" {
			fields = append(fields, f.Title)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	var atts []nocodb.Attachment
	seen := map[string]bool{}
	for _, r := range t.Records {
		for _, title := range fields {
			for _, a := range nocodb.ParseAttachments(r.Fields[title]) {
				key := FileKey(a.Source(), a.Size)
				if a.Source() == "" || seen[key] {
					continue
				}
				seen[key] = true
				atts = append(atts, a)
			}
		}
	}
	t.Files = make([]File, len(atts))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.LinkConcurrency)
	for i, a := range atts {
		g.Go(func() error {
			f := File{Source: a.Source(), Size: a.Size}
			sha, err := opts.Files.Store(gctx, a)
			switch {
			case err == nil:
				f.SHA256 = sha
			case errors.Is(err, ErrFileUnavailable):
				f.Error = err.Error()
			default:
				return fmt.Errorf("file %q: %w", a.Title, err)
			}
			t.Files[i] = f
			if (i+1)%20 == 0 {
				progress(fmt.Sprintf("%d records, files %d/%d", len(t.Records), i+1, len(atts)))
			}
			return nil
		})
	}
	return g.Wait()
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
