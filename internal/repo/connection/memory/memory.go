// Package memory is an in-memory connection.Repository for tests.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/connection"
)

type Store struct {
	mu    sync.Mutex
	conns map[string]*repo.Connection
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{conns: make(map[string]*repo.Connection)}
}

func (s *Store) Create(_ context.Context, c *repo.Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.conns[c.ID] = &cp
	return nil
}

func (s *Store) Get(ctx context.Context, userID, id string) (*repo.Connection, error) {
	c, err := s.GetByID(ctx, id)
	if err != nil || c.UserID != userID {
		return nil, repo.ErrNotFound
	}
	return c, nil
}

func (s *Store) GetByID(_ context.Context, id string) (*repo.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) List(_ context.Context, f repo.Filter) ([]*repo.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Connection
	for _, c := range s.conns {
		if (f.UserID == "" || c.UserID == f.UserID) && (f.Provider == "" || c.Provider == f.Provider) {
			cp := *c
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) Update(_ context.Context, c *repo.Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.conns[c.ID]
	if !ok || old.UserID != c.UserID {
		return repo.ErrNotFound
	}
	cp := *c
	cp.Provider, cp.CreatedAt = old.Provider, old.CreatedAt
	s.conns[c.ID] = &cp
	return nil
}

func (s *Store) SetStatus(_ context.Context, id string, status repo.Status, message string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return repo.ErrNotFound
	}
	c.Status, c.StatusMessage, c.StatusCheckedAt = status, message, at
	return nil
}

func (s *Store) Delete(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok || c.UserID != userID {
		return repo.ErrNotFound
	}
	delete(s.conns, id)
	return nil
}
