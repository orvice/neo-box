// Package snapshot captures a NocoDB Base (schema, records, links) into a
// self-describing gzip-compressed JSON document, and reads it back.
//
// Document layout (format version 2):
//
//	{
//	  "format": "neobox.nocodb.snapshot",
//	  "version": 2,
//	  "created_at": "...",
//	  "source": {"base_url": "...", "base_id": "..."},
//	  "attachments": true,
//	  "base": { ...v3 base meta, verbatim... },
//	  "tables": [
//	    {
//	      "id": "...", "title": "...",
//	      "schema": { ...v3 table schema incl. fields, verbatim... },
//	      "records": [ {"id": 1, "fields": {...}}, ... ],
//	      "links": [ {"field_id": "...", "record_id": 1, "linked_ids": [3, 4]}, ... ],
//	      "files": [ {"source": "download/...", "size": 17, "sha256": "..."}, ... ],
//	      "views": [ {"view": {...}, "columns": [...], "sorts": [...], "filters": [...]}, ... ]
//	    }
//	  ]
//	}
//
// NocoDB payloads are kept verbatim (json.RawMessage) so a restore sees
// exactly what the API returned. Tables are written one at a time so peak
// memory is bounded by the largest table, not the whole Base.
//
// "attachments" says attachment files were captured. The bytes are not in
// the document: each table's "files" lists the distinct files its
// Attachment values reference, by source (the attachment's path, else its
// url) and size, with the sha256 they are stored under, or the error that
// kept one from being read. Version 1 documents have neither.
//
// "views" holds every view of the table as v2 meta returns it (v3 views
// are licence-gated in OSS): the view with its type-specific settings, its
// column settings, sorts, and filters, with nested groups flattened and
// linked by fk_parent_id, each group before its members. A table without
// "views" had them left out (version 1, or a version 2 document written
// before views were captured).
package snapshot

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

const (
	FormatName    = "neobox.nocodb.snapshot"
	FormatVersion = 2
	// MinFormatVersion is the oldest version readers and Restore accept.
	MinFormatVersion = 1
	ContentType      = "application/gzip"
)

// Source identifies where a snapshot was taken from.
type Source struct {
	BaseURL string `json:"base_url"`
	BaseID  string `json:"base_id"`
}

// Header is everything in the document except the tables.
type Header struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Source    Source    `json:"source"`
	// Attachments is set when attachment files were captured.
	Attachments bool            `json:"attachments,omitempty"`
	Base        json.RawMessage `json:"base"`
}

// Link records that RecordID is linked to LinkedIDs through FieldID.
type Link struct {
	FieldID   string            `json:"field_id"`
	RecordID  json.RawMessage   `json:"record_id"`
	LinkedIDs []json.RawMessage `json:"linked_ids"`
}

