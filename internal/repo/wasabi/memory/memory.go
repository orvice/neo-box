// Package memory is an in-memory wasabi.Repository for tests.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/wasabi"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

type key struct {
	connectionID, bucket string
	day                  time.Time
}

type Store struct {
	mu     sync.Mutex
	usage  map[key]wasabi.Usage
	states map[string]repo.SyncState
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{usage: make(map[key]wasabi.Usage), states: make(map[string]repo.SyncState)}
}

func (s *Store) UpsertUsage(_ context.Context, connectionID string, rows []wasabi.Usage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range rows {
		u.Day = wasabi.DayOf(u.Day)
		s.usage[key{connectionID, u.Bucket, u.Day}] = u
	}
	return nil
}

// rows returns the connection's rows matching keep, oldest first.
func (s *Store) rows(connectionID string, keep func(wasabi.Usage) bool) []wasabi.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []wasabi.Usage
	for k, u := range s.usage {
		if k.connectionID == connectionID && keep(u) {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].Bucket < out[j].Bucket
	})
	return out
}

func (s *Store) ListUsage(_ context.Context, connectionID, bucket string, from, to time.Time) ([]wasabi.Usage, error) {
	from, to = wasabi.DayOf(from), wasabi.DayOf(to)
	return s.rows(connectionID, func(u wasabi.Usage) bool {
		return u.Bucket == bucket && !u.Day.Before(from) && !u.Day.After(to)
	}), nil
}

func (s *Store) LatestBucketUsage(_ context.Context, connectionID string) ([]wasabi.Usage, error) {
	latest := map[string]wasabi.Usage{}
	for _, u := range s.rows(connectionID, func(u wasabi.Usage) bool { return u.Bucket != "" }) {
		latest[u.Bucket] = u // rows are oldest first
	}
	out := make([]wasabi.Usage, 0, len(latest))
	for _, u := range latest {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}

func (s *Store) LatestAccountUsage(_ context.Context, connectionID string) (*wasabi.Usage, error) {
	rows := s.rows(connectionID, func(u wasabi.Usage) bool { return u.Bucket == "" })
	if len(rows) == 0 {
		return nil, repo.ErrNotFound
	}
	return &rows[len(rows)-1], nil
}

func (s *Store) GetSyncState(_ context.Context, connectionID string) (*repo.SyncState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[connectionID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return &st, nil
}

func (s *Store) SaveSyncState(_ context.Context, st *repo.SyncState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[st.ConnectionID] = *st
	return nil
}

func (s *Store) DeleteConnectionData(_ context.Context, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.usage {
		if k.connectionID == connectionID {
			delete(s.usage, k)
		}
	}
	delete(s.states, connectionID)
	return nil
}
