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

func TestOperationsAreOwnedRetainedAfterTenantRemovalAndCleanedWithConnection(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenant := &repo.Tenant{ID: "tenant", UserID: "u1", ConnectionID: "c1", CustomerID: "Ca", Name: "Tenant A", DelegatedAdminEmail: "admin@a.example", Status: "ok", StatusCheckedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := s.Create(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	makeOperation := func(id, user, connection string) *repo.Operation {
		return &repo.Operation{ID: id, UserID: user, ConnectionID: connection, TenantID: "tenant", TenantName: "Tenant A", CustomerID: "Ca", Action: "user_password_reset", Target: "PATCH /users/user-id", Requested: map[string]any{"password_changed": true}, Status: "pending", ActorID: user, ActorName: "Owner", CreatedAt: now}
	}
	a, b := makeOperation("a", "u1", "c1"), makeOperation("b", "u2", "c2")
	for _, o := range []*repo.Operation{a, b} {
		if err := s.CreateOperation(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	wrong := *a
	wrong.UserID = "u2"
	wrong.Status = "succeeded"
	wrong.FinishedAt = now.Add(time.Second)
	if err := s.FinishOperation(ctx, &wrong); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("other owner's outcome update=%v", err)
	}
	a.Status = "unknown"
	a.Error = "Google did not confirm"
	a.FinishedAt = now.Add(time.Second)
	if err := s.FinishOperation(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishOperation(ctx, a); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("terminal status overwritten=%v", err)
	}
	rows, total, err := s.ListOperations(ctx, repo.OperationFilter{UserID: "u1", ConnectionID: "c1", Query: "PASSWORD", Status: "unknown", Limit: 20})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Requested["password_changed"] != true || rows[0].FinishedAt.IsZero() {
		t.Fatalf("roundtrip=%v total=%d err=%v", rows, total, err)
	}
	rows, total, err = s.ListOperations(ctx, repo.OperationFilter{UserID: "u2", ConnectionID: "c1", Limit: 20})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatal("other owner read operation history")
	}
	if err = s.Delete(ctx, "u1", "c1", "tenant"); err != nil {
		t.Fatal(err)
	}
	rows, total, err = s.ListOperations(ctx, repo.OperationFilter{UserID: "u1", ConnectionID: "c1", TenantID: "tenant", Limit: 20})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatal("tenant removal deleted history")
	}
	if err = s.DeleteConnectionData(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	_, total, err = s.ListOperations(ctx, repo.OperationFilter{UserID: "u1", ConnectionID: "c1", Limit: 20})
	if err != nil || total != 0 {
		t.Fatal("connection cleanup left operations")
	}
	_, total, err = s.ListOperations(ctx, repo.OperationFilter{UserID: "u2", ConnectionID: "c2", Limit: 20})
	if err != nil || total != 1 {
		t.Fatal("cleanup affected another connection")
	}
}
