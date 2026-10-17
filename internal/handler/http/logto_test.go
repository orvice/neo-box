package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"

	"go.orx.me/apps/neo-box/internal/application"
	"go.orx.me/apps/neo-box/internal/auth/logto"
	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
	"go.orx.me/apps/neo-box/pkg/proto/neobox/v1/neoboxv1connect"
)

type testAuthenticator struct{}

func (testAuthenticator) Authenticate(_ context.Context, token string) (*neoboxv1.User, error) {
	switch token {
	case "valid":
		return &neoboxv1.User{Id: "personal", ExternalId: "owner"}, nil
	case "other-user":
		return nil, logto.ErrForbidden
	default:
		return nil, errors.New("invalid token")
	}
}

func TestLogtoMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var verifier TokenAuthenticator
	r.Use(LogtoMiddleware(func() TokenAuthenticator { return verifier }))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/private", func(c *gin.Context) {
		user, ok := auth.UserFromContext(c.Request.Context())
		if !ok || user.Id != "personal" {
			t.Error("business owner missing")
		}
		if _, ok := auth.SessionFromContext(c.Request.Context()); ok {
			t.Error("created local session")
		}
		c.Status(http.StatusOK)
	})
	// Even the old public Login route must require a valid Logto token.
	r.POST(authServicePrefix+"Login", func(c *gin.Context) { c.Status(http.StatusNotImplemented) })
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if got := request("GET", "/private", "valid").Code; got != 503 {
		t.Fatalf("unready: %d", got)
	}
	if got := request("GET", "/ping", "").Code; got != 200 {
		t.Fatalf("health: %d", got)
	}
	if got := request("OPTIONS", "/private", "").Code; got != 204 {
		t.Fatalf("preflight: %d", got)
	}
	verifier = testAuthenticator{}
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {"legacy-session", 401}, {"other-user", 403}, {"valid", 200}} {
		if got := request("GET", "/private", test.token).Code; got != test.status {
			t.Fatalf("%q: %d", test.token, got)
		}
	}
	if got := request("POST", authServicePrefix+"Login", "").Code; got != 401 {
		t.Fatalf("legacy login exposed: %d", got)
	}
}

func TestPublicLogtoConfigContainsNoOwnerSettings(t *testing.T) {
	r := gin.New()
	r.Use(LogtoMiddleware(func() TokenAuthenticator { return nil }))
	RegisterLogtoConfig(r, &config.AppConfig{Auth: config.AuthConfig{Logto: config.LogtoConfig{
		Endpoint: "https://identity.test", AppID: "spa", Resource: "https://api.test", OwnerSubject: "secret-owner-subject", DataOwnerID: "private-owner-id", AdoptExistingData: true,
	}}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/config", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "owner") || strings.Contains(body, "adopt") {
		t.Fatal("private auth settings leaked")
	}
}

func TestOnlyMeAuthRPCIsRegistered(t *testing.T) {
	service := &application.LogtoAuthServiceServer{}
	var handler neoboxv1connect.AuthServiceHandler = service
	if _, err := handler.Login(context.Background(), connect.NewRequest(&neoboxv1.LoginRequest{})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("local login remains available")
	}
	if _, err := handler.CreateUser(context.Background(), connect.NewRequest(&neoboxv1.CreateUserRequest{})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("local user management remains available")
	}
	ctx := auth.WithAuthenticated(context.Background(), &neoboxv1.User{Id: "personal"}, nil)
	response, err := handler.Me(ctx, connect.NewRequest(&neoboxv1.MeRequest{}))
	if err != nil || response.Msg.User.Id != "personal" {
		t.Fatalf("Me: %v", err)
	}
}
