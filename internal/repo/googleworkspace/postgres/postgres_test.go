package postgres

import (
	"context"
	"errors"
	repo "go.orx.me/apps/neo-box/internal/repo/googleworkspace"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	"testing"
	"time"
)

func TestTenantOwnershipUniquenessAndIndependentHealth(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := &repo.Tenant{ID: "a", UserID: "u1", ConnectionID: "shared", CustomerID: "Ca", Name: "A", DelegatedAdminEmail: "admin@a.example", Status: "ok", StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now}
	b := *a
	b.ID, b.CustomerID, b.Name, b.DelegatedAdminEmail = "b", "Cb", "B", "admin@b.example"
	for _, tnt := range []*repo.Tenant{a, &b} {
		if err := s.Create(ctx, tnt); err != nil {
			t.Fatal(err)
		}
	}
	duplicate := *a
	duplicate.ID = "duplicate"
	if err := s.Create(ctx, &duplicate); !errors.Is(err, repo.ErrDuplicate) {
		t.Fatalf("duplicate=%v", err)
	}
	for _, pair := range [][2]string{{"u2", "shared"}, {"u1", "other"}} {
		if _, err := s.Get(ctx, pair[0], pair[1], "a"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("unscoped get=%v", err)
		}
		if err := s.Delete(ctx, pair[0], pair[1], "a"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("unscoped delete=%v", err)
		}
	}
	a.Status, a.StatusMessage = "error", "authorization revoked"
	if err := s.SetStatus(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "u1", "shared", "b")
	if err != nil || got.Status != "ok" {
		t.Fatalf("other tenant health changed: %v %v", got, err)
	}
	if err := s.Delete(ctx, "u1", "shared", "a"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, "u1", "shared", "b")
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "Renamed"
	got.DelegatedAdminEmail = "other@b.example"
	if err = s.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteConnectionData(ctx, "shared"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "u1", "shared")
	if err != nil || len(list) != 0 {
		t.Fatalf("cleanup=%v %v", list, err)
	}
}
