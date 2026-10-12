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
	List(context.Context, string, string) ([]*Tenant, error)
	Get(context.Context, string, string, string) (*Tenant, error)
	Create(context.Context, *Tenant) error
	Update(context.Context, *Tenant) error
	SetStatus(context.Context, *Tenant) error
	Delete(context.Context, string, string, string) error
	DeleteConnectionData(context.Context, string) error
}
