// Package logto implements OIDC sign-in with server-side encrypted sessions.
package logto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/logtosession"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

var (
	ErrForbidden       = errors.New("this Logto identity cannot access Neo Box")
	ErrUnauthenticated = errors.New("Logto session is missing or expired")
	ErrInvalidFlow     = errors.New("invalid or expired Logto login flow")
)

const FlowTTL = 10 * time.Minute

// Service keeps OAuth credentials and tokens on the server. Session rows have
// no dependency on local user records or on the deprecated password sessions.
type Service struct {
	cfg        config.LogtoConfig
	verifier   *oidc.IDTokenVerifier
	oauth      oauth2.Config
	endSession string
	httpClient *http.Client
	repo       logtosession.Repository
	cipher     *secretbox.Cipher
}

func ValidateConfig(cfg config.LogtoConfig) error {
	if _, err := secureURL(cfg.Endpoint, false); err != nil {
		return fmt.Errorf("auth.logto.endpoint: %w", err)
	}
	if _, err := secureURL(cfg.PublicURL, true); err != nil {
		return fmt.Errorf("auth.logto.public_url: %w", err)
	}
	if strings.TrimSpace(cfg.AppID) == "" || strings.TrimSpace(cfg.AppSecret) == "" || strings.TrimSpace(cfg.OwnerSubject) == "" {
		return errors.New("auth.logto.app_id, app_secret and owner_subject are required")
	}
	owner := cfg.EffectiveDataOwnerID()
	if owner == "." || owner == ".." || strings.ContainsAny(owner, "/\\") || strings.TrimSpace(owner) != owner {
		return errors.New("auth.logto.data_owner_id must be a single storage path segment")
	}
	return nil
}

func secureURL(raw string, originOnly bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && loopback)) ||
		(originOnly && u.Path != "" && u.Path != "/") {
		return nil, errors.New("must be an HTTPS URL (HTTP loopback is allowed for development), without query or fragment; public_url must be an origin")
	}
	return u, nil
}

func New(ctx context.Context, cfg config.LogtoConfig, repo logtosession.Repository, cipher *secretbox.Cipher) (*Service, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	if cipher == nil || repo == nil {
		return nil, errors.New("Logto requires encrypted server-side session storage")
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, httpClient)
	provider, err := oidc.NewProvider(ctx, strings.TrimRight(cfg.Endpoint, "/")+"/oidc")
	if err != nil {
		return nil, fmt.Errorf("discover Logto: %w", err)
	}
	var metadata struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, err
	}
	if _, err := secureURL(metadata.EndSession, false); err != nil {
		return nil, errors.New("Logto discovery has no valid end_session_endpoint")
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	return &Service{
		cfg: cfg, repo: repo, cipher: cipher, endSession: metadata.EndSession, httpClient: httpClient,
		verifier: provider.VerifierContext(ctx, &oidc.Config{ClientID: cfg.AppID, SupportedSigningAlgs: []string{"RS256", "ES256"}}),
		oauth: oauth2.Config{
			ClientID: cfg.AppID, ClientSecret: cfg.AppSecret, Endpoint: endpoint,
			RedirectURL: strings.TrimRight(cfg.PublicURL, "/") + "/api/auth/callback",
			Scopes:      []string{oidc.ScopeOpenID, "profile", "email", "offline_access"},
		},
	}, nil
}

func (s *Service) Origin() string                  { return strings.TrimRight(s.cfg.PublicURL, "/") }
func (s *Service) SecureCookies() bool             { return strings.HasPrefix(s.Origin(), "https://") }
func (s *Service) SessionTTL() time.Duration       { return s.cfg.EffectiveSessionTTL() }
func (s *Service) Purge(ctx context.Context) error { return s.repo.Purge(ctx, time.Now().UTC()) }
