package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	"go.orx.me/apps/neo-box/internal/repo/oauthstate"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func TestConsumeIsSingleUse(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	entry := &oauthstate.Entry{State: "st", Provider: "github", RedirectURI: "/cb", CreatedAt: t0, ExpiresAt: t0.Add(10 * time.Minute)}
	if err := s.Create(ctx, entry); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Create(ctx, entry); err == nil {
		t.Fatal("Create duplicate: expected error")
	}

	got, err := s.Consume(ctx, "st", t0)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Provider != "github" || got.RedirectURI != "/cb" || !got.ExpiresAt.Equal(entry.ExpiresAt) {
		t.Fatalf("unexpected entry: %+v", got)
	}
	if _, err := s.Consume(ctx, "st", t0); !errors.Is(err, oauthstate.ErrNotFound) {
		t.Fatalf("second Consume = %v, want ErrNotFound", err)
	}
	if _, err := s.Consume(ctx, "", t0); !errors.Is(err, oauthstate.ErrNotFound) {
		t.Fatalf("Consume(empty) = %v, want ErrNotFound", err)
	}
}

func TestExpiredStates(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	for _, e := range []*oauthstate.Entry{
		{State: "old", Provider: "github", CreatedAt: t0.Add(-time.Hour), ExpiresAt: t0.Add(-time.Minute)},
		{State: "new", Provider: "github", CreatedAt: t0, ExpiresAt: t0.Add(time.Minute)},
	} {
		if err := s.Create(ctx, e); err != nil {
			t.Fatalf("Create(%s): %v", e.State, err)
		}
	}
	if _, err := s.Consume(ctx, "old", t0); !errors.Is(err, oauthstate.ErrNotFound) {
		t.Fatalf("Consume(expired) = %v, want ErrNotFound", err)
	}
	n, err := s.DeleteExpired(ctx, t0)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired = %d, %v; want 1", n, err)
	}
	if _, err := s.Consume(ctx, "new", t0); err != nil {
		t.Fatalf("Consume(new) after purge: %v", err)
	}
}
