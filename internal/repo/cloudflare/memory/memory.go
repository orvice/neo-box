// Package memory is an in-memory cloudflare.Repository for tests.
package memory

import (
	"context"
	"errors"
	"sort"
	"sync"

	repo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

type Store struct {
	mu  sync.Mutex
	ops map[string]*repo.DNSOperation

	// FailCreate and FailFinish make the next calls fail, to test what
	// happens when the operation log can't be written.
	FailCreate, FailFinish bool
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{ops: make(map[string]*repo.DNSOperation)}
}

var errInjected = errors.New("injected repository failure")

func (s *Store) CreateDNSOperation(_ context.Context, op *repo.DNSOperation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailCreate {
		return errInjected
	}
	cp := *op
	s.ops[op.ID] = &cp
	return nil
}

func (s *Store) FinishDNSOperation(_ context.Context, op *repo.DNSOperation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.FailFinish {
		return errInjected
	}
	cur, ok := s.ops[op.ID]
	if !ok {
		return repo.ErrNotFound
	}
	cur.RecordID, cur.Status, cur.Error, cur.After, cur.FinishedAt = op.RecordID, op.Status, op.Error, op.After, op.FinishedAt
	return nil
}

func (s *Store) ListDNSOperations(_ context.Context, f repo.Filter) ([]*repo.DNSOperation, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []*repo.DNSOperation
	for _, op := range s.ops {
		if op.UserID == f.UserID && op.ConnectionID == f.ConnectionID && (f.ZoneID == "" || op.ZoneID == f.ZoneID) {
			cp := *op
			all = append(all, &cp)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	total := len(all)
	if f.Offset >= total {
		return nil, total, nil
	}
	all = all[f.Offset:]
	if f.Limit > 0 && len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, total, nil
}

func (s *Store) DeleteConnectionData(_ context.Context, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, op := range s.ops {
		if op.ConnectionID == connectionID {
			delete(s.ops, id)
		}
	}
	return nil
}
