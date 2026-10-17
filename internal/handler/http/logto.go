package http

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/auth/logto"
	"go.orx.me/apps/neo-box/internal/repo/auth"
)

type BrowserAuth interface {
	Authenticate(context.Context, string) (*logto.Identity, error)
	IdentityForLogout(context.Context, string) (*logto.Identity, error)
	Begin(context.Context, string, bool) (string, string, error)
	Complete(context.Context, string, string, string) (string, string, error)
	Logout(context.Context, string) (string, error)
	Origin() string
	SecureCookies() bool
	SessionTTL() time.Duration
}

type BrowserAuthProvider func() BrowserAuth

func cookieName(service BrowserAuth, flow bool) string {
	name := "neobox_session"
	if flow {
		name = "neobox_login"
	}
	if service.SecureCookies() {
		name = "__Host-" + name
	}
	return name
}

func authCookie(c *gin.Context, service BrowserAuth, flow bool, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if value == "" {
		maxAge = -1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName(service, flow), Value: value, Path: "/",
		HttpOnly: true, Secure: service.SecureCookies(), SameSite: http.SameSiteLaxMode,
		MaxAge: maxAge, Expires: time.Now().Add(ttl),
	})
}

const identityKey = "logto_identity"

func LogtoMiddleware(provider BrowserAuthProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/ping" {
			c.Next()
			return
		}
		service := provider()
		if service == nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "authentication is not ready"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		// The configured dashboard origin is authoritative. Never reflect an
		// arbitrary Origin when requests carry session credentials.
		origin := c.GetHeader("Origin")
		if origin != "" && origin != service.Origin() {
			c.AbortWithStatusJSON(403, gin.H{"error": "origin is not allowed"})
			return
		}
		if origin != "" {
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, X-CSRF-Token")
			c.Header("Access-Control-Expose-Headers", "Content-Type, Content-Disposition, Connect-Error-Code, Connect-Error-Message")
		}
		if c.Request.Method == http.MethodOptions {
			if origin == "" {
				c.AbortWithStatus(403)
				return
			}
			c.AbortWithStatus(204)
			return
		}
		path := c.Request.URL.Path
		if c.Request.Method == http.MethodGet && (path == "/api/auth/login" || path == "/api/auth/callback") {
			c.Next()
			return
		}
		cookie, _ := c.Cookie(cookieName(service, false))
		var identity *logto.Identity
		var err error
		logout := path == "/api/auth/logout" && c.Request.Method == http.MethodPost
		if logout {
			identity, err = service.IdentityForLogout(c.Request.Context(), cookie)
		} else {
			identity, err = service.Authenticate(c.Request.Context(), cookie)
		}
		if errors.Is(err, logto.ErrUnauthenticated) {
			authCookie(c, service, false, "", -time.Hour)
			if logout {
				c.Redirect(http.StatusSeeOther, service.Origin()+"/sign-in")
				c.Abort()
				return
			}
			unauthorized(c)
			return
		}
		if errors.Is(err, logto.ErrForbidden) {
			authCookie(c, service, false, "", -time.Hour)
			c.AbortWithStatusJSON(403, gin.H{"error": "this account does not have access to Neo Box"})
			return
		}
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "authentication is temporarily unavailable"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			csrf := c.GetHeader("X-CSRF-Token")
			if path == "/api/auth/logout" && c.Request.Method == http.MethodPost && csrf == "" {
				csrf = c.PostForm("csrf_token")
			}
			if identity.CSRF == "" || !hmac.Equal([]byte(csrf), []byte(identity.CSRF)) {
				c.AbortWithStatusJSON(403, gin.H{"error": "invalid CSRF token"})
				return
			}
		}
		c.Set(identityKey, identity)
		c.Request = c.Request.WithContext(auth.WithAuthenticated(c.Request.Context(), identity.User, nil))
		c.Next()
	}
}

func RegisterLogto(r *gin.Engine, provider BrowserAuthProvider) {
	r.GET("/api/auth/login", func(c *gin.Context) {
		service := provider()
		cookie, destination, err := service.Begin(c.Request.Context(), c.Query("redirect"), c.Query("reauth") == "1")
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "unable to begin sign-in"})
			return
		}
		authCookie(c, service, true, cookie, logto.FlowTTL)
		c.Redirect(http.StatusSeeOther, destination)
	})
	r.GET("/api/auth/callback", func(c *gin.Context) {
		service := provider()
		cookie, _ := c.Cookie(cookieName(service, true))
		authCookie(c, service, true, "", -time.Hour)
		session, returnPath, err := service.Complete(c.Request.Context(), cookie, c.Query("state"), c.Query("code"))
		if err != nil {
			reason := "login_failed"
			if errors.Is(err, logto.ErrForbidden) {
				reason = "forbidden"
			}
			c.Redirect(http.StatusSeeOther, service.Origin()+"/sign-in?error="+reason)
			return
		}
		authCookie(c, service, false, session, service.SessionTTL())
		c.Redirect(http.StatusSeeOther, service.Origin()+logto.ReturnPath(returnPath))
	})
	r.GET("/api/auth/session", func(c *gin.Context) {
		identity := c.MustGet(identityKey).(*logto.Identity)
		u := identity.User
		c.JSON(200, gin.H{"user": gin.H{
			"id": u.Id, "username": u.Username, "displayName": u.DisplayName,
			"avatarUrl": u.AvatarUrl, "role": u.Role,
		}, "csrfToken": identity.CSRF})
	})
	r.POST("/api/auth/logout", func(c *gin.Context) {
		service := provider()
		cookie, _ := c.Cookie(cookieName(service, false))
		destination, err := service.Logout(c.Request.Context(), cookie)
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "unable to sign out"})
			return
		}
		authCookie(c, service, false, "", -time.Hour)
		c.Redirect(http.StatusSeeOther, destination)
	})
}
