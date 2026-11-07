package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	testSessionToken = "test-session-token"
	testPassword     = "correct horse battery"
)

// fakeAuthRepo is the auth.Repository subset the HTTP middleware and the auth
// RPCs touch. Everything else panics so an unexpected call is visible.
type fakeAuthRepo struct {
	user      *neoboxv1.User
	hash      string
	users     []*neoboxv1.User
	lookupErr error
}

func newFakeAuthRepo() *fakeAuthRepo {
	return &fakeAuthRepo{
		user: &neoboxv1.User{Id: "u1", Username: "alice", DisplayName: "Alice", Role: "user", Email: "alice@example.com"},
		hash: mustHashPassword(testPassword),
	}
}

func mustHashPassword(pw string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(hash)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (f *fakeAuthRepo) CountUsers(context.Context) (int64, error) { return int64(len(f.users)), nil }

func (f *fakeAuthRepo) ListUsers(context.Context) ([]*neoboxv1.User, error) { return f.users, nil }

func (f *fakeAuthRepo) CreateUser(context.Context, *neoboxv1.User, string) error { panic("unused") }

func (f *fakeAuthRepo) UpdateUserPassword(context.Context, string, string, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}

func (f *fakeAuthRepo) UpdateUserProfile(context.Context, string, string, *string, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}

func (f *fakeAuthRepo) SetUserDisabled(context.Context, string, bool, time.Time) (*neoboxv1.User, error) {
	panic("unused")
}

func (f *fakeAuthRepo) FindUserByUsername(_ context.Context, username string) (*neoboxv1.User, string, error) {
	if f.user == nil || username != f.user.GetUsername() {
		return nil, "", auth.ErrUserNotFound
	}
	return f.user, f.hash, nil
}

func (f *fakeAuthRepo) FindUserByID(context.Context, string) (*neoboxv1.User, string, error) {
	panic("unused")
}

func (f *fakeAuthRepo) FindUserByExternalID(context.Context, string, string) (*neoboxv1.User, error) {
	panic("unused")
}

func (f *fakeAuthRepo) GetUser(context.Context, string) (*neoboxv1.User, error) {
	return f.user, nil
}

func (f *fakeAuthRepo) CreateSession(context.Context, *auth.Session) error { return nil }

func (f *fakeAuthRepo) LookupSession(_ context.Context, tokenHash string, _ time.Time) (*auth.Session, *neoboxv1.User, error) {
	if f.lookupErr != nil {
		return nil, nil, f.lookupErr
	}
	if tokenHash != hashToken(testSessionToken) {
		return nil, nil, auth.ErrSessionNotFound
	}
	return &auth.Session{ID: "s1", UserID: f.user.GetId()}, f.user, nil
}

func (f *fakeAuthRepo) TouchSession(context.Context, string, time.Time) error { return nil }

func (f *fakeAuthRepo) RevokeSession(context.Context, string) error { return nil }

func newTestRouter(t *testing.T) (*gin.Engine, *Handlers, *fakeAuthRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := newFakeAuthRepo()
	router, handlers := SetupRoutes(&config.AppConfig{})
	handlers.authRepo.Store(auth.Repository(repo))
	handlers.authSvcServer.SetRepo(repo)
	engine := gin.New()
	router(engine)
	return engine, handlers, repo
}

func do(t *testing.T, engine *gin.Engine, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func doJSON(t *testing.T, engine *gin.Engine, method, path, body string, authorized bool) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if authorized {
		headers["Authorization"] = "Bearer " + testSessionToken
	}
	return do(t, engine, method, path, []byte(body), headers)
}

func assertConnectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"`+code+`"`) {
		t.Fatalf("body %s missing code %q", body, code)
	}
}

func TestHealthStaysPublic(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	rec := doJSON(t, engine, http.MethodGet, "/ping", "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("ping = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPublicOAuthProvidersNeedsNoSession(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/ListOAuthProviders", `{}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestOAuthLoginPathsArePublic(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	for _, procedure := range []string{"BeginOAuthFlow", "CompleteOAuthFlow"} {
		rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/"+procedure, `{}`, false)
		if rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "unauthorized") {
			t.Fatalf("%s was gated by the session middleware: %s", procedure, rec.Body.String())
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %s)", procedure, rec.Code, rec.Body.String())
		}
	}
}

