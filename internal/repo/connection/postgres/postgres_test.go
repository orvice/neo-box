package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	repo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
)

var t0 = time.Now().UTC().Truncate(time.Microsecond)

func newConn(id, userID, provider string, at time.Time) *repo.Connection {
	return &repo.Connection{
		ID: id, UserID: userID, Provider: provider, Name: id,
		Config:           json.RawMessage(`{"base_url":"https://noco"}`),
		SecretCiphertext: "sealed-" + id,
		Status:           repo.StatusOK, StatusCheckedAt: at,
		CreatedAt: at, UpdatedAt: at,
	}
}

func TestConnectionsAreOwnerScoped(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	for _, c := range []*repo.Connection{
		newConn("c1", "u1", "nocodb", t0),
		newConn("c2", "u1", "wasabi", t0.Add(time.Second)),
		newConn("c3", "u2", "nocodb", t0.Add(2*time.Second)),
	} {
		if err := s.Create(ctx, c); err != nil {
			t.Fatalf("Create(%s): %v", c.ID, err)
		}
	}

	got, err := s.Get(ctx, "u1", "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var cfg map[string]string
	if err := json.Unmarshal(got.Config, &cfg); err != nil || cfg["base_url"] != "https://noco" {
		t.Fatalf("config round trip = %s, %v", got.Config, err)
	}
	if got.SecretCiphertext != "sealed-c1" || got.Status != repo.StatusOK || !got.StatusCheckedAt.Equal(t0) {
		t.Fatalf("unexpected connection: %+v", got)
	}
	if _, err := s.Get(ctx, "u2", "c1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Get(other user) = %v, want ErrNotFound", err)
	}
	if _, err := s.GetByID(ctx, "c1"); err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	ids := func(f repo.Filter) []string {
		t.Helper()
		list, err := s.List(ctx, f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		var out []string
		for _, c := range list {
			out = append(out, c.ID)
		}
		return out
	}
	if got := ids(repo.Filter{UserID: "u1"}); len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("List(u1) = %v", got)
	}
	if got := ids(repo.Filter{UserID: "u1", Provider: "wasabi"}); len(got) != 1 || got[0] != "c2" {
		t.Fatalf("List(u1, wasabi) = %v", got)
	}
	if got := ids(repo.Filter{Provider: "nocodb"}); len(got) != 2 || got[0] != "c1" || got[1] != "c3" {
		t.Fatalf("List(nocodb) = %v", got)
	}

	if err := s.Delete(ctx, "u2", "c1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Delete(other user) = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "u1", "c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(ctx, "c1"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrNotFound", err)
	}
}

func TestUpdateAndStatus(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	c := newConn("c1", "u1", "nocodb", t0)
	if err := s.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	t1 := t0.Add(time.Minute)
	if err := s.SetStatus(ctx, "c1", repo.StatusError, "401 unauthorized", t1); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _ := s.Get(ctx, "u1", "c1")
	if got.Status != repo.StatusError || got.StatusMessage != "401 unauthorized" || !got.StatusCheckedAt.Equal(t1) {
		t.Fatalf("status not recorded: %+v", got)
	}
	if err := s.SetStatus(ctx, "missing", repo.StatusOK, "", t1); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("SetStatus(missing) = %v, want ErrNotFound", err)
	}

	c.Name = "renamed"
	c.Config = json.RawMessage(`{"base_url":"https://other"}`)
	c.SecretCiphertext = "sealed-new"
	c.Status, c.StatusMessage, c.StatusCheckedAt = repo.StatusOK, "", t1
	c.UpdatedAt = t1
	if err := s.Update(ctx, c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s.Get(ctx, "u1", "c1")
	if got.Name != "renamed" || got.SecretCiphertext != "sealed-new" || got.Status != repo.StatusOK ||
		got.StatusMessage != "" || !got.UpdatedAt.Equal(t1) || !got.CreatedAt.Equal(t0) {
		t.Fatalf("Update did not persist: %+v", got)
	}

	other := *c
	other.UserID = "u2"
	if err := s.Update(ctx, &other); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("Update(other user) = %v, want ErrNotFound", err)
	}
}
