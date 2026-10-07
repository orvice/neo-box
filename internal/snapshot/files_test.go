package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// fakeFiles stores files by source; sources listed in missing are
// unavailable, and fatal fails the build.
type fakeFiles struct {
	mu      sync.Mutex
	stored  []string
	missing map[string]bool
	fatal   string
}

func (f *fakeFiles) Store(_ context.Context, a nocodb.Attachment) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case a.Source() == f.fatal:
		return "", errors.New("blob store down")
	case f.missing[a.Source()]:
		return "", fmt.Errorf("%w: 404 not found", ErrFileUnavailable)
	}
	f.stored = append(f.stored, a.Source())
	return "sha-" + a.Source(), nil
}

func filesAPI() *fakeAPI {
	return &fakeAPI{
		tables: []nocodb.TableSummary{{ID: "m1", Title: "Docs"}, {ID: "m2", Title: "Plain"}},
		schemas: map[string]*nocodb.Table{
			"m1": schema("m1", "Docs", `[{"id":"c1","title":"Name","type":"SingleLineText"},{"id":"c2","title":"Files","type":"Attachment"},{"id":"c3","title":"Cover","type":"Attachment"}]`),
			"m2": schema("m2", "Plain", `[{"id":"c4","title":"Name","type":"SingleLineText"}]`),
		},
		records: map[string][]nocodb.Record{
			"m1": {
				rec(1, `{"Name":"a","Files":[{"path":"download/a.txt","signedPath":"dltemp/x/a.txt","title":"a.txt","size":1},{"url":"https://cdn/b.png","title":"b.png","size":2}],"Cover":[{"path":"download/a.txt","title":"a.txt","size":1}]}`),
				rec(2, `{"Name":"b","Files":[{"path":"download/c.txt","title":"c.txt","size":3}],"Cover":null}`),
				rec(3, `{"Name":"c","Files":[]}`),
			},
			"m2": {rec(10, `{"Name":"x"}`)},
		},
	}
}

