package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// filesCaptured reports whether the restore can bring attachments back:
// the snapshot holds their files and the caller can open them.
func (r *restorer) filesCaptured() bool {
	return r.header.Attachments && r.opts.OpenFile != nil
}

// restoreFiles uploads each restored record's attachment files and sets
// them on its Attachment fields. Records are inserted without attachments
// because v3 only accepts new ones by URL; v2 storage upload keeps each
// file's title, and a v2 record update sets the uploaded objects.
func (r *restorer) restoreFiles(ctx context.Context) error {
	if !r.filesCaptured() {
		return nil
	}
	n := 0
	_, err := r.read(func(st *Table) error {
		n++
		t := r.byID[st.ID]
		if t == nil || t.newID == "" {
			return nil
		}
		var fields []*planField
		for _, f := range t.fields {
			if f.Type == "Attachment" && f.restored(r.ids) {
				fields = append(fields, f)
			}
		}
		if len(fields) == 0 {
			return nil
		}
		prefix := fmt.Sprintf("[%d/%d] %s: ", n, len(r.tables), t.title)
		if err := r.restoreTableFiles(ctx, t, st, fields, prefix); err != nil {
			return fmt.Errorf("table %q: attachments: %w", t.title, err)
		}
		return nil
	})
	return err
}

// fileRow is one pending v2 update: the record's new attachment values and
// how many files each field holds.
type fileRow struct {
	values map[string]json.RawMessage
	counts map[string]int64
	bytes  int64
}

func (r *restorer) restoreTableFiles(ctx context.Context, t *planTable, st *Table, fields []*planField, prefix string) error {
	files := make(map[string]File, len(st.Files))
	for _, f := range st.Files {
		files[FileKey(f.Source, f.Size)] = f
	}
	ids := r.recordIDs[t.id]

	skipped := map[string]int64{}
	reasons := map[string]string{}
	skip := func(field string, n int64, why string) {
		skipped[field] += n
		if reasons[field] == "" {
			reasons[field] = why
		}
	}
	defer func() {
		for _, f := range fields {
			if c := skipped[f.Title]; c > 0 {
				r.warn(WarnAttachmentsSkipped, t.title, f.Title, c, reasons[f.Title])
			}
		}
	}()
	if t.pkTitle == "" {
		for _, rec := range st.Records {
			for _, f := range fields {
				if c := len(nocodb.ParseAttachments(rec.Fields[f.Title])); c > 0 {
					skip(f.Title, int64(c), "the new table has no ID field to update records by")
				}
			}
		}
		return nil
	}

	update := func(rows []fileRow) error {
		values := make([]map[string]json.RawMessage, len(rows))
		for i, row := range rows {
			values[i] = row.values
		}
		if err := r.api.UpdateRecordsV2(ctx, t.newID, values); err != nil {
			return err
		}
		for _, row := range rows {
			for _, c := range row.counts {
				t.stats.FileCount += c
			}
			t.stats.FileBytes += row.bytes
		}
		return nil
	}
	flush := func(rows []fileRow) error {
		err := update(rows)
		if err == nil || !nocodb.IsBadRequest(err) {
			return err
		}
		// One bad row fails its whole batch; find it.
		for _, row := range rows {
			if len(rows) > 1 {
				err = update([]fileRow{row})
			}
			if err == nil {
				continue
			}
			if !nocodb.IsBadRequest(err) {
				return err
			}
			for field, c := range row.counts {
				skip(field, c, apiMessage(err))
			}
		}
		return nil
	}

	batch := make([]fileRow, 0, r.opts.BatchSize)
	for i, rec := range st.Records {
		newID, ok := ids[rec.IDString()]
		if !ok {
			// The record wasn't restored; records_failed covers it.
			continue
		}
		row := fileRow{
			values: map[string]json.RawMessage{t.pkTitle: newID},
			counts: map[string]int64{},
		}
		for _, f := range fields {
			var uploads []nocodb.UploadFile
			for _, a := range nocodb.ParseAttachments(rec.Fields[f.Title]) {
				up, why, err := r.loadFile(ctx, files, a)
				if err != nil {
					return err
				}
				if why != "" {
					skip(f.Title, 1, why)
					continue
				}
				uploads = append(uploads, up)
			}
			if len(uploads) == 0 {
				continue
			}
			uploaded, err := r.api.UploadFiles(ctx, uploads)
			if err != nil {
				if !uploadRejected(err) {
					return err
				}
				skip(f.Title, int64(len(uploads)), apiMessage(err))
				continue
			}
			v, err := json.Marshal(uploaded)
			if err != nil {
				return err
			}
			row.values[f.Title] = v
			row.counts[f.Title] = int64(len(uploads))
			for _, up := range uploads {
				row.bytes += int64(len(up.Content))
			}
		}
		if len(row.counts) == 0 {
			continue
		}
		batch = append(batch, row)
		if len(batch) == r.opts.BatchSize {
			if err := flush(batch); err != nil {
				return err
			}
			batch = batch[:0]
			r.opts.Progress(fmt.Sprintf("%sattachments %d/%d records", prefix, i+1, len(st.Records)))
		}
	}
	if len(batch) > 0 {
		return flush(batch)
	}
	return nil
}

// loadFile reads one attachment's stored file for upload. why is set when
// the file can't be restored; err only for failures that should stop the
// restore.
func (r *restorer) loadFile(ctx context.Context, files map[string]File, a nocodb.Attachment) (up nocodb.UploadFile, why string, err error) {
	f, ok := files[FileKey(a.Source(), a.Size)]
	switch {
	case !ok:
		return up, "the file is not in the snapshot", nil
	case f.SHA256 == "":
		return up, "the file could not be downloaded when the snapshot was taken: " + f.Error, nil
	}
	rc, err := r.opts.OpenFile(ctx, f.SHA256)
	if err != nil {
		if errors.Is(err, ErrFileUnavailable) {
			return up, "the file is missing from storage", nil
		}
		return up, "", fmt.Errorf("open file %s: %w", f.SHA256, err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return up, "", fmt.Errorf("read file %s: %w", f.SHA256, err)
	}
	title := a.Title
	if title == "" {
		title = path.Base(a.Source())
	}
	return nocodb.UploadFile{Title: title, Mimetype: a.Mimetype, Content: content}, "", nil
}

// uploadRejected reports whether NocoDB refused the upload itself (a bad
// or too large file), so other files can still be tried.
func uploadRejected(err error) bool {
	var apiErr *nocodb.APIError
	return nocodb.IsBadRequest(err) ||
		(errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusRequestEntityTooLarge)
}
