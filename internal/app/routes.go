package app

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/config"
	httpHandler "go.orx.me/apps/neo-box/internal/handler/http"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	nocodbrepo "go.orx.me/apps/neo-box/internal/repo/nocodb"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	"go.orx.me/apps/neo-box/internal/wasabisync"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

// Handlers holds the RPC services that need post-bootstrap wiring. Routes are
// registered before Butterfly loads the YAML config and before PostgreSQL is
// connected, so repositories are attached later by Bootstrap.
type Handlers struct {
	cfg                 *config.AppConfig
	authSvcServer       *application.AuthServiceServer
	connectionSvcServer *application.ConnectionServiceServer
	nocodbSvcServer     *application.NocoDBServiceServer
	wasabiSvcServer     *application.WasabiServiceServer
	authRepo            atomic.Value // auth.Repository
	snapshots           atomic.Value // *snapshotContent

	stop    context.CancelFunc
	manager *backup.Manager
	wasabi  *wasabisync.Manager
}

func (h *Handlers) authRepoFromHolder() auth.Repository {
	repo, _ := h.authRepo.Load().(auth.Repository)
	return repo
}

func (h *Handlers) snapshotContentFromHolder() httpHandler.SnapshotContent {
	src, _ := h.snapshots.Load().(*snapshotContent)
	if src == nil {
		return nil
	}
	return src
}

// snapshotContent adapts the repo + manager to the download handler.
type snapshotContent struct {
	repo    nocodbrepo.Repository
	manager *backup.Manager
}

func (s *snapshotContent) GetSnapshot(ctx *gin.Context, userID, id string) (*nocodbrepo.Snapshot, error) {
	return s.repo.GetSnapshot(ctx.Request.Context(), userID, id)
}

func (s *snapshotContent) OpenContent(ctx *gin.Context, snap *nocodbrepo.Snapshot) (io.ReadCloser, error) {
	return s.manager.OpenContent(ctx.Request.Context(), snap)
}

// SetupRoutes builds the Gin router and the handler set that Bootstrap wires.
func SetupRoutes(cfg *config.AppConfig) (func(r *gin.Engine), *Handlers) {
	// Every Connect handler shares the same option set (snake_case JSON).
	connectOpts := connectx.HandlerOptions()

	// Session TTL is read from cfg in Bootstrap, after YAML is loaded.
	authSvcServer := application.NewAuthServiceServer(nil, 0)
	authConnectPath, authConnectHandler := neoboxv1connect.NewAuthServiceHandler(authSvcServer, connectOpts...)
	connectionSvcServer := application.NewConnectionServiceServer()
	connectionConnectPath, connectionConnectHandler := neoboxv1connect.NewConnectionServiceHandler(connectionSvcServer, connectOpts...)
	nocodbSvcServer := application.NewNocoDBServiceServer()
	nocodbConnectPath, nocodbConnectHandler := neoboxv1connect.NewNocoDBServiceHandler(nocodbSvcServer, connectOpts...)
	wasabiSvcServer := application.NewWasabiServiceServer()
	wasabiConnectPath, wasabiConnectHandler := neoboxv1connect.NewWasabiServiceHandler(wasabiSvcServer, connectOpts...)

	handlers := &Handlers{
		cfg:                 cfg,
		authSvcServer:       authSvcServer,
		connectionSvcServer: connectionSvcServer,
		nocodbSvcServer:     nocodbSvcServer,
		wasabiSvcServer:     wasabiSvcServer,
	}

	router := func(r *gin.Engine) {
		r.Use(httpHandler.AuthMiddleware(cfg, handlers.authRepoFromHolder))
		httpHandler.RegisterHealth(r)
		httpHandler.RegisterSnapshotDownload(r, handlers.snapshotContentFromHolder)

		r.Any("/api"+authConnectPath+"*path", gin.WrapH(http.StripPrefix("/api", authConnectHandler)))
		r.Any("/api"+connectionConnectPath+"*path", gin.WrapH(http.StripPrefix("/api", connectionConnectHandler)))
		r.Any("/api"+nocodbConnectPath+"*path", gin.WrapH(http.StripPrefix("/api", nocodbConnectHandler)))
		r.Any("/api"+wasabiConnectPath+"*path", gin.WrapH(http.StripPrefix("/api", wasabiConnectHandler)))
	}

	return router, handlers
}
