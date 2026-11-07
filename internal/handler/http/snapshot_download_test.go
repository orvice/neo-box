package http

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/repo/auth"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type fakeSnapshotContent struct {
	snap    *repo.Snapshot
	body    []byte
	getErr  error
	openErr error
}

func (f *fakeSnapshotContent) GetSnapshot(_ *gin.Context, userID, id string) (*repo.Snapshot, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if userID == "" || id != f.snap.ID {
		return nil, repo.ErrNotFound
	}
	return f.snap, nil
}

func (f *fakeSnapshotContent) OpenContent(_ *gin.Context, _ *repo.Snapshot) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func newDownloadEngine(t *testing.T, content SnapshotContent) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := auth.WithAuthenticated(c.Request.Context(), &neoboxv1.User{Id: "u1"}, &auth.Session{ID: "s1", UserID: "u1"})
		c.Request = c.Request.WithContext(ctx)
	})
	RegisterSnapshotDownload(r, func() SnapshotContent { return content })
	return r
}

func gzipPayload(t *testing.T, size int) []byte {
	t.Helper()
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSnapshotDownloadStreamsPastTheRPCLimit(t *testing.T) {
	body := gzipPayload(t, 6<<20)
	if len(body) <= connectx.RPCMaxRequestBytes {
		t.Fatalf("test payload is only %d bytes; it must exceed the %d-byte RPC limit", len(body), connectx.RPCMaxRequestBytes)
	}
	snap := &repo.Snapshot{
		ID:        "snap1",
		UserID:    "u1",
		BaseTitle: "My Base / 2024",
		SizeBytes: int64(len(body)),
		CreatedAt: time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	engine := newDownloadEngine(t, &fakeSnapshotContent{snap: snap, body: body})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("Content-Type = %q", got)
	}
	wantName := `attachment; filename="nocodb-My-Base---2024-20240501-120000.json.gz"`
	if got := rec.Header().Get("Content-Disposition"); got != wantName {
		t.Fatalf("Content-Disposition = %q, want %q", got, wantName)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(body))
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("streamed %d bytes, want %d", rec.Body.Len(), len(body))
	}
}

func TestSnapshotDownloadAuthAndLookupFailures(t *testing.T) {
	snap := &repo.Snapshot{ID: "snap1", UserID: "u1", CreatedAt: time.Now()}
	content := &fakeSnapshotContent{snap: snap, body: []byte("x")}

	t.Run("no session", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		RegisterSnapshotDownload(r, func() SnapshotContent { return content })
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("provider not wired yet", func(t *testing.T) {
		engine := newDownloadEngine(t, nil)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown snapshot", func(t *testing.T) {
		engine := newDownloadEngine(t, content)
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nocodb/snapshots/other/download", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("content unavailable", func(t *testing.T) {
		engine := newDownloadEngine(t, &fakeSnapshotContent{snap: snap, openErr: repo.ErrNotFound})
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
	})
}
