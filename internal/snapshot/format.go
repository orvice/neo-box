// Package snapshot captures a NocoDB Base (schema, records, links) into a
// self-describing gzip-compressed JSON document, and reads it back.
//
// Document layout (format version 1):
//
//	{
//	  "format": "neobox.nocodb.snapshot",
//	  "version": 1,
//	  "created_at": "...",
//	  "source": {"base_url": "...", "base_id": "..."},
//	  "base": { ...v3 base meta, verbatim... },
//	  "tables": [
//	    {
//	      "id": "...", "title": "...",
//	      "schema": { ...v3 table schema incl. fields, verbatim... },
//	      "records": [ {"id": 1, "fields": {...}}, ... ],
//	      "links": [ {"field_id": "...", "record_id": 1, "linked_ids": [3, 4]}, ... ]
//	    }
//	  ]
//	}
//
// NocoDB payloads are kept verbatim (json.RawMessage) so a future restore
// sees exactly what the API returned. Tables are written one at a time so
// peak memory is bounded by the largest table, not the whole Base.
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
	FormatVersion = 1
	ContentType   = "application/gzip"
)

// Source identifies where a snapshot was taken from.
type Source struct {
	BaseURL string `json:"base_url"`
	BaseID  string `json:"base_id"`
}

// Header is everything in the document except the tables.
type Header struct {
	Format    string          `json:"format"`
	Version   int             `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	Source    Source          `json:"source"`
	Base      json.RawMessage `json:"base"`
}

// Link records that RecordID is linked to LinkedIDs through FieldID.
type Link struct {
	FieldID   string            `json:"field_id"`
	RecordID  json.RawMessage   `json:"record_id"`
	LinkedIDs []json.RawMessage `json:"linked_ids"`
}

// Table is one table's schema and content.
type Table struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Schema  json.RawMessage `json:"schema"`
	Records []nocodb.Record `json:"records"`
	Links   []Link          `json:"links"`
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
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot: open gzip: %w", err)
	}
	defer gz.Close()
	dec := json.NewDecoder(bufio.NewReader(gz))

	if err := expectDelim(dec, '{'); err != nil {
		return nil, nil, err
	}
	header := &Header{}
	var found *Table
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, err
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
		case "base":
			err = dec.Decode(&header.Base)
		case "tables":
			if err = expectDelim(dec, '['); err != nil {
				return nil, nil, err
			}
			for dec.More() {
				var t Table
				if err := dec.Decode(&t); err != nil {
					return nil, nil, fmt.Errorf("snapshot: decode table: %w", err)
				}
				if t.ID == tableID {
					found = &t
				}
			}
			err = expectDelim(dec, ']')
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("snapshot: decode %q: %w", key, err)
		}
		if header.Format != "" && header.Format != FormatName {
			return nil, nil, fmt.Errorf("snapshot: unexpected format %q", header.Format)
		}
	}
	return header, found, nil
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
