package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.orx.me/apps/neo-box/internal/blobstore"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// withFiles makes p1's one record hold two attachments: a.txt (stored by
// NocoDB, served at its signed path) and b.png (an external URL that is
// gone).
func withFiles(h *harness) {
	h.api.docs = `[{"path":"download/a.txt","signedPath":"dltemp/s/a.txt","title":"a.txt","mimetype":"text/plain","size":5},{"url":"https://cdn/b.png","title":"b.png","size":9}]`
	h.api.files = map[string]string{"dltemp/s/a.txt": "hello"}
}

func (h *harness) stored(t *testing.T, sha string) bool {
	t.Helper()
	ok, err := h.repo.FileExists(context.Background(), "u1", sha)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := h.blobs.Get(context.Background(), FileKey("u1", sha))
	if err == nil {
		rc.Close()
	}
	if ok != (err == nil) {
		t.Fatalf("file record exists = %v but blob err = %v", ok, err)
	}
	return ok
}

func TestSnapshotStoresFilesOnce(t *testing.T) {
	h := newHarness(t)
	withFiles(h)
	ctx := context.Background()

	first := h.snapshot(t)
	if first.Status != repo.StatusSucceeded || !first.AttachmentsIncluded ||
		first.FileCount != 1 || first.FileBytes != 5 || first.FilesMissing != 1 {
		t.Fatalf("first = %+v", first)
	}
	if !h.stored(t, sum("hello")) {
		t.Fatal("a.txt not stored")
	}
	// The second snapshot finds a.txt already stored; only the broken
	// b.png is tried again.
	second := h.snapshot(t)
	if second.FileCount != 1 || second.FilesMissing != 1 {
		t.Fatalf("second = %+v", second)
	}
	downloads := slices.Sorted(slices.Values(h.api.downloads))
	if got := strings.Join(downloads, ","); got != "dltemp/s/a.txt,https://cdn/b.png,https://cdn/b.png" {
		t.Fatalf("downloads = %s", got)
	}

	// The file outlives the first snapshot because the second uses it.
	if err := h.m.DeleteSnapshot(ctx, first); err != nil {
		t.Fatal(err)
	}
	if !h.stored(t, sum("hello")) {
		t.Fatal("a.txt deleted while a snapshot still uses it")
	}
	if err := h.m.DeleteSnapshot(ctx, second); err != nil {
		t.Fatal(err)
	}
	if h.stored(t, sum("hello")) {
		t.Fatal("a.txt kept after its last snapshot was deleted")
	}

	// The hint now points at a deleted file, so it is downloaded again.
	h.snapshot(t)
	if n := strings.Count(strings.Join(h.api.downloads, ","), "dltemp/s/a.txt"); n != 2 {
		t.Fatalf("a.txt downloaded %d times, want 2", n)
	}
}

func TestPolicyCanLeaveFilesOut(t *testing.T) {
	h := newHarness(t)
	withFiles(h)
	_ = h.repo.UpsertPolicy(context.Background(), &repo.Policy{ConnectionID: "c1", BaseID: "p1", UserID: "u1", IncludeAttachments: false})

	snap := h.snapshot(t)
	if snap.Status != repo.StatusSucceeded || snap.AttachmentsIncluded || snap.FileCount != 0 || len(h.api.downloads) != 0 {
		t.Fatalf("snap = %+v, downloads = %v", snap, h.api.downloads)
	}
}

func TestFailedSnapshotKeepsNoFiles(t *testing.T) {
	h := newHarness(t)
	withFiles(h)
	h.api.failTable = true

	snap := h.snapshot(t)
	if snap.Status != repo.StatusFailed {
		t.Fatalf("snap = %+v", snap)
	}
	if len(h.api.downloads) == 0 {
		t.Fatal("the first table's files were not downloaded before the failure")
	}
	if h.stored(t, sum("hello")) {
		t.Fatal("a failed snapshot kept its files")
	}
}

func TestRestoreUploadsFiles(t *testing.T) {
	h := newHarness(t)
	withFiles(h)
	snap := h.snapshot(t)

	r, err := h.m.EnqueueRestore(context.Background(), snap, h.target(), "CRM copy")
	if err != nil {
		t.Fatal(err)
	}
	done := h.waitRestore(t, r.ID)
	if done.Status != repo.RestoreSucceeded || done.FileCount != 1 {
		t.Fatalf("done = %+v", done)
	}
	if len(h.api.uploads) != 1 || h.api.uploads[0].Title != "a.txt" || string(h.api.uploads[0].Content) != "hello" || h.api.uploads[0].Mimetype != "text/plain" {
		t.Fatalf("uploads = %+v", h.api.uploads)
	}
	if len(h.api.updates) != 1 || string(h.api.updates[0]["Id"]) != "1" {
		t.Fatalf("updates = %v", h.api.updates)
	}
	if len(done.Warnings) != 1 || done.Warnings[0].Code != "attachments_skipped" || done.Warnings[0].Count != 1 {
		t.Fatalf("warnings = %+v", done.Warnings)
	}
}

func TestOpenFileMissing(t *testing.T) {
	h := newHarness(t)
	_, err := h.m.openFile(context.Background(), "u1", "nope")
	if err == nil || errors.Is(err, blobstore.ErrNotFound) || !strings.Contains(err.Error(), "file unavailable") {
		t.Fatalf("err = %v", err)
	}
}
