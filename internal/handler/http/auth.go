package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

// unauthenticatedFallbackWarn ensures the loud "no auth wired" warning is
// emitted at most once per process.
var unauthenticatedFallbackWarn sync.Once

const (
	bearerPrefix        = "Bearer "
	defaultTouchTimeout = 2 * time.Second
)

// AuthRepoProvider returns the auth repository once bootstrap has wired it,
// or nil before that.
type AuthRepoProvider func() auth.Repository

// AuthMiddleware authenticates every non-public request against the
// Mongo-backed session store and attaches the user and session to the
// request context for ConnectRPC services.
func AuthMiddleware(cfg *config.AppConfig, authProvider AuthRepoProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		applyCORSHeaders(c)
		// CORS preflight carries no credentials, so answer it before auth;
		// the actual request is still authenticated below.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		if isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		var authRepo auth.Repository
		if authProvider != nil {
			authRepo = authProvider()
		}

		if authRepo == nil {
			if !cfg.Auth.AllowUnauthenticated {
				log.FromContext(c.Request.Context()).Warn(
					"auth not ready and allow_unauthenticated=false; rejecting request",
					"path", c.Request.URL.Path,
				)
				unauthorized(c)
				return
			}
			unauthenticatedFallbackWarn.Do(func() {
				log.FromContext(c.Request.Context()).Warn(
					"AUTH DISABLED: auth.allow_unauthenticated is true and no auth is wired; every request is granted admin. Do not run this configuration in production.",
				)
			})
			c.Request = c.Request.WithContext(auth.WithAdmin(c.Request.Context()))
			c.Next()
			return
		}

		token, ok := bearerToken(c)
		if !ok {
			unauthorized(c)
			return
		}

		session, user, err := authRepo.LookupSession(c.Request.Context(), hashSecret(token), time.Now().UTC())
		if err != nil {
			if !errors.Is(err, auth.ErrSessionNotFound) && !errors.Is(err, auth.ErrUserNotFound) && !errors.Is(err, auth.ErrUserDisabled) {
				log.FromContext(c.Request.Context()).Warn("auth session lookup failed", "err", err)
			}
			unauthorized(c)
			return
		}
		go touchSession(authRepo, session.ID)
		ctx := auth.WithAuthenticated(c.Request.Context(), user, session)
		if user.GetRole() == "admin" {
			ctx = auth.WithAdmin(ctx)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func applyCORSHeaders(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return
	}
	header := c.Writer.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Connect-Protocol-Version")
	header.Set("Access-Control-Expose-Headers", "Content-Type, Connect-Error-Code, Connect-Error-Message")
}

const authServicePrefix = "/api/" + neoboxv1connect.AuthServiceName + "/"

func isPublicPath(path string) bool {
	switch path {
	case "/ping",
		authServicePrefix + "Login",
		authServicePrefix + "ListOAuthProviders",
		authServicePrefix + "BeginOAuthFlow",
		authServicePrefix + "CompleteOAuthFlow":
		return true
	}
	return false
}

func bearerToken(c *gin.Context) (string, bool) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
	return token, token != ""
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func touchSession(repo auth.Repository, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTouchTimeout)
	defer cancel()
	_ = repo.TouchSession(ctx, id, time.Now().UTC())
}

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}
