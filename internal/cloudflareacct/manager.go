// Package cloudflareacct is what Neo Box does with the Cloudflare Account of
// a connection: it lists the Zones, DNS records, Workers and Pages projects
// the API token can see in that Account, live and never stored, and changes
// DNS records, logging each change it starts.
//
// Every Zone and record is checked against the connection's Account before
// it is read or changed, whatever else the token can reach. It is also the
// "cloudflare" connection Provider.
package cloudflareacct

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
)

// Connections is what the Manager needs from the connection service
// (*connection.Service).
type Connections interface {
	Open(c *connrepo.Connection, config, secret any) error
	RecordStatus(ctx context.Context, id string, err error)
}

type Config struct {
	// Endpoint overrides cloudflare.DefaultEndpoint.
	Endpoint string
	// RetryDelay overrides the client's first retry delay (tests).
	RetryDelay time.Duration
}

type Manager struct {
	cfg   Config
	repo  cfrepo.Repository
	conns Connections
	now   func() time.Time
	newID func() string

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func New(cfg Config, r cfrepo.Repository, conns Connections) *Manager {
	return &Manager{
		cfg: cfg, repo: r, conns: conns,
		now: time.Now, newID: uuid.NewString,
		limiters: make(map[string]*rate.Limiter),
	}
}

// limiter is shared by every client of one connection.
func (m *Manager) limiter(connectionID string) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.limiters[connectionID]
	if !ok {
		l = cloudflare.NewLimiter()
		m.limiters[connectionID] = l
	}
	return l
}

func (m *Manager) forget(connectionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.limiters, connectionID)
}

func (m *Manager) newClient(token string, limiter *rate.Limiter) (*cloudflare.Client, error) {
	opts := []cloudflare.Option{cloudflare.WithEndpoint(m.cfg.Endpoint)}
	if limiter != nil {
		opts = append(opts, cloudflare.WithLimiter(limiter))
	}
	if m.cfg.RetryDelay > 0 {
		opts = append(opts, cloudflare.WithRetryDelay(m.cfg.RetryDelay))
	}
	return cloudflare.New(token, opts...)
}

// Account is a connection opened for one request: its Account and a client
// with its token.
type Account struct {
	m    *Manager
	conn *connrepo.Connection
	api  *cloudflare.Client
	// ID is the connection's Account ID.
	ID string
}

// Open decrypts c's settings. c must be a Cloudflare connection.
func (m *Manager) Open(c *connrepo.Connection) (*Account, error) {
	if c.Provider != cloudflare.ProviderType {
		return nil, fmt.Errorf("connection %s is not a Cloudflare connection", c.ID)
	}
	var (
		cfg cloudflare.ConnectionConfig
		sec cloudflare.ConnectionSecret
	)
	if err := m.conns.Open(c, &cfg, &sec); err != nil {
		return nil, err
	}
	api, err := m.newClient(sec.APIToken, m.limiter(c.ID))
	if err != nil {
		return nil, err
	}
	return &Account{m: m, conn: c, api: api, ID: cfg.AccountID}, nil
}

// --- errors ---

var (
	// ErrZoneNotFound: no such Zone in the connection's Account.
	ErrZoneNotFound = errors.New("zone not found in this account")
	// ErrRecordNotFound: no such record in the Zone.
	ErrRecordNotFound = errors.New("DNS record not found in this zone")
	// ErrNotEditable: Neo Box does not change records of this type, or
	// this record cannot be proxied.
	ErrNotEditable = errors.New("this DNS record cannot be changed here")
	// ErrReadOnly: Cloudflare says DNS records of the Zone can be read but
	// not changed.
	ErrReadOnly = errors.New("the API token may read but not change this zone's DNS records")
	// ErrLogUnavailable: the change could not be logged, so it was not
	// sent.
	ErrLogUnavailable = errors.New("the DNS operation could not be logged, so nothing was changed")
)

// TokenRejectedError means Cloudflare no longer accepts the connection's
// token; the connection's status has been set to error.
type TokenRejectedError struct{ Err error }

func (e *TokenRejectedError) Error() string {
	return "Cloudflare does not accept this connection's API token (invalid, expired or revoked); update the connection: " + e.Err.Error()
}
func (e *TokenRejectedError) Unwrap() error { return e.Err }

// PermissionError means the token is valid but lacks the permission for
// what was asked.
type PermissionError struct {
	// What could not be done, e.g. "list Workers".
	What string
	// Permission names the token permission that allows it.
	Permission string
	Err        error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("the API token may not %s in this account; grant it %q (%v)", e.What, e.Permission, e.Err)
}
func (e *PermissionError) Unwrap() error { return e.Err }

// classify turns a refused token into TokenRejectedError (and records the
// connection's status) and a refused permission into PermissionError.
// Cloudflare answers some missing permissions in the same way as a bad
// token, so a 403 is told apart by verifying the token.
func (a *Account) classify(ctx context.Context, err error, what, permission string) error {
	switch {
	case cloudflare.IsAuthError(err):
		return a.tokenRejected(ctx, err)
	case cloudflare.IsPermissionDenied(err):
		if verr := a.api.VerifyToken(ctx, a.ID); verr != nil && cloudflare.IsAuthError(verr) {
			return a.tokenRejected(ctx, verr)
		}
		return &PermissionError{What: what, Permission: permission, Err: err}
	}
	return err
}

func (a *Account) tokenRejected(ctx context.Context, err error) error {
	a.m.conns.RecordStatus(context.WithoutCancel(ctx), a.conn.ID, fmt.Errorf("Cloudflare no longer accepts the API token: %w", err))
	return &TokenRejectedError{Err: err}
}
