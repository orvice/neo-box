package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/google/uuid"

	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/notify"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/snapshot"
)

// EnqueueRestore records a pending restore of snap into a new Base titled
// title on the target connection, and queues it.
func (m *Manager) EnqueueRestore(ctx context.Context, snap *repo.Snapshot, target *connrepo.Connection, title string) (*repo.Restore, error) {
	if snap.Status != repo.StatusSucceeded {
		return nil, ErrNotRestorable
	}
	r := &repo.Restore{
		ID:                 uuid.NewString(),
		UserID:             snap.UserID,
		SnapshotID:         snap.ID,
		SourceConnectionID: snap.ConnectionID,
		SourceBaseID:       snap.BaseID,
		SourceBaseTitle:    snap.BaseTitle,
		TargetConnectionID: target.ID,
		TargetBaseTitle:    title,
		Status:             repo.RestorePending,
		CreatedAt:          m.now().UTC(),
	}
	if err := m.repo.CreateRestore(ctx, r); err != nil {
		return nil, err
	}
	select {
	case m.restoreQueue <- r.ID:
		return r, nil
	default:
		r.Status = repo.RestoreFailed
		r.Error = "restore queue is full"
		r.FinishedAt = m.now().UTC()
		_ = m.repo.UpdateRestore(ctx, r)
		return nil, ErrQueueFull
	}
}

func (m *Manager) executeRestore(parent context.Context, restoreID string) {
	logger := log.FromContext(parent)
	r, err := m.repo.GetRestore(parent, "", restoreID)
	if err != nil {
		logger.Error("restore job lookup failed", "restore_id", restoreID, "err", err)
		return
	}

	ctx, cancel := context.WithTimeout(parent, m.cfg.RestoreTimeout)
	defer cancel()

	r.Status = repo.RestoreRunning
	r.StartedAt = m.now().UTC()
	r.Progress = "starting"
	if err := m.repo.UpdateRestore(ctx, r); err != nil {
		logger.Error("restore start update failed", "restore_id", r.ID, "err", err)
		return
	}

	report, runErr := m.restore(ctx, r)
	// Persist the terminal state even if the run context expired.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer finishCancel()
	// The target's token was used for every write; a 401/403 says it's
	// broken, other failures say nothing about the connection.
	if runErr == nil || nocodb.IsAuthError(runErr) {
		m.conns.RecordStatus(finishCtx, r.TargetConnectionID, runErr)
	}
	if report != nil {
		if report.BaseID != "" {
			r.TargetBaseID = report.BaseID
		}
		r.TableCount = len(report.Tables)
		r.RecordCount = report.RecordCount
		r.LinkCount = report.LinkCount
		r.FileCount = report.FileCount
		r.ViewCount = report.ViewCount
		r.Warnings = make([]repo.RestoreWarning, len(report.Warnings))
		for i, w := range report.Warnings {
			r.Warnings[i] = repo.RestoreWarning(w)
		}
	}
	r.FinishedAt = m.now().UTC()
	r.Progress = ""
	if runErr != nil {
		r.Status = repo.RestoreFailed
		r.Error = runErr.Error()
		logger.Warn("restore failed", "restore_id", r.ID, "snapshot_id", r.SnapshotID, "base_id", r.TargetBaseID, "err", runErr)
	} else {
		r.Status = repo.RestoreSucceeded
		logger.Info("restore succeeded", "restore_id", r.ID, "snapshot_id", r.SnapshotID, "base_id", r.TargetBaseID,
			"records", r.RecordCount, "links", r.LinkCount, "files", r.FileCount, "views", r.ViewCount, "warnings", len(r.Warnings))
	}
	if err := m.repo.UpdateRestore(finishCtx, r); err != nil {
		logger.Error("restore finish update failed", "restore_id", r.ID, "err", err)
	}
	if runErr != nil {
		title := r.SourceBaseTitle
		if title == "" {
			title = r.SourceBaseID
		}
		body := r.Error
		if r.TargetBaseID != "" {
			body += fmt.Sprintf("\nThe partial Base %s (%q) is left in NocoDB.", r.TargetBaseID, r.TargetBaseTitle)
		}
		m.notify(finishCtx, notify.Alert{
			UserID: r.UserID, Source: "nocodb", ConnectionID: r.TargetConnectionID, Kind: "restore_failed",
			Key:      "nocodb:restore_failed:" + r.ID,
			Severity: notify.Warning,
			Title:    fmt.Sprintf("Restore of %q failed", title),
			Body:     body,
			Link:     fmt.Sprintf("/connections/%s/snapshots/%s", r.SourceConnectionID, r.SnapshotID),
		})
	}
}

func (m *Manager) restore(ctx context.Context, r *repo.Restore) (*snapshot.RestoreReport, error) {
	snap, err := m.repo.GetSnapshot(ctx, "", r.SnapshotID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, errors.New("the snapshot was deleted")
		}
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	conn, err := m.conns.GetByID(ctx, r.TargetConnectionID)
	if err != nil {
		return nil, fmt.Errorf("load connection: %w", err)
	}
	api, _, err := m.open(conn)
	if err != nil {
		return nil, err
	}

	var (
		progressMu   sync.Mutex
		lastProgress time.Time
	)
	return snapshot.Restore(ctx, api, func() (io.ReadCloser, error) {
		return m.OpenContent(ctx, snap)
	}, snapshot.RestoreOptions{
		Title: r.TargetBaseTitle,
		// Throttled because the UI only polls every few seconds.
		Progress: func(msg string) {
			progressMu.Lock()
			if time.Since(lastProgress) < time.Second {
				progressMu.Unlock()
				return
			}
			lastProgress = time.Now()
			progressMu.Unlock()
			_ = m.repo.UpdateRestoreProgress(ctx, r.ID, msg)
		},
		OpenFile: func(ctx context.Context, sha string) (io.ReadCloser, error) {
			return m.openFile(ctx, snap.UserID, sha)
		},
		// Recorded at once, so a restore that fails later still names the
		// partial Base it left behind.
		OnBaseCreated: func(baseID string) {
			r.TargetBaseID = baseID
			if err := m.repo.UpdateRestoreTargetBase(ctx, r.ID, baseID); err != nil {
				log.FromContext(ctx).Warn("restore base id update failed", "restore_id", r.ID, "err", err)
			}
		},
	})
}
