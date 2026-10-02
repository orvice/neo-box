package main

import (
	"context"
	"log/slog"
	"os"

	"butterfly.orx.me/core"
	"butterfly.orx.me/core/app"

	neoboxapp "go.orx.me/apps/neo-box/internal/app"
	appconfig "go.orx.me/apps/neo-box/internal/config"
)

const (
	serviceName      = "neobox"
	defaultNamespace = "apps"
)

// namespace is the Butterfly namespace; with the service name it forms the
// config key (<namespace>/neobox) read from Consul.
func namespace() string {
	if ns := os.Getenv("BUTTERFLY_NAMESPACE"); ns != "" {
		return ns
	}
	return defaultNamespace
}

func main() {
	cfg := new(appconfig.AppConfig)
	router, handlers := neoboxapp.SetupRoutes(cfg)

	ns := namespace()
	svc := core.New(&app.Config{
		Namespace: ns,
		Service:   serviceName,
		Config:    cfg,
		Router:    router,
		InitFunc: []func() error{
			func() error {
				return handlers.Bootstrap(context.Background())
			},
		},
		TeardownFunc: []func() error{
			handlers.Shutdown,
		},
	})

	slog.Info("starting butterfly service", "service", serviceName, "namespace", ns, "commit", serverBuildCommit())
	svc.Run()
}
