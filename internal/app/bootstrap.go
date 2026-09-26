package app

import (
	"context"
	"time"

	"butterfly.orx.me/core/log"
	"butterfly.orx.me/core/store/s3"
	"butterfly.orx.me/core/store/sqldb"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/jackc/pgx/v5/stdlib"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/auth/provider"
	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/connection"
	"go.orx.me/apps/neo-box/internal/ent"
	authpg "go.orx.me/apps/neo-box/internal/repo/auth/postgres"
	connectionpg "go.orx.me/apps/neo-box/internal/repo/connection/postgres"
	nocodbpg "go.orx.me/apps/neo-box/internal/repo/nocodb/postgres"
	oauthstatepg "go.orx.me/apps/neo-box/internal/repo/oauthstate/postgres"
	wasabipg "go.orx.me/apps/neo-box/internal/repo/wasabi/postgres"
	"go.orx.me/apps/neo-box/internal/secretbox"
	"go.orx.me/apps/neo-box/internal/wasabisync"
)

// Bootstrap connects storage, migrates the schema, seeds the initial admin,
// and wires repositories into the handlers built by SetupRoutes.
func (h *Handlers) Bootstrap(ctx context.Context) error {
	cfg := h.cfg
	client, err := openPostgres(ctx, cfg)
	if err != nil {
		return err
	}

	authRepo := authpg.New(client)
	if err := application.BootstrapInitialAdmin(ctx, authRepo, cfg.Auth); err != nil {
		return err
	}
	stateRepo := oauthstatepg.New(client)

	h.authSvcServer.SetSessionTTL(cfg.Auth.EffectiveSessionTTL())
	h.authSvcServer.SetRepo(authRepo)
	h.authSvcServer.SetOAuthStateRepo(stateRepo)
	h.authSvcServer.SetProviderRegistry(provider.BuildRegistry(cfg.Auth))
	h.authRepo.Store(authRepo)

	cipher, err := setupCipher(ctx, cfg.Crypto)
	if err != nil {
		return err
	}
	conns := connection.NewService(connectionpg.New(client), cipher)
	h.connectionSvcServer.SetService(conns)

	runCtx, stop := context.WithCancel(context.Background())
	if err := h.bootstrapNocoDB(ctx, runCtx, client, conns); err != nil {
		stop()
		return err
	}
	if err := h.bootstrapWasabi(runCtx, client, conns); err != nil {
		stop()
		return err
	}
	h.stop = stop
	go purgeExpired(runCtx, authRepo, stateRepo)
	return nil
}

// setupCipher returns nil when no key is configured: connections then
// cannot be created or used.
func setupCipher(ctx context.Context, cfg config.CryptoConfig) (*secretbox.Cipher, error) {
	if cfg.EncryptionKey == "" {
		log.FromContext(ctx).Warn("crypto.encryption_key is not set; connections cannot be created")
		return nil, nil
	}
	return secretbox.NewCipher(cfg.EncryptionKey)
}

func (h *Handlers) bootstrapNocoDB(ctx, runCtx context.Context, client *ent.Client, conns *connection.Service) error {
	cfg := h.cfg

	blobs, err := setupBlobStore(ctx, cfg.Storage)
	if err != nil {
		return err
	}

	nocodbRepo := nocodbpg.New(client)
	manager := backup.New(backup.Config{
		Workers:           cfg.NocoDB.Workers,
		RequestsPerSecond: cfg.NocoDB.RequestsPerSecond,
		PageSize:          cfg.NocoDB.PageSize,
		SnapshotTimeout:   cfg.NocoDB.SnapshotTimeout,
	}, nocodbRepo, conns, blobs)
	conns.Register(manager.ConnectionProvider())

	if err := manager.Start(runCtx); err != nil {
		return err
	}
	h.manager = manager

	h.nocodbSvcServer.SetDeps(nocodbRepo, manager, conns)
	h.snapshots.Store(&snapshotContent{repo: nocodbRepo, manager: manager})
	return nil
}

