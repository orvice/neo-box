package postgres

import (
	"context"
	"go.orx.me/apps/neo-box/internal/ent"
	op "go.orx.me/apps/neo-box/internal/ent/workspaceoperation"
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
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.WorkspaceOperation.Delete().Where(op.ConnectionID(id)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.WorkspaceTenant.Delete().Where(et.ConnectionID(id)).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
func fromRow(r *ent.WorkspaceTenant) *repo.Tenant {
	t := &repo.Tenant{ID: r.ID, UserID: r.UserID, ConnectionID: r.ConnectionID, Name: r.Name, CustomerID: r.CustomerID, DelegatedAdminEmail: r.DelegatedAdminEmail, Status: r.Status, StatusMessage: r.StatusMessage, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.StatusCheckedAt != nil {
		t.StatusCheckedAt = *r.StatusCheckedAt
	}
	return t
}

func (s *Store) CreateOperation(ctx context.Context, o *repo.Operation) error {
	return s.client.WorkspaceOperation.Create().SetID(o.ID).SetUserID(o.UserID).SetConnectionID(o.ConnectionID).SetTenantID(o.TenantID).SetTenantName(o.TenantName).SetCustomerID(o.CustomerID).SetAction(o.Action).SetTarget(o.Target).SetRequested(o.Requested).SetStatus(o.Status).SetError(o.Error).SetActorID(o.ActorID).SetActorName(o.ActorName).SetCreatedAt(o.CreatedAt).Exec(ctx)
}
func (s *Store) FinishOperation(ctx context.Context, o *repo.Operation) error {
	n, err := s.client.WorkspaceOperation.Update().Where(op.ID(o.ID), op.UserID(o.UserID), op.ConnectionID(o.ConnectionID), op.Status("pending")).SetStatus(o.Status).SetError(o.Error).SetResult(o.Result).SetFinishedAt(o.FinishedAt).Save(ctx)
	if err == nil && n == 0 {
		return repo.ErrNotFound
	}
	return err
}
func (s *Store) ListOperations(ctx context.Context, f repo.OperationFilter) ([]*repo.Operation, int, error) {
	q := s.client.WorkspaceOperation.Query().Where(op.UserID(f.UserID), op.ConnectionID(f.ConnectionID))
	if f.TenantID != "" {
		q = q.Where(op.TenantID(f.TenantID))
	}
	if f.Status != "" {
		q = q.Where(op.Status(f.Status))
	}
	if f.Query != "" {
		q = q.Where(op.Or(op.ActionContainsFold(f.Query), op.TargetContainsFold(f.Query), op.ActorNameContainsFold(f.Query), op.TenantNameContainsFold(f.Query)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(op.FieldCreatedAt), ent.Desc(op.FieldID)).Offset(f.Offset).Limit(f.Limit).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*repo.Operation, 0, len(rows))
	for _, r := range rows {
		o := &repo.Operation{ID: r.ID, UserID: r.UserID, ConnectionID: r.ConnectionID, TenantID: r.TenantID, TenantName: r.TenantName, CustomerID: r.CustomerID, Action: r.Action, Target: r.Target, Requested: r.Requested, Result: r.Result, Status: r.Status, Error: r.Error, ActorID: r.ActorID, ActorName: r.ActorName, CreatedAt: r.CreatedAt}
		if r.FinishedAt != nil {
			o.FinishedAt = *r.FinishedAt
		}
		out = append(out, o)
	}
	return out, total, nil
}
