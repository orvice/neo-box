package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/logtosession"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

var _ logtosession.Repository = (*Store)(nil)

func (s *Store) Create(ctx context.Context, r *logtosession.Record) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO logto_sessions (id, kind, ciphertext, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`, r.ID, r.Kind, r.Ciphertext, r.CreatedAt, r.ExpiresAt)
	return err
}

func (s *Store) TakeFlow(ctx context.Context, id string, now time.Time) (*logtosession.Record, error) {
	r := &logtosession.Record{ID: id, Kind: "flow"}
	err := s.db.QueryRowContext(ctx, `DELETE FROM logto_sessions WHERE id = $1 AND kind = 'flow' RETURNING ciphertext, created_at, expires_at`, id).Scan(&r.Ciphertext, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, logtosession.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !now.Before(r.ExpiresAt) {
		return nil, logtosession.ErrNotFound
	}
	return r, nil
}

func (s *Store) WithSession(ctx context.Context, id string, now time.Time, fn func(*logtosession.Record) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r := &logtosession.Record{ID: id, Kind: "session"}
	err = tx.QueryRowContext(ctx, `SELECT ciphertext, created_at, expires_at FROM logto_sessions WHERE id = $1 AND kind = 'session' AND expires_at > $2 FOR UPDATE`, id, now).Scan(&r.Ciphertext, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return logtosession.ErrNotFound
	}
	if err != nil {
		return err
	}
	before := r.Ciphertext
	if err := fn(r); err != nil {
		return err
	}
	if r.Ciphertext != before {
		if _, err := tx.ExecContext(ctx, `UPDATE logto_sessions SET ciphertext = $2 WHERE id = $1`, id, r.Ciphertext); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM logto_sessions WHERE id = $1`, id)
	return err
}

func (s *Store) Purge(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM logto_sessions WHERE expires_at <= $1`, now)
	return err
}
