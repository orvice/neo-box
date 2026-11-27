// Package porkbunacct is what Neo Box does with a Porkbun connection:
// it reads domains, nameservers, DNS records, URL forwards, DNSSEC records
// and glue records live from Porkbun (strictly read-only, never stored) and
// implements the "porkbun" connection Provider.
package porkbunacct

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/porkbun"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
)

// Connections is what the Manager needs from the connection service
// (*connection.Service).
type Connections interface {
	Open(c *connrepo.Connection, config, secret any) error
	RecordStatus(ctx context.Context, id string, err error)
}

type Config struct {
	// Endpoint overrides porkbun.DefaultEndpoint.
	Endpoint string
	// RetryDelay overrides the client's first retry delay (tests).
	RetryDelay time.Duration
	// Limiter returns the rate limiter of a new connection; nil means
	// porkbun.NewLimiter (tests).
	Limiter func() *rate.Limiter
}

type Manager struct {
	cfg   Config
	conns Connections
	now   func() time.Time

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func New(cfg Config, conns Connections) *Manager {
	return &Manager{
		cfg:      cfg,
		conns:    conns,
		now:      time.Now,
		limiters: make(map[string]*rate.Limiter),
	}
}

func (m *Manager) limiter(connectionID string) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.limiters[connectionID]
	if !ok {
		if m.cfg.Limiter != nil {
			l = m.cfg.Limiter()
		} else {
			l = porkbun.NewLimiter()
		}
		m.limiters[connectionID] = l
	}
	return l
}

func (m *Manager) forget(connectionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.limiters, connectionID)
}

func (m *Manager) newClient(apiKey, secretAPIKey string, limiter *rate.Limiter) (*porkbun.Client, error) {
	opts := []porkbun.Option{porkbun.WithEndpoint(m.cfg.Endpoint)}
	if limiter != nil {
		opts = append(opts, porkbun.WithLimiter(limiter))
	}
	if m.cfg.RetryDelay > 0 {
		opts = append(opts, porkbun.WithRetryDelay(m.cfg.RetryDelay))
	}
	return porkbun.New(apiKey, secretAPIKey, opts...)
}

// Account is a Porkbun connection opened for one request.
type Account struct {
	m    *Manager
	conn *connrepo.Connection
	api  *porkbun.Client
}

// Open decrypts c's credentials and prepares a rate-limited Porkbun client.
func (m *Manager) Open(c *connrepo.Connection) (*Account, error) {
	if c.Provider != porkbun.ProviderType {
		return nil, fmt.Errorf("connection %s is not a Porkbun connection", c.ID)
	}
	var (
		cfg porkbun.ConnectionConfig
		sec porkbun.ConnectionSecret
	)
	if err := m.conns.Open(c, &cfg, &sec); err != nil {
		return nil, err
	}
	api, err := m.newClient(cfg.APIKey, sec.SecretAPIKey, m.limiter(c.ID))
	if err != nil {
		return nil, err
	}
	return &Account{m: m, conn: c, api: api}, nil
}

// --- errors ---

var (
	// ErrDomainNotFound: no such domain in the connection's Porkbun account.
	ErrDomainNotFound = errors.New("domain not found in this Porkbun account")
)

// CredentialsRejectedError means Porkbun no longer accepts the connection's
// API key pair; the connection's health status has been marked error.
type CredentialsRejectedError struct{ Err error }

func (e *CredentialsRejectedError) Error() string {
	return "Porkbun does not accept this connection's API keys (invalid or revoked); update the connection: " + e.Err.Error()
}
func (e *CredentialsRejectedError) Unwrap() error { return e.Err }

// PermissionError means the API key is valid but lacks the scope, IP allowlist
// or domain allowlist needed for what was asked.
type PermissionError struct {
	What string
	Err  error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("the Porkbun API key may not %s (%v)", e.What, e.Err)
}
func (e *PermissionError) Unwrap() error { return e.Err }

// APIAccessDisabledError means the target domain is not opted in to Porkbun
// API access yet.
type APIAccessDisabledError struct {
	Domain string
	Err    error
}

func (e *APIAccessDisabledError) Error() string {
	return fmt.Sprintf("API access is disabled for %s; enable API Access for this domain in the Porkbun dashboard", e.Domain)
}
func (e *APIAccessDisabledError) Unwrap() error { return e.Err }

// RateLimitedError means Porkbun throttled the request even after retries.
type RateLimitedError struct{ Err error }

func (e *RateLimitedError) Error() string {
	return "Porkbun rate limit exceeded; please wait a moment and try again"
}
func (e *RateLimitedError) Unwrap() error { return e.Err }

func (a *Account) classify(ctx context.Context, err error, domain, what string) error {
	switch {
	case porkbun.IsAuthError(err):
		return a.credentialsRejected(ctx, err)
	case porkbun.IsAPIAccessDisabled(err):
		return &APIAccessDisabledError{Domain: domain, Err: err}
	case porkbun.IsPermissionDenied(err):
		return &PermissionError{What: what, Err: err}
	case porkbun.IsRateLimited(err):
		return &RateLimitedError{Err: err}
	case porkbun.IsNotFound(err):
		return ErrDomainNotFound
	}
	return err
}

func (a *Account) credentialsRejected(ctx context.Context, err error) error {
	a.m.conns.RecordStatus(context.WithoutCancel(ctx), a.conn.ID, fmt.Errorf("Porkbun no longer accepts the API keys: %w", err))
	return &CredentialsRejectedError{Err: err}
}
