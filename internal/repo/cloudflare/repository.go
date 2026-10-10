// Package cloudflare persists the DNS operations started from Neo Box on
// Cloudflare connections. Every operation is owned by one user; reads are
// scoped by UserID.
package cloudflare

import (
	"context"
	"errors"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
)

var ErrNotFound = errors.New("dns operation not found")

// Action is what a DNS operation does to a record.
type Action string

const (
	ActionCreate     Action = "create"
	ActionUpdate     Action = "update"
	ActionDelete     Action = "delete"
	ActionSetProxied Action = "set_proxied"
)

// Status is a DNS operation's outcome.
type Status string

const (
	// StatusPending: recorded before the change was sent, with no outcome
	// recorded since.
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	// StatusUnknown: sent, but Cloudflare's answer never arrived, so the
	// change may or may not have been made.
	StatusUnknown Status = "unknown"
)

// DNSOperation is one DNS change started from Neo Box.
type DNSOperation struct {
	ID           string
	UserID       string
	ConnectionID string
	AccountID    string
	ZoneID       string
	ZoneName     string
	Action       Action
	// RecordID is empty for a create until it succeeds.
	RecordID   string
	RecordType string
	RecordName string
	// Before is the record before the change; nil for a create.
	Before *cloudflare.DNSRecord
	// After is the record Cloudflare returned; nil for a delete and for
	// anything that did not succeed.
	After *cloudflare.DNSRecord
	// Requested is the change as sent to Cloudflare (the request body's
	// record fields); nil for a delete.
	Requested map[string]any
	Status    Status
	// Error says why it failed or is unknown. It never holds credentials.
	Error      string
	ActorID    string
	ActorName  string
	CreatedAt  time.Time
	FinishedAt time.Time
}

// Filter narrows ListDNSOperations. UserID and ConnectionID are required;
// an empty ZoneID lists every Zone's operations.
type Filter struct {
	UserID       string
	ConnectionID string
	ZoneID       string
	Offset       int
	Limit        int
}

type Repository interface {
	// CreateDNSOperation stores a new operation, normally pending.
	CreateDNSOperation(ctx context.Context, op *DNSOperation) error
	// FinishDNSOperation records the outcome: status, record ID, after,
	// error and finished time. Everything else is fixed at create.
	FinishDNSOperation(ctx context.Context, op *DNSOperation) error
	// ListDNSOperations returns one page of matching operations, newest
	// first, and how many match in all.
	ListDNSOperations(ctx context.Context, f Filter) ([]*DNSOperation, int, error)
	// DeleteConnectionData deletes every operation of the connection.
	DeleteConnectionData(ctx context.Context, connectionID string) error
}
