package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.orx.me/apps/neo-box/internal/ent"
	entconnection "go.orx.me/apps/neo-box/internal/ent/connection"
	"go.orx.me/apps/neo-box/internal/ent/predicate"
	repo "go.orx.me/apps/neo-box/internal/repo/connection"
)

type Store struct {
	client *ent.Client
}

var _ repo.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) Create(ctx context.Context, c *repo.Connection) error {
	err := s.client.Connection.Create().
		SetID(c.ID).
		SetUserID(c.UserID).
		SetProvider(c.Provider).
		SetName(c.Name).
		SetConfig(string(c.Config)).
		SetSecretCiphertext(c.SecretCiphertext).
		SetStatus(string(c.Status)).
		SetStatusMessage(c.StatusMessage).
		SetNillableStatusCheckedAt(nilIfZero(c.StatusCheckedAt)).
		SetCreatedAt(c.CreatedAt).
		SetUpdatedAt(c.UpdatedAt).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert connection: %w", err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, userID, id string) (*repo.Connection, error) {
	return s.find(ctx, entconnection.ID(id), entconnection.UserID(userID))
}

func (s *Store) GetByID(ctx context.Context, id string) (*repo.Connection, error) {
	return s.find(ctx, entconnection.ID(id))
}

func (s *Store) find(ctx context.Context, ps ...predicate.Connection) (*repo.Connection, error) {
	row, err := s.client.Connection.Query().Where(ps...).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, repo.ErrNotFound
		}
		return nil, fmt.Errorf("find connection: %w", err)
	}
	return fromRow(row), nil
}

func (s *Store) List(ctx context.Context, f repo.Filter) ([]*repo.Connection, error) {
	q := s.client.Connection.Query()
	if f.UserID != "" {
		q = q.Where(entconnection.UserID(f.UserID))
	}
	if f.Provider != "" {
		q = q.Where(entconnection.Provider(f.Provider))
	}
	rows, err := q.Order(ent.Asc(entconnection.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	out := make([]*repo.Connection, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

func (s *Store) Update(ctx context.Context, c *repo.Connection) error {
	u := s.client.Connection.Update().
		Where(entconnection.ID(c.ID), entconnection.UserID(c.UserID)).
		SetName(c.Name).
		SetConfig(string(c.Config)).
		SetSecretCiphertext(c.SecretCiphertext).
		SetStatus(string(c.Status)).
		SetStatusMessage(c.StatusMessage).
		SetUpdatedAt(c.UpdatedAt)
	if c.StatusCheckedAt.IsZero() {
		u.ClearStatusCheckedAt()
	} else {
		u.SetStatusCheckedAt(c.StatusCheckedAt)
	}
	n, err := u.Save(ctx)
	if err != nil {
		return fmt.Errorf("update connection: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) SetStatus(ctx context.Context, id string, status repo.Status, message string, at time.Time) error {
	n, err := s.client.Connection.Update().
		Where(entconnection.ID(id)).
		SetStatus(string(status)).
		SetStatusMessage(message).
		SetStatusCheckedAt(at).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("set connection status: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, userID, id string) error {
	n, err := s.client.Connection.Delete().
		Where(entconnection.ID(id), entconnection.UserID(userID)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	if n == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func fromRow(r *ent.Connection) *repo.Connection {
	c := &repo.Connection{
		ID: r.ID, UserID: r.UserID, Provider: r.Provider, Name: r.Name,
		Config: json.RawMessage(r.Config), SecretCiphertext: r.SecretCiphertext,
		Status: repo.Status(r.Status), StatusMessage: r.StatusMessage,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.StatusCheckedAt != nil {
		c.StatusCheckedAt = *r.StatusCheckedAt
	}
	return c
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
