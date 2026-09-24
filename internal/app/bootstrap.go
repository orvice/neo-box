package app

import (
	"context"

	"butterfly.orx.me/core/log"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/auth/provider"
	"go.orx.me/apps/neo-box/internal/config"
	authmongo "go.orx.me/apps/neo-box/internal/repo/auth/mongo"
	oauthstatemongo "go.orx.me/apps/neo-box/internal/repo/oauthstate/mongo"
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
	return nil
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
