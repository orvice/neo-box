package main

import (
	"context"
	"log/slog"

	"butterfly.orx.me/core"
	"butterfly.orx.me/core/app"

	neoboxapp "go.orx.me/apps/neo-box/internal/app"
	appconfig "go.orx.me/apps/neo-box/internal/config"
)

const serviceName = "neobox"

func main() {
	cfg := new(appconfig.AppConfig)
	router, handlers := neoboxapp.SetupRoutes(cfg)

	svc := core.New(&app.Config{
		Namespace: "apps",
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

	slog.Info("starting butterfly service", "service", serviceName, "commit", serverBuildCommit())
	svc.Run()
}
