package googleworkspace

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("Workspace tenant not found")
var ErrDuplicate = errors.New("this customer is already attached to the Service Account")

type Tenant struct {
	ID, UserID, ConnectionID, Name, CustomerID, DelegatedAdminEmail string
	Status, StatusMessage                                           string
	StatusCheckedAt, CreatedAt, UpdatedAt                           time.Time
}
type Repository interface {
	CreateOperation(context.Context, *Operation) error
	FinishOperation(context.Context, *Operation) error
	ListOperations(context.Context, OperationFilter) ([]*Operation, int, error)
	List(context.Context, string, string) ([]*Tenant, error)
	Get(context.Context, string, string, string) (*Tenant, error)
	Create(context.Context, *Tenant) error
	Update(context.Context, *Tenant) error
	SetStatus(context.Context, *Tenant) error
	Delete(context.Context, string, string, string) error
	DeleteConnectionData(context.Context, string) error
}

// Operation is sanitized before it reaches this repository. No secrets are stored.
type Operation struct {
	ID, UserID, ConnectionID, TenantID, TenantName, CustomerID string
	Action, Target, Status, Error, ActorID, ActorName          string
	Requested, Result                                          map[string]any
	CreatedAt, FinishedAt                                      time.Time
}
type OperationFilter struct {
	UserID, ConnectionID, TenantID, Query, Status string
	Offset, Limit                                 int
}