// File is one distinct attachment file referenced by a table's records.
// SHA256 names the stored content; it is empty when the file could not be
// read, and Error says why.
type File struct {
	Source string `json:"source"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Error  string `json:"error,omitempty"`
}

// FileKey identifies a file by its attachment's source and size.
func FileKey(source string, size int64) string {
	return fmt.Sprintf("%d:%s", size, source)
}

// View is one view of a table, verbatim from v2 meta.
type View struct {
	View    json.RawMessage   `json:"view"`
	Columns []json.RawMessage `json:"columns"`
	Sorts   []json.RawMessage `json:"sorts"`
	Filters []json.RawMessage `json:"filters"`
}

// Table is one table's schema and content.
type Table struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Schema  json.RawMessage `json:"schema"`
	Records []nocodb.Record `json:"records"`
	Links   []Link          `json:"links"`
	Files   []File          `json:"files,omitempty"`
	Views   []View          `json:"views,omitempty"`
}

// LinkCount is the number of (record, linked record) pairs in the table.
func (t *Table) LinkCount() int64 {
	var n int64
	for _, l := range t.Links {
		n += int64(len(l.LinkedIDs))
	}
	return n
}

// Fields parses the field list out of the verbatim schema.
func (t *Table) Fields() []nocodb.Field {
	var parsed struct {
		Fields []nocodb.Field `json:"fields"`
	}
	_ = json.Unmarshal(t.Schema, &parsed)
	return parsed.Fields
}

// Writer streams a document: WriteHeader, then WriteTable per table, then
// Close.
type Writer struct {
	gz     *gzip.Writer
	buf    *bufio.Writer
	tables int
	state  int
}

const (
	stateNew = iota
	stateTables
	stateClosed
)

// NewWriter wraps w. Close must be called to flush the gzip stream; it does
// not close w.
func NewWriter(w io.Writer) *Writer {
	gz := gzip.NewWriter(w)
	return &Writer{gz: gz, buf: bufio.NewWriter(gz)}
}

func (w *Writer) WriteHeader(h Header) error {
	if w.state != stateNew {
		return errors.New("snapshot: header already written")
	}
	h.Format = FormatName
	h.Version = FormatVersion
	if len(h.Base) == 0 {
		h.Base = json.RawMessage("null")
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	// Re-open the header object so "tables" becomes its last member.
	if _, err := w.buf.Write(raw[:len(raw)-1]); err != nil {
		return err
	}
	if _, err := w.buf.WriteString(`,"tables":[`); err != nil {
		return err
	}
	w.state = stateTables
	return nil
}

func (w *Writer) WriteTable(t *Table) error {
	if w.state != stateTables {
		return errors.New("snapshot: write header before tables")
	}
	if t.Records == nil {
		t.Records = []nocodb.Record{}
	}
	if t.Links == nil {
		t.Links = []Link{}
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("snapshot: encode table %s: %w", t.ID, err)
	}
	if w.tables > 0 {
		if err := w.buf.WriteByte(','); err != nil {
			return err
		}
	}
	if _, err := w.buf.Write(raw); err != nil {
		return err
	}
	w.tables++
	return nil
}

func (w *Writer) Close() error {
	if w.state == stateClosed {
		return nil
	}
	if w.state != stateTables {
		return errors.New("snapshot: closing before header was written")
	}
	w.state = stateClosed
	if _, err := w.buf.WriteString("]}"); err != nil {
		return err
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	return w.gz.Close()
}

// ReadTable scans a gzip document and returns the table with tableID, or
// (nil, nil) when it is absent. Other tables are skipped without being kept
// in memory.
func ReadTable(r io.Reader, tableID string) (*Header, *Table, error) {
	var found *Table
	header, err := ReadTables(r, func(t *Table) error {
		if t.ID == tableID {
			found = t
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return header, found, nil
}

// ReadTables scans a gzip document and calls fn with each table in document
// order. Only one table is held in memory at a time. The header is complete
// before the first call, because writers put "tables" last.
func ReadTables(r io.Reader, fn func(*Table) error) (*Header, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("snapshot: open gzip: %w", err)
	}
	defer gz.Close()
	dec := json.NewDecoder(bufio.NewReader(gz))

	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	header := &Header{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		switch key {
		case "format":
			err = dec.Decode(&header.Format)
		case "version":
			err = dec.Decode(&header.Version)
		case "created_at":
			err = dec.Decode(&header.CreatedAt)
		case "source":
			err = dec.Decode(&header.Source)
		case "attachments":
			err = dec.Decode(&header.Attachments)
		case "base":
			err = dec.Decode(&header.Base)
		case "tables":
			if err = expectDelim(dec, '['); err != nil {
				return nil, err
			}
			for dec.More() {
				var t Table
				if err := dec.Decode(&t); err != nil {
					return nil, fmt.Errorf("snapshot: decode table: %w", err)
				}
				if err := fn(&t); err != nil {
					return nil, err
				}
			}
			err = expectDelim(dec, ']')
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot: decode %q: %w", key, err)
		}
		if header.Format != "" && header.Format != FormatName {
			return nil, fmt.Errorf("snapshot: unexpected format %q", header.Format)
		}
	}
	return header, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("snapshot: read token: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("snapshot: expected %q, got %v", want, tok)
	}
	return nil
}
