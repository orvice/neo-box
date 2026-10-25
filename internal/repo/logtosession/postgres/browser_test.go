package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-jose/go-jose/v4"

	"go.orx.me/apps/neo-box/internal/auth/logto"
	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/ent"
	handler "go.orx.me/apps/neo-box/internal/handler/http"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	sessionpg "go.orx.me/apps/neo-box/internal/repo/logtosession/postgres"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

type fixture struct {
	t                              *testing.T
	service                        *logto.Service
	newService                     func() *logto.Service
	router                         *gin.Engine
	client                         *ent.Client
	cipher                         *secretbox.Cipher
	issuer                         string
	key                            *rsa.PrivateKey
	mu                             sync.Mutex
	nonce, challenge               string
	subject, audience, tokenIssuer string
	wrongNonce                     bool
	wrongSignature, expiredID      bool
	expiresIn                      int
	refreshError                   string
	refreshes                      atomic.Int32
	exchanges                      atomic.Int32
}

func setup(t *testing.T) *fixture {
	client, db := pgtest.NewClientAndDB(t)
	f := &fixture{t: t, client: client, subject: "owner-sub", audience: "web-app", expiresIn: 3600}
	var err error
	f.key, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	op := httptest.NewServer(http.HandlerFunc(f.serveProvider))
	t.Cleanup(op.Close)
	f.issuer = op.URL + "/oidc"
	f.cipher, err = secretbox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	f.newService = func() *logto.Service {
		service, err := logto.New(context.Background(), config.LogtoConfig{
			Endpoint: op.URL, AppID: "web-app", AppSecret: "app-secret", PublicURL: "https://dashboard.test",
			OwnerSubject: "owner-sub", DataOwnerID: "personal", SessionTTL: time.Hour,
		}, sessionpg.New(db), f.cipher)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}
	f.service = f.newService()
	gin.SetMode(gin.TestMode)
	f.router = gin.New()
	provider := func() handler.BrowserAuth { return f.service }
	f.router.Use(handler.LogtoMiddleware(provider))
	handler.RegisterLogto(f.router, provider)
	f.router.POST("/api/write", func(c *gin.Context) { c.String(200, "saved") })
	f.router.GET("/api/download", func(c *gin.Context) { c.String(200, "snapshot") })
	return f
}

func (f *fixture) serveProvider(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/oidc/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.issuer, "jwks_uri": f.issuer + "/jwks", "authorization_endpoint": f.issuer + "/auth",
			"token_endpoint": f.issuer + "/token", "end_session_endpoint": f.issuer + "/session/end",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/oidc/jwks":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, KeyID: "key-1", Algorithm: "RS256", Use: "sig"}}})
	case "/oidc/token":
		id, secret, ok := r.BasicAuth()
		if !ok || id != "web-app" || secret != "app-secret" {
			w.WriteHeader(401)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshes.Add(1)
			if f.refreshError != "" {
				status := 400
				if f.refreshError == "server_error" {
					status = 503
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": f.refreshError})
				return
			}
			if r.Form.Get("refresh_token") != "server-refresh-secret" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "refreshed-access-secret", "refresh_token": "rotated-refresh-secret", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		f.exchanges.Add(1)
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge || r.Form.Get("redirect_uri") != "https://dashboard.test/api/auth/callback" || r.Form.Get("code") != "auth-code" {
			w.WriteHeader(400)
			return
		}
		nonce := f.nonce
		if f.wrongNonce {
			nonce = "wrong-nonce"
		}
		issuer := f.issuer
		if f.tokenIssuer != "" {
			issuer = f.tokenIssuer
		}
		idExpires := time.Now().Add(time.Hour)
		if f.expiredID {
			idExpires = time.Now().Add(-time.Hour)
		}
		claims := map[string]any{"iss": issuer, "aud": f.audience, "sub": f.subject, "nonce": nonce, "exp": idExpires.Unix(), "iat": time.Now().Unix(), "name": "Logto Owner", "username": "owner", "picture": "https://images.test/avatar.png"}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithHeader("kid", "key-1"))
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(500)
			return
		}
		payload, _ := json.Marshal(claims)
		signed, err := signer.Sign(payload)
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(500)
			return
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			f.t.Error(err)
			w.WriteHeader(500)
			return
		}
		if f.wrongSignature {
			parts := strings.Split(raw, ".")
			signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
			signature[0] ^= 1
			parts[2] = base64.RawURLEncoding.EncodeToString(signature)
			raw = strings.Join(parts, ".")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "server-access-secret", "refresh_token": "server-refresh-secret", "id_token": raw, "token_type": "Bearer", "expires_in": f.expiresIn})
	default:
		http.NotFound(w, r)
	}
}

