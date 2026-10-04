// Package notify persists notification channels and the alert log. Every
// row is owned by one user; reads and writes are scoped by UserID.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// Channel is one place a user's alerts are delivered.
type Channel struct {
	ID     string
	UserID string
	// Type is the channel kind, e.g. "telegram".
	Type    string
	Name    string
	Enabled bool
	// Config is the type's non-secret settings as JSON.
	Config json.RawMessage
	// SecretCiphertext is the type's secret settings (JSON), sealed.
	SecretCiphertext string
	LastSentAt       time.Time
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Severity says how urgent an alert is.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Alert is something a user was told about.
type Alert struct {
	ID     string
	UserID string
	// Source is what raised it: "nocodb", "wasabi", "connection".
	Source       string
	ConnectionID string
	// Kind names the rule, e.g. "snapshot_failed".
	Kind string
	// Key deduplicates: a key already raised for the user is not raised
	// again.
	Key      string
	Severity Severity
	Title    string
	Body     string
	// Link is a dashboard path, e.g. "/connections/<id>".
	Link          string
	CreatedAt     time.Time
	DeliveredAt   time.Time
	DeliveryError string
}

type Repository interface {
	CreateChannel(ctx context.Context, c *Channel) error
	GetChannel(ctx context.Context, userID, id string) (*Channel, error)
	// UpdateChannel overwrites name, enabled, config, secret, and
	// updated_at.
	UpdateChannel(ctx context.Context, c *Channel) error
	DeleteChannel(ctx context.Context, userID, id string) error
	// ListChannels returns the user's channels, oldest first.
	ListChannels(ctx context.Context, userID string) ([]*Channel, error)
	// RecordChannelResult notes a delivery attempt: errMsg is empty on
	// success, and sentAt is only kept on success.
	RecordChannelResult(ctx context.Context, id string, sentAt time.Time, errMsg string) error

	// CreateAlert stores a, unless the user already has an alert with
	// a.Key; created reports which.
	CreateAlert(ctx context.Context, a *Alert) (created bool, err error)
	GetAlert(ctx context.Context, id string) (*Alert, error)
	// SetAlertDelivery records the delivery outcome.
	SetAlertDelivery(ctx context.Context, id string, deliveredAt time.Time, deliveryError string) error
	// ListAlerts returns the user's newest alerts first.
	ListAlerts(ctx context.Context, userID string, limit int) ([]*Alert, error)
	// ListUndelivered returns alerts created at or after since that have
	// neither been delivered nor failed, oldest first.
	ListUndelivered(ctx context.Context, since time.Time) ([]*Alert, error)
}
