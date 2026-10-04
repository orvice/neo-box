// Package memory is an in-memory notify.Repository for tests.
package memory

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/notify"
)

type Store struct {
	mu       sync.Mutex
	channels map[string]*repo.Channel
	alerts   map[string]*repo.Alert
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{channels: map[string]*repo.Channel{}, alerts: map[string]*repo.Alert{}}
}

func (s *Store) CreateChannel(_ context.Context, c *repo.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	cp.Config = slices.Clone(c.Config)
	s.channels[c.ID] = &cp
	return nil
}

func (s *Store) GetChannel(_ context.Context, userID, id string) (*repo.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channels[id]
	if !ok || c.UserID != userID {
		return nil, repo.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *Store) UpdateChannel(_ context.Context, c *repo.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.channels[c.ID]
	if !ok || old.UserID != c.UserID {
		return repo.ErrNotFound
	}
	old.Name, old.Enabled, old.Config, old.SecretCiphertext, old.UpdatedAt =
		c.Name, c.Enabled, slices.Clone(c.Config), c.SecretCiphertext, c.UpdatedAt
	return nil
}

func (s *Store) DeleteChannel(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channels[id]
	if !ok || c.UserID != userID {
		return repo.ErrNotFound
	}
	delete(s.channels, id)
	return nil
}

func (s *Store) ListChannels(_ context.Context, userID string) ([]*repo.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Channel
	for _, c := range s.channels {
		if c.UserID == userID {
			cp := *c
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) RecordChannelResult(_ context.Context, id string, sentAt time.Time, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.channels[id]; ok {
		c.LastError = errMsg
		if errMsg == "" {
			c.LastSentAt = sentAt
		}
	}
	return nil
}

func (s *Store) CreateAlert(_ context.Context, a *repo.Alert) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.alerts {
		if old.UserID == a.UserID && old.Key == a.Key {
			return false, nil
		}
	}
	cp := *a
	s.alerts[a.ID] = &cp
	return true, nil
}

func (s *Store) GetAlert(_ context.Context, id string) (*repo.Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.alerts[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *Store) SetAlertDelivery(_ context.Context, id string, deliveredAt time.Time, deliveryError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.alerts[id]
	if !ok {
		return repo.ErrNotFound
	}
	a.DeliveredAt, a.DeliveryError = deliveredAt, deliveryError
	return nil
}

func (s *Store) ListAlerts(_ context.Context, userID string, limit int) ([]*repo.Alert, error) {
	out := s.find(func(a *repo.Alert) bool { return a.UserID == userID })
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ListUndelivered(_ context.Context, since time.Time) ([]*repo.Alert, error) {
	out := s.find(func(a *repo.Alert) bool {
		return !a.CreatedAt.Before(since) && a.DeliveredAt.IsZero() && a.DeliveryError == ""
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) find(keep func(*repo.Alert) bool) []*repo.Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*repo.Alert
	for _, a := range s.alerts {
		if keep(a) {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out
}