func TestLoginArgumentMetadataAndSnakeCaseJSON(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", `{}`, false)
	assertConnectError(t, rec, http.StatusBadRequest, "invalid_argument")
	if got := rec.Header().Get("Argument"); got != "username" {
		t.Fatalf("Argument header = %q, want username", got)
	}
	if !strings.Contains(rec.Body.String(), `"message":"username is required"`) {
		t.Fatalf("message = %s", rec.Body.String())
	}
}

func TestLoginAcceptsCharsetEmptyAndUnknownFields(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	cases := []struct {
		name        string
		body        []byte
		contentType string
	}{
		{"empty body", nil, "application/json"},
		{"empty object", []byte(`{}`), "application/json"},
		{"charset", []byte(`{}`), "application/json; charset=utf-8"},
		{"unknown fields", []byte(`{"future_field":{"nested":true},"another":[1,2,3]}`), "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", tc.body, map[string]string{"Content-Type": tc.contentType})
			assertConnectError(t, rec, http.StatusBadRequest, "invalid_argument")
			if got := rec.Header().Get("Argument"); got != "username" {
				t.Fatalf("Argument header = %q, want username", got)
			}
		})
	}
}

func TestLoginSnakeCaseResponseAndBinaryClient(t *testing.T) {
	engine, _, _ := newTestRouter(t)

	// JSON: response fields use proto field names, not JSON names.
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login",
		`{"username":"alice","password":"`+testPassword+`"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("json login status = %d (body %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"expires_at"`) {
		t.Fatalf("response is not snake_case: %s", body)
	}
	if strings.Contains(body, `"expiresAt"`) {
		t.Fatalf("response leaked camelCase: %s", body)
	}

	// Binary protobuf: what the Connect-Web frontend sends.
	reqBody, err := proto.Marshal(&neoboxv1.LoginRequest{Username: "alice", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	rec = do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", reqBody, map[string]string{"Content-Type": "application/proto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("binary login status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/proto") {
		t.Fatalf("binary Content-Type = %q", got)
	}
	var res neoboxv1.LoginResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("binary response: %v", err)
	}
	if res.GetToken() == "" || res.GetUser().GetDisplayName() != "Alice" {
		t.Fatalf("binary response = %v", &res)
	}
}

func TestInvalidCredentialsReachTheHandler(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", `{"username":"alice","password":"wrong"}`, false)
	assertConnectError(t, rec, http.StatusUnauthorized, "unauthenticated")
}

