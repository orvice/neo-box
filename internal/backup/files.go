package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/nocodb"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/snapshot"
)

// Attachment files are stored once per user under their sha256 and shared
// by every snapshot that uses them. A snapshot records each file it uses
// as it goes; deleting a snapshot (or failing one) drops those records,
// and files no snapshot uses are then deleted. Registering a file holds
// filesMu shared and collecting unused files holds it exclusively, so a
// file can't be collected between a running snapshot finding it stored
// and recording that it uses it. This relies on there being one neo-box
// process (ADR 0001).

// FileKey is where an attachment file's content is stored.
func FileKey(userID, sha256 string) string {
	return fmt.Sprintf("nocodb/%s/files/%s", userID, sha256)
}

// fileStore stores the attachment files of one snapshot run.
type fileStore struct {
	m    *Manager
	api  NocoDB
	snap *repo.Snapshot
}

var _ snapshot.Files = (*fileStore)(nil)

// Store skips the download when the connection's last copy of this
// attachment is still stored; otherwise it downloads the file, hashing it
// on the way, and stores it unless the content is already there.
func (s *fileStore) Store(ctx context.Context, att nocodb.Attachment) (string, error) {
	source := att.Source()
	if sha, ok, err := s.m.repo.FileSource(ctx, s.snap.ConnectionID, source, att.Size); err != nil {
		return "", err
	} else if ok {
		reused, err := s.register(ctx, sha, 0, nil)
		if err != nil {
			return "", err
		}
		if reused {
			return sha, nil
		}
	}

	tmp, sha, size, err := s.download(ctx, att)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := s.register(ctx, sha, size, tmp); err != nil {
		return "", err
	}
	if err := s.m.repo.PutFileSource(ctx, s.snap.ConnectionID, source, att.Size, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// register records that the snapshot uses the file sha. With content nil
// it only succeeds (true) when the file is already stored; otherwise it
// stores content first if needed.
func (s *fileStore) register(ctx context.Context, sha string, size int64, content io.ReadSeeker) (bool, error) {
	s.m.filesMu.RLock()
	defer s.m.filesMu.RUnlock()
	userID := s.snap.UserID
	exists, err := s.m.repo.FileExists(ctx, userID, sha)
	if err != nil {
		return false, err
	}
	if !exists {
		if content == nil {
			return false, nil
		}
		if err := s.m.blobs.Put(ctx, FileKey(userID, sha), content, size, "application/octet-stream"); err != nil {
			return false, fmt.Errorf("store file: %w", err)
		}
		if err := s.m.repo.CreateFile(ctx, &repo.File{UserID: userID, SHA256: sha, Size: size, CreatedAt: s.m.now().UTC()}); err != nil {
			return false, err
		}
	}
	if err := s.m.repo.AddSnapshotFile(ctx, s.snap.ID, userID, sha); err != nil {
		return false, err
	}
	return true, nil
}

// download reads the attachment from the first reference that works into
// a temp file, positioned at its start. A file no reference can serve is
// unavailable, which the snapshot records and moves past.
func (s *fileStore) download(ctx context.Context, att nocodb.Attachment) (*os.File, string, int64, error) {
	var errs []string
	for _, ref := range att.DownloadRefs() {
		tmp, sha, size, err := s.downloadRef(ctx, ref)
		if err == nil {
			return tmp, sha, size, nil
		}
		if ctx.Err() != nil {
			return nil, "", 0, ctx.Err()
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		errs = append(errs, "the attachment has no url or path")
	}
	return nil, "", 0, fmt.Errorf("%w: %s", snapshot.ErrFileUnavailable, strings.Join(errs, "; "))
}

func (s *fileStore) downloadRef(ctx context.Context, ref string) (*os.File, string, int64, error) {
	body, err := s.api.Download(ctx, ref)
	if err != nil {
		return nil, "", 0, err
	}
	defer body.Close()
	tmp, err := os.CreateTemp("", "neobox-file-*")
	if err != nil {
		return nil, "", 0, err
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, "", 0, err
	}
	return tmp, hex.EncodeToString(h.Sum(nil)), size, nil
}

// releaseFiles forgets the files a snapshot uses (for one that failed) and
// collects those no snapshot uses any more.
func (m *Manager) releaseFiles(ctx context.Context, snap *repo.Snapshot) {
	if err := m.repo.DeleteSnapshotFiles(ctx, snap.ID); err != nil {
		log.FromContext(ctx).Warn("forget snapshot files failed", "snapshot_id", snap.ID, "err", err)
		return
	}
	m.collectFiles(ctx, snap.UserID)
}

// collectFiles deletes the user's files that no snapshot uses. Failures
// are logged; the next collection retries them.
func (m *Manager) collectFiles(ctx context.Context, userID string) {
	m.filesMu.Lock()
	defer m.filesMu.Unlock()
	logger := log.FromContext(ctx)
	unused, err := m.repo.ListUnusedFiles(ctx, userID)
	if err != nil {
		logger.Warn("list unused files failed", "user_id", userID, "err", err)
		return
	}
	for _, sha := range unused {
		if err := m.blobs.Delete(ctx, FileKey(userID, sha)); err != nil {
			logger.Warn("delete file failed", "user_id", userID, "sha256", sha, "err", err)
			continue
		}
		if err := m.repo.DeleteFile(ctx, userID, sha); err != nil {
			logger.Warn("delete file record failed", "user_id", userID, "sha256", sha, "err", err)
		}
	}
}

// openFile opens one of the user's stored files for a restore.
func (m *Manager) openFile(ctx context.Context, userID, sha string) (io.ReadCloser, error) {
	rc, err := m.blobs.Get(ctx, FileKey(userID, sha))
	if errors.Is(err, blobstore.ErrNotFound) {
		return nil, fmt.Errorf("%w: not in storage", snapshot.ErrFileUnavailable)
	}
	return rc, err
}

// includeAttachments reports whether snapshots of the Base store
// attachment files: its policy says so, and a Base without a policy does.
func (m *Manager) includeAttachments(ctx context.Context, snap *repo.Snapshot) (bool, error) {
	policies, err := m.repo.ListPolicies(ctx, snap.UserID, snap.ConnectionID)
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.BaseID == snap.BaseID {
			return p.IncludeAttachments, nil
		}
	}
	return true, nil
}
