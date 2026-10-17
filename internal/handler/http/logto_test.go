package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/auth/logto"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

type testBrowserAuth struct{}

func (s testBrowserAuth) IdentityForLogout(ctx context.Context, cookie string) (*logto.Identity, error) {
	return s.Authenticate(ctx, cookie)
}

func (testBrowserAuth) Origin() string            { return "https://dashboard.test" }
func (testBrowserAuth) SecureCookies() bool       { return true }
func (testBrowserAuth) SessionTTL() time.Duration { return time.Hour }
func (testBrowserAuth) Begin(context.Context, string, bool) (string, string, error) {
	return "flow-cookie", "https://identity.test/oidc/auth", nil
}
func (testBrowserAuth) Complete(context.Context, string, string, string) (string, string, error) {
	return "session-cookie", "/connections", nil
}
func (testBrowserAuth) Logout(context.Context, string) (string, error) {
	return "https://identity.test/oidc/session/end", nil
}
func (testBrowserAuth) Authenticate(_ context.Context, cookie string) (*logto.Identity, error) {
	if cookie == "database-down" {
		return nil, errors.New("database down")
	}
	if cookie != "session-cookie" {
		return nil, logto.ErrUnauthenticated
	}
	return &logto.Identity{User: &neoboxv1.User{Id: "personal", DisplayName: "Owner"}, CSRF: "csrf-secret"}, nil
}

func TestBrowserAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var service BrowserAuth
	provider := func() BrowserAuth { return service }
	r.Use(LogtoMiddleware(provider))
	RegisterLogto(r, provider)
	r.GET("/ping", func(c *gin.Context) { c.Status(200) })
	r.POST("/private", func(c *gin.Context) {
		user, ok := auth.UserFromContext(c.Request.Context())
		if !ok || user.Id != "personal" {
			t.Error("business owner missing")
		}
		c.Status(200)
	})
	request := func(method, path, cookie, csrf, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-neobox_session", Value: cookie})
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/ping", "", "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/auth/login", "", "", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	service = testBrowserAuth{}
	for _, test := range []struct {
		cookie, csrf, origin string
		status               int
	}{
		{"", "", "", 401}, {"legacy-session", "", "", 401}, {"session-cookie", "", "", 403},
		{"session-cookie", "bad", "", 403}, {"session-cookie", "csrf-secret", "https://evil.test", 403},
		{"session-cookie", "csrf-secret", "https://dashboard.test", 200}, {"database-down", "csrf-secret", "", 503},
	} {
		w := request("POST", "/private", test.cookie, test.csrf, test.origin)
		if w.Code != test.status {
			t.Fatalf("%+v: %d", test, w.Code)
		}
		if test.cookie == "database-down" && len(w.Result().Cookies()) > 0 {
			t.Fatal("cleared session on transient failure")
		}
	}
	w := request("GET", "/api/auth/session", "session-cookie", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "csrfToken") || strings.Contains(w.Body.String(), "session-cookie") {
		t.Fatal(w.Body.String())
	}
	w = request("OPTIONS", "/private", "", "", "https://dashboard.test")
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentialed preflight failed")
	}
	if w := request("OPTIONS", "/private", "", "", "https://evil.test"); w.Code != 403 {
		t.Fatal("foreign preflight allowed")
	}
	w = request("GET", "/api/auth/login", "", "", "")
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Domain != "" || cookie.Path != "/" {
		t.Fatal("unsafe cookie attributes")
	}
	req := httptest.NewRequest("POST", "/private", nil)
	req.Header.Set("Authorization", "Bearer a-real-looking-JWT")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("accepted Bearer authentication")
	}
}

func TestOnlyMeAuthRPCIsRegistered(t *testing.T) {
	var handler neoboxv1connect.AuthServiceHandler = &application.LogtoAuthServiceServer{}
	if _, err := handler.Login(context.Background(), connect.NewRequest(&neoboxv1.LoginRequest{})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("local login available")
	}
	if _, err := handler.CreateUser(context.Background(), connect.NewRequest(&neoboxv1.CreateUserRequest{})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("local user management available")
	}
	ctx := auth.WithAuthenticated(context.Background(), &neoboxv1.User{Id: "personal"}, nil)
	result, err := handler.Me(ctx, connect.NewRequest(&neoboxv1.MeRequest{}))
	if err != nil || result.Msg.User.Id != "personal" {
		t.Fatal(err)
	}
}