// purgeExpired deletes expired sessions and OAuth states every hour until ctx
// is done. Lookups already ignore expired rows; this only keeps tables small.
func purgeExpired(ctx context.Context, sessions *authpg.Store, states *oauthstatepg.Store) {
	logger := log.FromContext(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		now := time.Now().UTC()
		if n, err := sessions.DeleteExpiredSessions(ctx, now); err != nil {
			logger.Warn("purge expired auth sessions failed", "err", err)
		} else if n > 0 {
			logger.Info("purged expired auth sessions", "count", n)
		}
		if n, err := states.DeleteExpired(ctx, now); err != nil {
			logger.Warn("purge expired oauth states failed", "err", err)
		} else if n > 0 {
			logger.Info("purged expired oauth states", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handlers) bootstrapWasabi(runCtx context.Context, client *ent.Client, conns *connection.Service) error {
	repo := wasabipg.New(client)
	manager := wasabisync.New(wasabisync.Config{Endpoint: h.cfg.Wasabi.StatsEndpoint}, repo, conns)
	conns.Register(manager.ConnectionProvider())
	if err := manager.Start(runCtx); err != nil {
		return err
	}
	h.wasabi = manager
	h.wasabiSvcServer.SetDeps(repo, manager, conns)
	return nil
}

// Shutdown stops the backup and Wasabi schedulers and workers. In-flight
// snapshots are cancelled and recorded as failed (or on next startup); an
// interrupted Wasabi sync resumes on the next start.
func (h *Handlers) Shutdown() error {
	if h.stop == nil {
		return nil
	}
	h.stop()
	done := make(chan struct{})
	go func() {
		h.manager.Wait()
		h.wasabi.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	return nil
}

func setupBlobStore(ctx context.Context, cfg config.StorageConfig) (blobstore.Store, error) {
	logger := log.FromContext(ctx)
	if cfg.S3Store != "" {
		client, bucket := s3.GetClient(cfg.S3Store), s3.GetBucket(cfg.S3Store)
		if client == nil || bucket == "" {
			logger.Error("storage.s3_store is set but no store.s3 client is registered under it", "store_key", cfg.S3Store)
			return nil, errS3NotRegistered(cfg.S3Store)
		}
		logger.Info("blob storage: s3", "store_key", cfg.S3Store, "bucket", bucket, "key_prefix", cfg.KeyPrefix)
		return blobstore.NewS3(client, bucket, cfg.KeyPrefix), nil
	}
	dir := cfg.EffectiveLocalDir()
	logger.Warn("blob storage: local directory (development only; set storage.s3_store for production)", "dir", dir)
	return blobstore.NewFS(dir)
}

type errS3NotRegistered string

func (e errS3NotRegistered) Error() string {
	return "storage.s3_store " + string(e) + " is not registered under store.s3"
}

type errDBNotRegistered string

func (e errDBNotRegistered) Error() string {
	return "db_store " + string(e) + " is not registered under store.db"
}

type errDBNotPostgres string

func (e errDBNotPostgres) Error() string {
	return "store.db." + string(e) + " must use driver: postgres"
}

// openPostgres wraps the butterfly store.db connection named by db_store in
// an ent client and migrates the schema.
func openPostgres(ctx context.Context, cfg *config.AppConfig) (*ent.Client, error) {
	logger := log.FromContext(ctx)

	key := cfg.EffectiveDBStore()
	db := sqldb.GetDB(key)
	if db == nil {
		logger.Error("db_store is not registered under store.db", "store_key", key)
		return nil, errDBNotRegistered(key)
	}
	if _, ok := db.Driver().(*stdlib.Driver); !ok {
		logger.Error("store.db entry is not a postgres connection", "store_key", key)
		return nil, errDBNotPostgres(key)
	}
	if err := db.PingContext(ctx); err != nil {
		logger.Error("failed to ping postgres", "store_key", key, "err", err)
		return nil, err
	}

	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if err := client.Schema.Create(ctx); err != nil {
		logger.Error("failed to migrate postgres schema", "store_key", key, "err", err)
		return nil, err
	}
	logger.Info("postgres connected", "store_key", key)
	return client, nil
}