func TestMeRequiresAValidSession(t *testing.T) {
	engine, _, repo := newTestRouter(t)

	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Me", `{}`, false)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error":"unauthorized"`) {
		t.Fatalf("no token = %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Me", []byte(`{}`), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer not-the-token",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d %s", rec.Code, rec.Body.String())
	}

	repo.lookupErr = auth.ErrUserDisabled
	rec = doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Me", `{}`, true)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user = %d %s", rec.Code, rec.Body.String())
	}
	repo.lookupErr = nil

	rec = doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Me", `{}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid session = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"display_name":"Alice"`) {
		t.Fatalf("Me response = %s", rec.Body.String())
	}
}

func TestAdminOnlyRPCsCheckTheRole(t *testing.T) {
	engine, _, repo := newTestRouter(t)
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/ListUsers", `{}`, true)
	assertConnectError(t, rec, http.StatusForbidden, "permission_denied")

	repo.user.Role = "admin"
	repo.users = []*neoboxv1.User{repo.user}
	rec = doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/ListUsers", `{}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"alice"`) {
		t.Fatalf("admin ListUsers = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCORSPreflightNeedsNoSession(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	rec := do(t, engine, http.MethodOptions, "/api/neobox.v1.AuthService/Me", nil, map[string]string{
		"Origin":                        "https://neobox.example.com",
		"Access-Control-Request-Method": "POST",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://neobox.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestRPCSizeLimitAppliesToPlainAndGzipRequests(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	oversize := []byte(`{"username":"","pad":"` + strings.Repeat("a", connectx.RPCMaxRequestBytes) + `"}`)
	rec := do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", oversize, map[string]string{"Content-Type": "application/json"})
	assertConnectError(t, rec, http.StatusTooManyRequests, "resource_exhausted")

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(oversize); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	rec = do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", compressed.Bytes(), map[string]string{
		"Content-Type":     "application/json",
		"Content-Encoding": "gzip",
	})
	assertConnectError(t, rec, http.StatusTooManyRequests, "resource_exhausted")
}

func TestRPCSizeLimitAcceptsBodyUnderTheLimitAndGzip(t *testing.T) {
	engine, _, _ := newTestRouter(t)
	under := []byte(`{"username":"","pad":"` + strings.Repeat("a", connectx.RPCMaxRequestBytes-1024) + `"}`)
	rec := do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", under, map[string]string{"Content-Type": "application/json"})
	assertConnectError(t, rec, http.StatusBadRequest, "invalid_argument")

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(under); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	rec = do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/Login", compressed.Bytes(), map[string]string{
		"Content-Type":     "application/json",
		"Content-Encoding": "gzip",
	})
	assertConnectError(t, rec, http.StatusBadRequest, "invalid_argument")
}

func TestResponseSizeIsNotCappedByTheRequestLimit(t *testing.T) {
	engine, _, repo := newTestRouter(t)
	repo.user.Role = "admin"
	big := strings.Repeat("x", 32<<10)
	for i := 0; i < 200; i++ {
		repo.users = append(repo.users, &neoboxv1.User{Id: "u" + string(rune('a'+i%26)), Username: big})
	}
	rec := doJSON(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/ListUsers", `{}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() <= connectx.RPCMaxRequestBytes {
		t.Fatalf("response was only %d bytes; expected an uncapped large response", rec.Body.Len())
	}
}

func TestUnknownRPCAndServiceProtocolErrors(t *testing.T) {
	engine, _, _ := newTestRouter(t)

	rec := do(t, engine, http.MethodPost, "/api/neobox.v1.AuthService/NoSuchMethod", []byte(`{}`), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + testSessionToken,
	})
	assertConnectError(t, rec, http.StatusNotImplemented, "unimplemented")

	// A service that does not exist is not routed at all; the session
	// middleware still runs first.
	rec = do(t, engine, http.MethodPost, "/api/neobox.v1.NoSuchService/Method", []byte(`{}`), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + testSessionToken,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown service = %d %s", rec.Code, rec.Body.String())
	}

	// A GET under a service prefix is not a Connect GET (HTTP GET is not
	// enabled), so the procedure answers 405 instead of an RPC error.
	rec = do(t, engine, http.MethodGet, "/api/neobox.v1.AuthService/Login", nil, map[string]string{
		"Authorization": "Bearer " + testSessionToken,
	})
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-RPC GET = %d %s", rec.Code, rec.Body.String())
	}
}

func TestSnapshotDownloadRouteStaysOutsideTheRPCMux(t *testing.T) {
	engine, _, _ := newTestRouter(t)

	rec := do(t, engine, http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("download without a session = %d %s", rec.Code, rec.Body.String())
	}

	// Authenticated, but Bootstrap has not attached the content source yet:
	// the route answers from its own Gin handler, not the Connect mux.
	rec = do(t, engine, http.MethodGet, "/api/nocodb/snapshots/snap1/download", nil, map[string]string{
		"Authorization": "Bearer " + testSessionToken,
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}

	// The same path as an RPC-shaped POST is not an RPC.
	rec = do(t, engine, http.MethodPost, "/api/nocodb/snapshots/snap1/download", []byte(`{}`), map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + testSessionToken,
	})
	if rec.Code == http.StatusOK || rec.Code == http.StatusNotImplemented {
		t.Fatalf("download path reached the RPC mux: %d %s", rec.Code, rec.Body.String())
	}
}
