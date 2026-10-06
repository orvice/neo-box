package wasabisync

import (
	"context"

	"butterfly.orx.me/core/log"
	"golang.org/x/sync/errgroup"

	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// bucketReadConcurrency bounds buckets read at once; each read is about ten
// S3 requests.
const bucketReadConcurrency = 4

// readBucketConfigs reads every bucket's settings and stores them. It runs
// after a successful usage sync and never fails it: a key without S3 read
// access only records why on the sync state.
func (m *Manager) readBucketConfigs(ctx context.Context, conn *connrepo.Connection) {
	logger := log.FromContext(ctx)
	st, err := m.repo.GetSyncState(ctx, conn.ID)
	if err != nil {
		logger.Warn("wasabi bucket settings: load sync state failed", "connection_id", conn.ID, "err", err)
		return
	}
	save := func() {
		st.ConfigFetchedAt, st.UpdatedAt = m.now().UTC(), m.now().UTC()
		if err := m.repo.SaveSyncState(ctx, st); err != nil {
			logger.Warn("wasabi bucket settings: save sync state failed", "connection_id", conn.ID, "err", err)
		}
	}
	ak, sk, err := m.keys(conn)
	if err != nil {
		st.ConfigError = err.Error()
		save()
		return
	}
	reader := m.newBucketReader(ak, sk)
	buckets, err := reader.ListBuckets(ctx)
	if err != nil {
		st.ConfigError = wasabi.ErrorReason(err)
		logger.Info("wasabi bucket settings: cannot list buckets", "connection_id", conn.ID, "reason", st.ConfigError)
		save()
		return
	}
	configs := make([]wasabi.BucketConfig, len(buckets))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(bucketReadConcurrency)
	for i, b := range buckets {
		g.Go(func() error {
			configs[i] = reader.Read(gctx, b)
			return nil
		})
	}
	_ = g.Wait()
	if err := m.repo.ReplaceBucketConfigs(ctx, conn.ID, configs); err != nil {
		logger.Warn("wasabi bucket settings: store failed", "connection_id", conn.ID, "err", err)
		return
	}
	st.ConfigError = ""
	save()
}

var _ BucketReader = (*wasabi.ConfigReader)(nil)
