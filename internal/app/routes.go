package app

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/backup"
	"go.orx.me/apps/neo-box/internal/config"
	httpHandler "go.orx.me/apps/neo-box/internal/handler/http"
	"go.orx.me/apps/neo-box/internal/notify"
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
	cloudflareSvcServer *application.CloudflareServiceServer
	workspaceSvcServer  *application.GoogleWorkspaceServiceServer
	notifySvcServer     *application.NotificationServiceServer
	authRepo            atomic.Value // auth.Repository
	snapshots           atomic.Value // *snapshotContent

	stop     context.CancelFunc
	manager  *backup.Manager
	wasabi   *wasabisync.Manager
	notifier *notify.Service
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
	// Session TTL is read from cfg in Bootstrap, after YAML is loaded.
	authSvcServer := application.NewAuthServiceServer(nil, 0)
	connectionSvcServer := application.NewConnectionServiceServer()
	nocodbSvcServer := application.NewNocoDBServiceServer()
	wasabiSvcServer := application.NewWasabiServiceServer()
	cloudflareSvcServer := application.NewCloudflareServiceServer()
	workspaceSvcServer := application.NewGoogleWorkspaceServiceServer()
	notifySvcServer := application.NewNotificationServiceServer()

	// One Connect server serves every service: HTTP options (codecs, read
	// limits) are applied once when the server is mounted on a mux.
	server := connect.NewServer()
	neoboxv1connect.RegisterAuthServiceHandler(server, authSvcServer)
	neoboxv1connect.RegisterConnectionServiceHandler(server, connectionSvcServer)
	neoboxv1connect.RegisterNocoDBServiceHandler(server, nocodbSvcServer)
	neoboxv1connect.RegisterWasabiServiceHandler(server, wasabiSvcServer)
	neoboxv1connect.RegisterCloudflareServiceHandler(server, cloudflareSvcServer)
	neoboxv1connect.RegisterGoogleWorkspaceServiceHandler(server, workspaceSvcServer)
	neoboxv1connect.RegisterNotificationServiceHandler(server, notifySvcServer)

	// RPCs keep their /api/neobox.v1.<Service>/<Method> paths: Gin matches the
	// service prefix, strips /api, and the inner mux dispatches the procedure.
	apiMux := http.NewServeMux()
	connecthttp.Mount(apiMux, server, connectx.HandlerOptions()...)
	apiHandler := http.StripPrefix("/api", apiMux)
	servicePrefixes := []string{
		neoboxv1connect.AuthServiceName,
		neoboxv1connect.ConnectionServiceName,
		neoboxv1connect.NocoDBServiceName,
		neoboxv1connect.WasabiServiceName,
		neoboxv1connect.CloudflareServiceName,
		neoboxv1connect.GoogleWorkspaceServiceName,
		neoboxv1connect.NotificationServiceName,
	}

	handlers := &Handlers{
		cfg:                 cfg,
		workspaceSvcServer:  workspaceSvcServer,
		authSvcServer:       authSvcServer,
		connectionSvcServer: connectionSvcServer,
		nocodbSvcServer:     nocodbSvcServer,
		wasabiSvcServer:     wasabiSvcServer,
		cloudflareSvcServer: cloudflareSvcServer,
		notifySvcServer:     notifySvcServer,
	}

	router := func(r *gin.Engine) {
		r.Use(httpHandler.AuthMiddleware(cfg, handlers.authRepoFromHolder))
		httpHandler.RegisterHealth(r)
		httpHandler.RegisterSnapshotDownload(r, handlers.snapshotContentFromHolder)

		// Only the RPC service prefixes go to the Connect mux; other /api
		// routes (health, snapshot download) keep their own Gin handlers.
		for _, name := range servicePrefixes {
			r.Any("/api/"+name+"/*path", gin.WrapH(apiHandler))
		}
	}

	return router, handlers
}
