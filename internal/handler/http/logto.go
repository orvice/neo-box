package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/auth/logto"
	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type TokenAuthenticator interface {
	Authenticate(context.Context, string) (*neoboxv1.User, error)
}

func LogtoMiddleware(provider func() TokenAuthenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		applyCORSHeaders(c)
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		if c.Request.URL.Path == "/ping" || c.Request.URL.Path == "/api/auth/config" {
			c.Next()
			return
		}
		verifier := provider()
		if verifier == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication is not ready"})
			return
		}
		token, ok := bearerToken(c)
		if !ok {
			unauthorized(c)
			return
		}
		user, err := verifier.Authenticate(c.Request.Context(), token)
		if errors.Is(err, logto.ErrForbidden) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "this account does not have access to Neo Box"})
			return
		}
		if err != nil {
			unauthorized(c)
			return
		}
		c.Request = c.Request.WithContext(auth.WithAuthenticated(c.Request.Context(), user, nil))
		c.Next()
	}
}

// Only public SPA configuration is returned; owner IDs and migration settings
// never leave the backend. The SPA needs no client secret.
func RegisterLogtoConfig(r *gin.Engine, cfg *config.AppConfig) {
	r.GET("/api/auth/config", func(c *gin.Context) {
		if err := logto.ValidateConfig(cfg.Auth.Logto); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Logto is not configured"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"endpoint": cfg.Auth.Logto.Endpoint, "appId": cfg.Auth.Logto.AppID,
			"resource": cfg.Auth.Logto.Resource, "scopes": cfg.Auth.Logto.Scopes,
		})
	})
}