func (f *fixture) request(method, path string, cookies []*http.Cookie, csrf string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, "https://dashboard.test"+path, strings.NewReader(form.Encode()))
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "https://dashboard.test")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f *fixture) begin() ([]*http.Cookie, string) {
	f.t.Helper()
	w := f.request("GET", "/api/auth/login?redirect=%2Fconnections%3Fprovider%3Dnocodb", nil, "", nil)
	if w.Code != 303 {
		f.t.Fatal(w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_secret") != "" || !strings.Contains(q.Get("scope"), "offline_access") {
		f.t.Fatal("incorrect authorization request")
	}
	f.mu.Lock()
	f.nonce, f.challenge = q.Get("nonce"), q.Get("code_challenge")
	f.mu.Unlock()
	return w.Result().Cookies(), q.Get("state")
}

func (f *fixture) complete(cookies []*http.Cookie, state string) *httptest.ResponseRecorder {
	return f.request("GET", "/api/auth/callback?state="+url.QueryEscape(state)+"&code=auth-code", cookies, "", nil)
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-neobox_session" && c.Value != "" {
			return c
		}
	}
	t.Fatal("no authenticated cookie")
	return nil
}

func TestBrowserSignInSessionAndLogout(t *testing.T) {
	f := setup(t)
	cookies, state := f.begin()
	w := f.complete(cookies, state)
	if w.Code != 303 || w.Header().Get("Location") != "https://dashboard.test/connections?provider=nocodb" {
		t.Fatal(w.Code, w.Header())
	}
	cookie := sessionCookie(t, w)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe session cookie")
	}
	row := f.client.LogtoSession.Query().OnlyX(context.Background())
	if row.ID == cookie.Value || strings.Contains(row.Ciphertext, "server-access-secret") {
		t.Fatal("raw cookie or token stored in database")
	}
	if f.client.User.Query().CountX(context.Background()) != 0 || f.client.Session.Query().CountX(context.Background()) != 0 {
		t.Fatal("created local user/password session")
	}
	w = f.request("GET", "/api/auth/session", []*http.Cookie{cookie}, "", nil)
	var response struct {
		User      struct{ ID, DisplayName string }
		CSRFToken string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.User.ID != "personal" || response.User.DisplayName != "Logto Owner" || response.CSRFToken == "" {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "server-access-secret") || strings.Contains(w.Body.String(), "server-refresh-secret") || strings.Contains(w.Body.String(), "id_token") {
		t.Fatal("tokens reached frontend JSON")
	}
	if w := f.request("POST", "/api/write", []*http.Cookie{cookie}, "", nil); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if w := f.request("POST", "/api/write", []*http.Cookie{cookie}, response.CSRFToken, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := f.request("GET", "/api/download", []*http.Cookie{cookie}, "", nil); w.Code != 200 {
		t.Fatal("cookie download failed")
	}
	if w := f.complete(cookies, state); strings.Contains(w.Header().Get("Location"), "/connections") {
		t.Fatal("replayed callback")
	}
	w = f.request("POST", "/api/auth/logout", []*http.Cookie{cookie}, "", url.Values{"csrf_token": {response.CSRFToken}})
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	logout, err := url.Parse(w.Header().Get("Location"))
	if err != nil || logout.Query().Get("id_token_hint") == "" || logout.Query().Get("post_logout_redirect_uri") != "https://dashboard.test/sign-in" {
		t.Fatal("missing Logto logout")
	}
	if w := f.request("GET", "/api/auth/session", []*http.Cookie{cookie}, "", nil); w.Code != 401 {
		t.Fatal("logged-out cookie accepted")
	}
}

func TestCallbackRejectsUntrustedIdentity(t *testing.T) {
	for _, test := range []string{"state", "nonce", "owner", "audience", "issuer", "expired ID token", "bad signature", "missing cookie"} {
		t.Run(test, func(t *testing.T) {
			f := setup(t)
			cookies, state := f.begin()
			switch test {
			case "state":
				state = "wrong-state"
			case "nonce":
				f.wrongNonce = true
			case "owner":
				f.subject = "other-user"
			case "audience":
				f.audience = "other-client"
			case "issuer":
				f.tokenIssuer = "https://other.test/oidc"
			case "expired ID token":
				f.expiredID = true
			case "bad signature":
				f.wrongSignature = true
			case "missing cookie":
				cookies = nil
			}
			w := f.complete(cookies, state)
			if !strings.Contains(w.Header().Get("Location"), "/sign-in?error=") {
				t.Fatal("invalid callback accepted")
			}
			for _, c := range w.Result().Cookies() {
				if c.Name == "__Host-neobox_session" && c.Value != "" {
					t.Fatal("invalid callback created a session")
				}
			}
			if test == "state" && f.exchanges.Load() != 0 {
				t.Fatal("exchanged code before checking state")
			}
		})
	}
}

func TestConcurrentRefreshAndFailureHandling(t *testing.T) {
	f := setup(t)
	f.expiresIn = 1
	cookies, state := f.begin()
	cookie := sessionCookie(t, f.complete(cookies, state))
	otherInstance := f.newService()
	var wg sync.WaitGroup
	for index := range 8 {
		service := f.service
		if index%2 == 0 {
			service = otherInstance
		}
		wg.Go(func() {
			if _, err := service.Authenticate(context.Background(), cookie.Value); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := f.refreshes.Load(); n != 1 {
		t.Fatalf("refreshed %d times", n)
	}
	for _, failure := range []string{"server_error", "invalid_grant"} {
		t.Run(failure, func(t *testing.T) {
			f := setup(t)
			f.expiresIn = 1
			cookies, state := f.begin()
			cookie := sessionCookie(t, f.complete(cookies, state))
			f.refreshError = failure
			w := f.request("GET", "/api/auth/session", []*http.Cookie{cookie}, "", nil)
			expected := 503
			if failure == "invalid_grant" {
				expected = 401
			}
			if w.Code != expected {
				t.Fatal(w.Code, w.Body.String())
			}
			count := f.client.LogtoSession.Query().CountX(context.Background())
			if failure == "server_error" && (count != 1 || len(w.Result().Cookies()) != 0) {
				t.Fatal("transient failure deleted session")
			}
			if failure == "invalid_grant" && count != 0 {
				t.Fatal("invalid grant session not revoked")
			}
		})
	}
}

func TestExpiredFlowAndSession(t *testing.T) {
	f := setup(t)
	cookies, state := f.begin()
	f.client.LogtoSession.Update().SetExpiresAt(time.Now().Add(-time.Hour)).SaveX(context.Background())
	if w := f.complete(cookies, state); !strings.Contains(w.Header().Get("Location"), "error=login_failed") {
		t.Fatal("expired flow accepted")
	}
	cookies, state = f.begin()
	cookie := sessionCookie(t, f.complete(cookies, state))
	f.client.LogtoSession.Update().SetExpiresAt(time.Now().Add(-time.Hour)).SaveX(context.Background())
	if w := f.request("GET", "/api/auth/session", []*http.Cookie{cookie}, "", nil); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	if err := f.service.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.client.LogtoSession.Query().CountX(context.Background()); n != 0 {
		t.Fatalf("expired rows remain: %d", n)
	}
}

func TestLogoutDoesNotRequireTokenRefresh(t *testing.T) {
	f := setup(t)
	f.expiresIn = 1
	cookies, state := f.begin()
	cookie := sessionCookie(t, f.complete(cookies, state))
	identity, err := f.service.IdentityForLogout(context.Background(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	f.refreshError = "server_error"
	response := f.request("POST", "/api/auth/logout", []*http.Cookie{cookie}, "", url.Values{"csrf_token": {identity.CSRF}})
	if response.Code != 303 || f.refreshes.Load() != 0 {
		t.Fatal("logout depended on remote refresh")
	}
	if n := f.client.LogtoSession.Query().CountX(context.Background()); n != 0 {
		t.Fatal("session not deleted")
	}
}
