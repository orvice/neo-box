package app

import (
	"context"
	"time"

	"butterfly.orx.me/core/log"
	"butterfly.orx.me/core/store/s3"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/auth/provider"
	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/blobstore"
	"go.orx.me/apps/neo-box/internal/config"
	authmongo "go.orx.me/apps/neo-box/internal/repo/auth/mongo"
	nocodbmongo "go.orx.me/apps/neo-box/internal/repo/nocodb/mongo"
	oauthstatemongo "go.orx.me/apps/neo-box/internal/repo/oauthstate/mongo"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

// Bootstrap connects storage, ensures indexes, seeds the initial admin, and
// wires repositories into the handlers built by SetupRoutes.
func (h *Handlers) Bootstrap(ctx context.Context) error {
	cfg := h.cfg
	db, err := connectMongo(ctx, cfg)
	if err != nil {
		return err
	}

	authRepo := authmongo.New(db)
	if err := application.BootstrapInitialAdmin(ctx, authRepo, cfg.Auth); err != nil {
		return err
	}
	stateRepo := oauthstatemongo.New(db)
	if err := stateRepo.EnsureIndexes(ctx); err != nil {
		return err
	}

	h.authSvcServer.SetSessionTTL(cfg.Auth.EffectiveSessionTTL())
	h.authSvcServer.SetRepo(authRepo)
	h.authSvcServer.SetOAuthStateRepo(stateRepo)
	h.authSvcServer.SetProviderRegistry(provider.BuildRegistry(cfg.Auth))
	h.authRepo.Store(authRepo)

	return h.bootstrapNocoDB(ctx, db)
}

func (h *Handlers) bootstrapNocoDB(ctx context.Context, db *mongo.Database) error {
	cfg := h.cfg
	logger := log.FromContext(ctx)

	var cipher *secretbox.Cipher
	if cfg.Crypto.EncryptionKey == "" {
		logger.Warn("crypto.encryption_key is not set; NocoDB connections cannot be created")
	} else {
		c, err := secretbox.NewCipher(cfg.Crypto.EncryptionKey)
		if err != nil {
			return err
		}
		cipher = c
	}

	blobs, err := setupBlobStore(ctx, cfg.Storage)
	if err != nil {
		return err
	}

	nocodbRepo := nocodbmongo.New(db)
	if err := nocodbRepo.EnsureIndexes(ctx); err != nil {
		return err
	}
	manager := backup.New(backup.Config{
		Workers:           cfg.NocoDB.Workers,
		RequestsPerSecond: cfg.NocoDB.RequestsPerSecond,
		PageSize:          cfg.NocoDB.PageSize,
		SnapshotTimeout:   cfg.NocoDB.SnapshotTimeout,
	}, nocodbRepo, blobs, cipher)

	runCtx, stop := context.WithCancel(context.Background())
	if err := manager.Start(runCtx); err != nil {
		stop()
		return err
	}
	h.stop = stop
	h.manager = manager

	h.nocodbSvcServer.SetDeps(nocodbRepo, manager, cipher)
	h.snapshots.Store(&snapshotContent{repo: nocodbRepo, manager: manager})
	return nil
}

// Shutdown stops the backup scheduler and workers. In-flight snapshots are
// cancelled and recorded as failed (or on next startup).
func (h *Handlers) Shutdown() error {
	if h.stop == nil {
		return nil
	}
	h.stop()
	done := make(chan struct{})
	go func() { h.manager.Wait(); close(done) }()
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

// connectMongo establishes a connection to MongoDB and returns the database handle.
func connectMongo(ctx context.Context, cfg *config.AppConfig) (*mongo.Database, error) {
	logger := log.FromContext(ctx)

	mongoURI := cfg.MongoURI
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	logger.Info("connecting to mongodb", "uri", mongoURI)

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		logger.Error("failed to connect to mongodb", "err", err)
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		logger.Error("failed to ping mongodb", "err", err)
		return nil, err
	}

	dbName := cfg.MongoDB
	if dbName == "" {
		dbName = "neobox"
	}
	logger.Info("mongodb connected", "database", dbName)

	return client.Database(dbName), nil
}
