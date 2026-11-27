// Package memory provides an in-memory telegram.Repository for tests.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/telegram"
)

type Store struct {
	mu       sync.RWMutex
	byUser   map[string]*repo.Binding
	byTGUser map[int64]*repo.Binding
	tokens   map[string]*repo.LinkToken
}

var _ repo.Repository = (*Store)(nil)

func New() *Store {
	return &Store{
		byUser:   make(map[string]*repo.Binding),
		byTGUser: make(map[int64]*repo.Binding),
		tokens:   make(map[string]*repo.LinkToken),
	}
}

func (s *Store) GetBindingByUser(_ context.Context, userID string) (*repo.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.byUser[userID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *Store) GetBindingByTelegramUser(_ context.Context, telegramUserID int64) (*repo.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.byTGUser[telegramUserID]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *Store) SaveBinding(_ context.Context, b *repo.Binding) error {
	if b == nil || b.UserID == "" || b.TelegramUserID == 0 {
		return errors.New("telegram binding invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.byTGUser[b.TelegramUserID]; ok && existing.UserID != b.UserID {
		return repo.ErrTelegramUserAlreadyBound
	}
	if prev, ok := s.byUser[b.UserID]; ok && prev.TelegramUserID != b.TelegramUserID {
		delete(s.byTGUser, prev.TelegramUserID)
	}
	cp := *b
	s.byUser[b.UserID] = &cp
	s.byTGUser[b.TelegramUserID] = &cp
	return nil
}

func (s *Store) DeleteBindingByUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byUser[userID]
	if !ok {
		return repo.ErrNotFound
	}
	delete(s.byUser, userID)
	delete(s.byTGUser, b.TelegramUserID)
	return nil
}

func (s *Store) DeleteBindingByTelegramUser(_ context.Context, telegramUserID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byTGUser[telegramUserID]
	if !ok {
		return repo.ErrNotFound
	}
	delete(s.byTGUser, telegramUserID)
	delete(s.byUser, b.UserID)
	return nil
}

func (s *Store) CreateLinkToken(_ context.Context, tok *repo.LinkToken) error {
	if tok == nil || tok.TokenHash == "" || tok.UserID == "" {
		return errors.New("telegram link token invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tokens[tok.TokenHash]; exists {
		return errors.New("duplicate link token")
	}
	cp := *tok
	s.tokens[tok.TokenHash] = &cp
	return nil
}

func (s *Store) ConsumeLinkToken(_ context.Context, tokenHash string, now time.Time) (*repo.LinkToken, error) {
	if tokenHash == "" {
		return nil, repo.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.tokens[tokenHash]
	if !ok {
		return nil, repo.ErrNotFound
	}
	if !tok.ExpiresAt.After(now) {
		return nil, repo.ErrNotFound
	}
	delete(s.tokens, tokenHash)
	cp := *tok
	return &cp, nil
}

func (s *Store) DeleteExpiredLinkTokens(_ context.Context, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, tok := range s.tokens {
		if tok.ExpiresAt.Before(now) {
			delete(s.tokens, k)
			n++
		}
	}
	return n, nil
}
