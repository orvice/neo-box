// Package connection persists Connections: one account at a third-party
// service (a Provider), owned by one user. Provider settings are opaque
// here: the non-secret part is stored as JSON, and the secret part arrives
// already sealed.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("connection not found")

// Status is the connection's health: the outcome of the last credential
// check or background run.
type Status string

const (
	StatusUnknown Status = "unknown"
	StatusOK      Status = "ok"
	StatusError   Status = "error"
)

type Connection struct {
	ID       string
	UserID   string
	Provider string
	Name     string
	// Config is the provider's non-secret settings as JSON.
	Config json.RawMessage
	// SecretCiphertext is the provider's secret settings as JSON, sealed
	// with secretbox.
	SecretCiphertext string
	Status           Status
	StatusMessage    string
	StatusCheckedAt  time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Filter narrows List. Zero fields are ignored.
type Filter struct {
	UserID   string
	Provider string
}

type Repository interface {
	Create(ctx context.Context, c *Connection) error
	// Get returns the user's connection.
	Get(ctx context.Context, userID, id string) (*Connection, error)
	// GetByID bypasses owner scoping; used by background work.
	GetByID(ctx context.Context, id string) (*Connection, error)
	// List returns matching connections, oldest first.
	List(ctx context.Context, f Filter) ([]*Connection, error)
	// Update overwrites every mutable field of the user's connection.
	Update(ctx context.Context, c *Connection) error
	// SetStatus records the connection's health.
	SetStatus(ctx context.Context, id string, status Status, message string, at time.Time) error
	Delete(ctx context.Context, userID, id string) error
}
