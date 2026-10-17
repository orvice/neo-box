// Package logto verifies API access tokens. It never creates local users or
// sessions. Logto owns sign-in, account information and session refresh.
package logto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"go.orx.me/apps/neo-box/internal/config"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

var ErrForbidden = errors.New("this Logto identity cannot access Neo Box")

type Authenticator struct {
	verifier *oidc.IDTokenVerifier
	cfg      config.LogtoConfig
}

func ValidateConfig(cfg config.LogtoConfig) error {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return errors.New("invalid auth.logto.endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && loopback)) {
		return errors.New("auth.logto.endpoint must be an HTTPS URL (HTTP localhost is allowed for development)")
	}
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.Resource) == "" || strings.TrimSpace(cfg.OwnerSubject) == "" {
		return errors.New("auth.logto.app_id, resource and owner_subject are required")
	}
	if cfg.Resource == cfg.AppID {
		return errors.New("auth.logto.resource must be an API resource identifier, distinct from the SPA app_id")
	}
	owner := cfg.EffectiveDataOwnerID()
	if owner == "." || owner == ".." || strings.ContainsAny(owner, "/\\") || strings.TrimSpace(owner) != owner {
		return errors.New("auth.logto.data_owner_id must be a single nonempty storage path segment")
	}
	for _, scope := range cfg.Scopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return errors.New("auth.logto.scopes must contain nonempty scope names")
		}
	}
	return nil
}

func New(ctx context.Context, cfg config.LogtoConfig) (*Authenticator, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	// The HTTP client also bounds background JWKS refreshes. RemoteKeySet caches
	// keys and fetches new ones on rotation; tokens never select a key URL.
	ctx = oidc.ClientContext(ctx, &http.Client{Timeout: 10 * time.Second})
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.Endpoint, "/")+"/oidc")
	if err != nil {
		return nil, fmt.Errorf("discover Logto: %w", err)
	}
	return &Authenticator{cfg: cfg, verifier: provider.VerifierContext(ctx, &oidc.Config{
		ClientID:             cfg.Resource,
		SupportedSigningAlgs: []string{"RS256", "ES256"},
	})}, nil
}

func (a *Authenticator) Authenticate(ctx context.Context, raw string) (*neoboxv1.User, error) {
	// This verifier checks the signature, issuer, API audience, expiry and nbf.
	// An ID token has the SPA audience and is therefore rejected here.
	token, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid Logto access token: %w", err)
	}
	var claims struct {
		Scope          string `json:"scope"`
		ClientID       string `json:"client_id"`
		OrganizationID string `json:"organization_id"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, err
	}
	if token.Subject != a.cfg.OwnerSubject || claims.OrganizationID != "" ||
		(claims.ClientID != "" && claims.ClientID != a.cfg.AppID) {
		return nil, ErrForbidden
	}
	for _, required := range a.cfg.Scopes {
		if !slices.Contains(strings.Fields(claims.Scope), required) {
			return nil, ErrForbidden
		}
	}
	return &neoboxv1.User{
		Id: a.cfg.EffectiveDataOwnerID(), Username: token.Subject,
		DisplayName: token.Subject, Role: "user", Provider: "logto", ExternalId: token.Subject,
	}, nil
}
