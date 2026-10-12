package postgres

import (
	"context"
	"go.orx.me/apps/neo-box/internal/ent"
	et "go.orx.me/apps/neo-box/internal/ent/workspacetenant"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
)

type Store struct{ client *ent.Client }

var _ repo.Repository = (*Store)(nil)

func New(c *ent.Client) *Store { return &Store{client: c} }
func (s *Store) List(ctx context.Context, userID, connectionID string) ([]*repo.Tenant, error) {
	rows, err := s.client.WorkspaceTenant.Query().Where(et.UserID(userID), et.ConnectionID(connectionID)).Order(ent.Asc(et.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*repo.Tenant, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}
func (s *Store) Get(ctx context.Context, userID, connectionID, id string) (*repo.Tenant, error) {
	r, err := s.client.WorkspaceTenant.Query().Where(et.UserID(userID), et.ConnectionID(connectionID), et.ID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromRow(r), nil
}
func (s *Store) Create(ctx context.Context, t *repo.Tenant) error {
	err := s.client.WorkspaceTenant.Create().SetID(t.ID).SetUserID(t.UserID).SetConnectionID(t.ConnectionID).SetName(t.Name).SetCustomerID(t.CustomerID).SetDelegatedAdminEmail(t.DelegatedAdminEmail).SetStatus(t.Status).SetStatusMessage(t.StatusMessage).SetStatusCheckedAt(t.StatusCheckedAt).SetCreatedAt(t.CreatedAt).SetUpdatedAt(t.UpdatedAt).Exec(ctx)
	if ent.IsConstraintError(err) {
		return repo.ErrDuplicate
	}
	return err
}
func (s *Store) Update(ctx context.Context, t *repo.Tenant) error {
	n, err := s.client.WorkspaceTenant.Update().Where(et.UserID(t.UserID), et.ConnectionID(t.ConnectionID), et.ID(t.ID)).SetName(t.Name).SetDelegatedAdminEmail(t.DelegatedAdminEmail).SetStatus(t.Status).SetStatusMessage(t.StatusMessage).SetStatusCheckedAt(t.StatusCheckedAt).SetUpdatedAt(t.UpdatedAt).Save(ctx)
	if err == nil && n == 0 {
		return repo.ErrNotFound
	}
	return err
}
func (s *Store) SetStatus(ctx context.Context, t *repo.Tenant) error {
	n, err := s.client.WorkspaceTenant.Update().Where(et.UserID(t.UserID), et.ConnectionID(t.ConnectionID), et.ID(t.ID)).SetStatus(t.Status).SetStatusMessage(t.StatusMessage).SetStatusCheckedAt(t.StatusCheckedAt).Save(ctx)
	if err == nil && n == 0 {
		return repo.ErrNotFound
	}
	return err
}
func (s *Store) Delete(ctx context.Context, userID, connectionID, id string) error {
	n, err := s.client.WorkspaceTenant.Delete().Where(et.UserID(userID), et.ConnectionID(connectionID), et.ID(id)).Exec(ctx)
	if err == nil && n == 0 {
		return repo.ErrNotFound
	}
	return err
}
func (s *Store) DeleteConnectionData(ctx context.Context, id string) error {
	_, err := s.client.WorkspaceTenant.Delete().Where(et.ConnectionID(id)).Exec(ctx)
	return err
}
func fromRow(r *ent.WorkspaceTenant) *repo.Tenant {
	t := &repo.Tenant{ID: r.ID, UserID: r.UserID, ConnectionID: r.ConnectionID, Name: r.Name, CustomerID: r.CustomerID, DelegatedAdminEmail: r.DelegatedAdminEmail, Status: r.Status, StatusMessage: r.StatusMessage, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.StatusCheckedAt != nil {
		t.StatusCheckedAt = *r.StatusCheckedAt
	}
	return t
}