func TestBuildCapturesFiles(t *testing.T) {
	files := &fakeFiles{missing: map[string]bool{"https://cdn/b.png": true}}
	var buf bytes.Buffer
	stats, err := Build(context.Background(), filesAPI(), Source{BaseID: "p1"}, &buf, Options{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	// a.txt is referenced twice but stored once.
	slices.Sort(files.stored)
	if !slices.Equal(files.stored, []string{"download/a.txt", "download/c.txt"}) {
		t.Fatalf("stored = %v", files.stored)
	}
	if stats.FileCount != 2 || stats.FileBytes != 4 || stats.FilesMissing != 1 {
		t.Fatalf("stats = %+v", stats)
	}

	header, docs, err := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if !header.Attachments {
		t.Fatal("header does not say attachments were captured")
	}
	want := []File{
		{Source: "download/a.txt", Size: 1, SHA256: "sha-download/a.txt"},
		{Source: "https://cdn/b.png", Size: 2, Error: "file unavailable: 404 not found"},
		{Source: "download/c.txt", Size: 3, SHA256: "sha-download/c.txt"},
	}
	if !slices.Equal(docs.Files, want) {
		t.Fatalf("files = %+v", docs.Files)
	}
	// Records stay verbatim.
	if !strings.Contains(string(docs.Records[0].Fields["Files"]), `"signedPath":"dltemp/x/a.txt"`) {
		t.Fatalf("record = %s", docs.Records[0].Fields["Files"])
	}
	_, plain, _ := ReadTable(bytes.NewReader(buf.Bytes()), "m2")
	if plain.Files != nil {
		t.Fatalf("a table without Attachment fields has files: %+v", plain.Files)
	}
}

func TestBuildWithoutFiles(t *testing.T) {
	var buf bytes.Buffer
	stats, err := Build(context.Background(), filesAPI(), Source{BaseID: "p1"}, &buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	header, docs, _ := ReadTable(bytes.NewReader(buf.Bytes()), "m1")
	if header.Attachments || docs.Files != nil || stats.FileCount != 0 {
		t.Fatalf("attachments captured without Files: %+v / %+v", header, docs.Files)
	}
}

func TestBuildFailsOnFileStoreError(t *testing.T) {
	var buf bytes.Buffer
	_, err := Build(context.Background(), filesAPI(), Source{BaseID: "p1"}, &buf, Options{Files: &fakeFiles{fatal: "download/c.txt"}})
	if err == nil || !strings.Contains(err.Error(), "blob store down") {
		t.Fatalf("err = %v", err)
	}
}

// filesDoc builds a snapshot of filesAPI with b.png unavailable.
func filesDoc(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	files := &fakeFiles{missing: map[string]bool{"https://cdn/b.png": true}}
	if _, err := Build(context.Background(), filesAPI(), Source{BaseID: "p1"}, &buf, Options{
		Files: files, Now: func() time.Time { return time.Unix(0, 0) },
	}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRestoreFiles(t *testing.T) {
	api := newFakeNoco()
	blobs := map[string]string{"sha-download/a.txt": "A"}
	report, err := Restore(context.Background(), api, opener(filesDoc(t)), RestoreOptions{
		Title: "x",
		OpenFile: func(_ context.Context, sha string) (io.ReadCloser, error) {
			content, ok := blobs[sha]
			if !ok {
				return nil, fmt.Errorf("%w: gone", ErrFileUnavailable)
			}
			return io.NopCloser(strings.NewReader(content)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Record 1: a.txt in both fields (one upload per field); b.png was
	// never downloaded. Record 2: c.txt is missing from storage.
	if want := [][]string{{"a.txt=A"}, {"a.txt=A"}}; !slicesEqual(api.uploads, want) {
		t.Fatalf("uploads = %v", api.uploads)
	}
	docs := api.tableByTitle("Docs")
	if len(api.updates) != 1 {
		t.Fatalf("updates = %v", api.updates)
	}
	row := api.updates[0]
	if string(row["Id"]) != docs.records[0] || !strings.Contains(string(row["Files"]), `"title":"a.txt"`) || !strings.Contains(string(row["Cover"]), `"path":"download/new/a.txt"`) {
		t.Fatalf("update = %v", row)
	}
	if report.FileCount != 2 || report.Tables[0].FileCount != 2 {
		t.Fatalf("file count = %d / %+v", report.FileCount, report.Tables)
	}
	var skipped []string
	for _, w := range report.Warnings {
		if w.Code == WarnAttachmentsSkipped {
			skipped = append(skipped, fmt.Sprintf("%s/%s %d: %s", w.Table, w.Field, w.Count, w.Message))
		}
	}
	want := []string{"Docs/Files 2: the file could not be downloaded when the snapshot was taken: file unavailable: 404 not found"}
	if !slices.Equal(skipped, want) {
		t.Fatalf("skipped = %q", skipped)
	}
}

func TestRestoreFilesRejectedUpload(t *testing.T) {
	api := newFakeNoco()
	api.failUpload = func(files []nocodb.UploadFile) bool { return true }
	report, err := Restore(context.Background(), api, opener(filesDoc(t)), RestoreOptions{
		Title: "x",
		OpenFile: func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("x")), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.FileCount != 0 || len(api.updates) != 0 {
		t.Fatalf("files = %d, updates = %v", report.FileCount, api.updates)
	}
	got := map[string]int64{}
	for _, w := range report.Warnings {
		got[w.Field] += w.Count
	}
	// Files: a.txt + c.txt rejected, b.png never downloaded. Cover: a.txt.
	if got["Files"] != 3 || got["Cover"] != 1 {
		t.Fatalf("warnings = %+v", report.Warnings)
	}
}

func slicesEqual(a, b [][]string) bool {
	return slices.EqualFunc(a, b, func(x, y []string) bool { return slices.Equal(x, y) })
}
