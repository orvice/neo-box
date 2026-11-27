package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	repo "go.orx.me/apps/neo-box/internal/repo/telegram"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func TestBindingCRUDAndConflicts(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	b := &repo.Binding{
		ID:               "b1",
		UserID:           "u1",
		TelegramUserID:   1001,
		ChatID:           1001,
		TelegramUsername: "alice",
		DisplayName:      "Alice",
		CreatedAt:        t0,
		UpdatedAt:        t0,
	}
	if err := s.SaveBinding(ctx, b); err != nil {
		t.Fatalf("SaveBinding: %v", err)
	}

	got, err := s.GetBindingByUser(ctx, "u1")
	if err != nil || got.TelegramUserID != 1001 || got.TelegramUsername != "alice" {
		t.Fatalf("GetBindingByUser = %+v, %v", got, err)
	}
	got, err = s.GetBindingByTelegramUser(ctx, 1001)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("GetBindingByTelegramUser = %+v, %v", got, err)
	}

	// Another Neo Box user cannot claim the same Telegram account.
	other := &repo.Binding{
		ID: "b2", UserID: "u2", TelegramUserID: 1001, ChatID: 1001,
		CreatedAt: t0, UpdatedAt: t0,
	}
	if err := s.SaveBinding(ctx, other); !errors.Is(err, repo.ErrTelegramUserAlreadyBound) {
		t.Fatalf("SaveBinding conflict = %v, want ErrTelegramUserAlreadyBound", err)
	}

	// Same user updating metadata for the same Telegram account succeeds.
	b.TelegramUsername = "alice_new"
	b.UpdatedAt = t0.Add(time.Minute)
	if err := s.SaveBinding(ctx, b); err != nil {
		t.Fatalf("SaveBinding update: %v", err)
	}
	got, err = s.GetBindingByUser(ctx, "u1")
	if err != nil || got.TelegramUsername != "alice_new" {
		t.Fatalf("GetBindingByUser after update = %+v, %v", got, err)
	}

	if err := s.DeleteBindingByUser(ctx, "u1"); err != nil {
		t.Fatalf("DeleteBindingByUser: %v", err)
	}
	if _, err := s.GetBindingByUser(ctx, "u1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("after delete = %v, want ErrNotFound", err)
	}
}

func TestLinkTokenSingleUseAndExpiry(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	tok := &repo.LinkToken{
		TokenHash: "hash1",
		UserID:    "u1",
		CreatedAt: t0,
		ExpiresAt: t0.Add(10 * time.Minute),
	}
	if err := s.CreateLinkToken(ctx, tok); err != nil {
		t.Fatalf("CreateLinkToken: %v", err)
	}

	got, err := s.ConsumeLinkToken(ctx, "hash1", t0)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("ConsumeLinkToken = %+v, %v", got, err)
	}
	if _, err := s.ConsumeLinkToken(ctx, "hash1", t0); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("second ConsumeLinkToken = %v, want ErrNotFound", err)
	}

	for _, e := range []*repo.LinkToken{
		{TokenHash: "expired", UserID: "u1", CreatedAt: t0.Add(-time.Hour), ExpiresAt: t0.Add(-time.Minute)},
		{TokenHash: "live", UserID: "u1", CreatedAt: t0, ExpiresAt: t0.Add(time.Minute)},
	} {
		if err := s.CreateLinkToken(ctx, e); err != nil {
			t.Fatalf("CreateLinkToken(%s): %v", e.TokenHash, err)
		}
	}
	if _, err := s.ConsumeLinkToken(ctx, "expired", t0); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("ConsumeLinkToken(expired) = %v, want ErrNotFound", err)
	}
	n, err := s.DeleteExpiredLinkTokens(ctx, t0)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpiredLinkTokens = %d, %v; want 1", n, err)
	}
	if _, err := s.ConsumeLinkToken(ctx, "live", t0); err != nil {
		t.Fatalf("ConsumeLinkToken(live): %v", err)
	}
}
