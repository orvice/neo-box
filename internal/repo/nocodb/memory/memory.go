// Package memory is an in-memory nocodb.Repository for tests.
package memory

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/nocodb"
)

type Store struct {
	mu       sync.Mutex
	policies map[string]*repo.Policy
	snaps    map[string]*repo.Snapshot
	restores map[string]*repo.Restore
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{
		policies: map[string]*repo.Policy{},
		snaps:    map[string]*repo.Snapshot{},
		restores: map[string]*repo.Restore{},
	}
}

// --- policies ---

func (s *Store) UpsertPolicy(_ context.Context, p *repo.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *p
	s.policies[p.ConnectionID+"/"+p.BaseID] = &cp
	return nil
}

func (s *Store) ListPolicies(_ context.Context, userID, connectionID string) ([]*repo.Policy, error) {
	return s.findPolicies(func(p *repo.Policy) bool { return p.UserID == userID && p.ConnectionID == connectionID }), nil
}

func (s *Store) ListEnabledPolicies(context.Context) ([]*repo.Policy, error) {
	return s.findPolicies(func(p *repo.Policy) bool { return p.Enabled }), nil
}

func (s *Store) findPolicies(match func(*repo.Policy) bool) []*repo.Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Policy
	for _, p := range s.policies {
		if match(p) {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out
}

func (s *Store) DeletePoliciesForConnection(_ context.Context, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.policies {
		if p.ConnectionID == connectionID {
			delete(s.policies, k)
		}
	}
	return nil
}

// --- snapshots ---

func (s *Store) CreateSnapshot(_ context.Context, snap *repo.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *snap
	s.snaps[snap.ID] = &cp
	return nil
}

func (s *Store) GetSnapshot(_ context.Context, userID, id string) (*repo.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snaps[id]
	if !ok || (userID != "" && snap.UserID != userID) {
		return nil, repo.ErrNotFound
	}
	cp := *snap
	return &cp, nil
}

func (s *Store) UpdateSnapshot(ctx context.Context, snap *repo.Snapshot) error {
	return s.CreateSnapshot(ctx, snap)
}

func (s *Store) UpdateSnapshotProgress(_ context.Context, id, progress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap, ok := s.snaps[id]; ok {
		snap.Progress = progress
	}
	return nil
}

func (s *Store) ListSnapshots(_ context.Context, f repo.SnapshotFilter) ([]*repo.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Snapshot
	for _, snap := range s.snaps {
		if (f.UserID == "" || snap.UserID == f.UserID) &&
			(f.ConnectionID == "" || snap.ConnectionID == f.ConnectionID) &&
			(f.BaseID == "" || snap.BaseID == f.BaseID) &&
			(f.Trigger == "" || snap.Trigger == f.Trigger) &&
			(f.Status == "" || snap.Status == f.Status) {
			cp := *snap
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) DeleteSnapshot(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snaps, id)
	return nil
}

func (s *Store) FailUnfinishedSnapshots(_ context.Context, reason string, at time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, snap := range s.snaps {
		if snap.Status == repo.StatusPending || snap.Status == repo.StatusRunning {
			snap.Status, snap.Error, snap.FinishedAt, snap.Progress = repo.StatusFailed, reason, at, ""
			n++
		}
	}
	return n, nil
}

// --- restores ---

func (s *Store) CreateRestore(_ context.Context, r *repo.Restore) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	cp.Warnings = slices.Clone(r.Warnings)
	s.restores[r.ID] = &cp
	return nil
}

func (s *Store) GetRestore(_ context.Context, userID, id string) (*repo.Restore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.restores[id]
	if !ok || (userID != "" && r.UserID != userID) {
		return nil, repo.ErrNotFound
	}
	cp := *r
	cp.Warnings = slices.Clone(r.Warnings)
	return &cp, nil
}

func (s *Store) UpdateRestore(ctx context.Context, r *repo.Restore) error {
	s.mu.Lock()
	_, ok := s.restores[r.ID]
	s.mu.Unlock()
	if !ok {
		return repo.ErrNotFound
	}
	return s.CreateRestore(ctx, r)
}

func (s *Store) UpdateRestoreProgress(_ context.Context, id, progress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.restores[id]; ok {
		r.Progress = progress
	}
	return nil
}

func (s *Store) UpdateRestoreTargetBase(_ context.Context, id, baseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.restores[id]; ok {
		r.TargetBaseID = baseID
	}
	return nil
}

func (s *Store) ListRestores(_ context.Context, f repo.RestoreFilter) ([]*repo.Restore, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Restore
	for _, r := range s.restores {
		if (f.UserID == "" || r.UserID == f.UserID) &&
			(f.SnapshotID == "" || r.SnapshotID == f.SnapshotID) &&
			(f.ConnectionID == "" || r.SourceConnectionID == f.ConnectionID || r.TargetConnectionID == f.ConnectionID) &&
			(!f.ActiveOnly || r.Active()) {
			cp := *r
			cp.Warnings = slices.Clone(r.Warnings)
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) DeleteRestoresForConnection(_ context.Context, connectionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.restores {
		if r.SourceConnectionID == connectionID || r.TargetConnectionID == connectionID {
			delete(s.restores, id)
		}
	}
	return nil
}

func (s *Store) FailUnfinishedRestores(_ context.Context, reason string, at time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, r := range s.restores {
		if r.Active() {
			r.Status, r.Error, r.FinishedAt, r.Progress = repo.RestoreFailed, reason, at, ""
			n++
		}
	}
	return n, nil
}
