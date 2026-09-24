package app

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/config"
	httpHandler "go.orx.me/apps/neo-box/internal/handler/http"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

// Handlers holds the RPC services that need post-bootstrap wiring. Routes are
// registered before Butterfly loads the YAML config and before MongoDB is
// connected, so repositories are attached later by Bootstrap.
type Handlers struct {
	cfg           *config.AppConfig
	authSvcServer *application.AuthServiceServer
	authRepo      atomic.Value // auth.Repository
}

func (h *Handlers) authRepoFromHolder() auth.Repository {
	repo, _ := h.authRepo.Load().(auth.Repository)
	return repo
}

// SetupRoutes builds the Gin router and the handler set that Bootstrap wires.
func SetupRoutes(cfg *config.AppConfig) (func(r *gin.Engine), *Handlers) {
	// Every Connect handler shares the same option set (snake_case JSON).
	connectOpts := connectx.HandlerOptions()

	// Session TTL is read from cfg in Bootstrap, after YAML is loaded.
	authSvcServer := application.NewAuthServiceServer(nil, 0)
	authConnectPath, authConnectHandler := neoboxv1connect.NewAuthServiceHandler(authSvcServer, connectOpts...)

	handlers := &Handlers{
		cfg:           cfg,
		authSvcServer: authSvcServer,
	}

	router := func(r *gin.Engine) {
		r.Use(httpHandler.AuthMiddleware(cfg, handlers.authRepoFromHolder))
		httpHandler.RegisterHealth(r)

		r.Any("/api"+authConnectPath+"*path", gin.WrapH(http.StripPrefix("/api", authConnectHandler)))
	}

	return router, handlers
}
