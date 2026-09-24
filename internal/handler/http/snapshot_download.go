package http

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"butterfly.orx.me/core/log"
	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/repo/auth"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/snapshot"
)

// SnapshotContent resolves and opens a user's snapshot content.
type SnapshotContent interface {
	GetSnapshot(ctx *gin.Context, userID, id string) (*repo.Snapshot, error)
	OpenContent(ctx *gin.Context, s *repo.Snapshot) (io.ReadCloser, error)
}

// SnapshotContentProvider returns the content source once bootstrap wired it.
type SnapshotContentProvider func() SnapshotContent

// RegisterSnapshotDownload mounts GET /api/nocodb/snapshots/:id/download,
// which streams the stored gzip JSON document.
func RegisterSnapshotDownload(r *gin.Engine, provider SnapshotContentProvider) {
	r.GET("/api/nocodb/snapshots/:id/download", func(c *gin.Context) {
		user, ok := auth.UserFromContext(c.Request.Context())
		if !ok {
			unauthorized(c)
			return
		}
		src := provider()
		if src == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "snapshots are not available"})
			return
		}
		snap, err := src.GetSnapshot(c, user.GetId(), c.Param("id"))
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "snapshot not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "lookup failed"})
			return
		}
		rc, err := src.OpenContent(c, snap)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "snapshot content unavailable"})
			return
		}
		defer rc.Close()

		c.Header("Content-Type", snapshot.ContentType)
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadName(snap)))
		if snap.SizeBytes > 0 {
			c.Header("Content-Length", fmt.Sprint(snap.SizeBytes))
		}
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, rc); err != nil {
			log.FromContext(c.Request.Context()).Warn("snapshot download interrupted", "snapshot_id", snap.ID, "err", err)
		}
	})
}

func downloadName(s *repo.Snapshot) string {
	title := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '-'
	}, s.BaseTitle)
	if strings.Trim(title, "-") == "" {
		title = s.BaseID
	}
	return fmt.Sprintf("nocodb-%s-%s.json.gz", title, s.CreatedAt.UTC().Format("20060102-150405"))
}
