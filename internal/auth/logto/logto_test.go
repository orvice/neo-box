package logto

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"go.orx.me/apps/neo-box/internal/config"
)

func TestAccessTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var jwksReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oidc/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/oidc/jwks":
			jwksReads.Add(1)
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL + "/oidc"
	cfg := config.LogtoConfig{Endpoint: server.URL, AppID: "spa", Resource: "https://neo-box.test/api", OwnerSubject: "owner-sub", DataOwnerID: "personal", Scopes: []string{"access"}}
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	newClaims := func() map[string]any {
		return map[string]any{"iss": issuer, "aud": cfg.Resource, "sub": cfg.OwnerSubject, "exp": time.Now().Add(time.Hour).Unix(), "client_id": cfg.AppID, "scope": "access"}
	}
	sign := func(claims map[string]any) string {
		t.Helper()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
		if err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(claims)
		jws, err := signer.Sign(payload)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := jws.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for range 2 {
		user, err := a.Authenticate(context.Background(), sign(newClaims()))
		if err != nil {
			t.Fatal(err)
		}
		if user.Id != "personal" || user.ExternalId != cfg.OwnerSubject || user.Provider != "logto" || user.Role == "admin" {
			t.Fatalf("unexpected identity: %v", user)
		}
	}
	if jwksReads.Load() != 1 {
		t.Fatal("JWKS was not cached")
	}
	for _, test := range []struct {
		name      string
		change    func(map[string]any)
		forbidden bool
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://other.test/oidc" }, false},
		{"ID token", func(c map[string]any) { c["aud"] = "spa" }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, false},
		{"missing expiry", func(c map[string]any) { delete(c, "exp") }, false},
		{"not yet valid", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, false},
		{"different user", func(c map[string]any) { c["sub"] = "someone-else" }, true},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }, true},
		{"machine identity", func(c map[string]any) { c["sub"] = "m2m-app" }, true},
		{"other client", func(c map[string]any) { c["client_id"] = "other-app" }, true},
		{"organization token", func(c map[string]any) { c["organization_id"] = "org" }, true},
		{"missing permission", func(c map[string]any) { c["scope"] = "other" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := newClaims()
			test.change(claims)
			_, err := a.Authenticate(context.Background(), sign(claims))
			if err == nil {
				t.Fatal("token accepted")
			}
			if errors.Is(err, ErrForbidden) != test.forbidden {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	for _, raw := range []string{"legacy-session-token", "eyJhbGciOiJub25lIn0.e30.", sign(newClaims()) + "tampered"} {
		if _, err := a.Authenticate(context.Background(), raw); err == nil {
			t.Fatal("invalid signature accepted")
		}
	}
}

func TestValidateConfig(t *testing.T) {
	base := config.LogtoConfig{Endpoint: "https://logto.example.com", AppID: "spa", Resource: "https://neobox.example.com/api", OwnerSubject: "owner"}
	for _, endpoint := range []string{"", "http://logto.example.com", "https://user:secret@logto.example.com", "https://logto.example.com?x=y"} {
		cfg := base
		cfg.Endpoint = endpoint
		if ValidateConfig(cfg) == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	for _, owner := range []string{"../someone", "..", "/", " bad "} {
		cfg := base
		cfg.DataOwnerID = owner
		if ValidateConfig(cfg) == nil {
			t.Fatalf("accepted owner %q", owner)
		}
	}
	base.OwnerSubject = ""
	if ValidateConfig(base) == nil {
		t.Fatal("accepted missing owner subject")
	}
}
