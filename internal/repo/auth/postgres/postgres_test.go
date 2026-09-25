package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/repo/auth"
	"go.orx.me/apps/neo-box/internal/repo/internal/pgtest"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// Postgres stores microseconds; truncate so round-tripped times compare equal.
var t0 = time.Now().UTC().Truncate(time.Microsecond)

func newUser(id, username string, at time.Time) *neoboxv1.User {
	return &neoboxv1.User{
		Id: id, Username: username, DisplayName: username, Role: "user",
		CreatedAt: timestamppb.New(at), UpdatedAt: timestamppb.New(at),
	}
}

func TestCreateAndFindUser(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	if err := s.CreateUser(ctx, newUser("u1", "alice", t0), "hash1"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, hash, err := s.FindUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("FindUserByUsername: %v", err)
	}
	if got.GetId() != "u1" || hash != "hash1" || !got.GetCreatedAt().AsTime().Equal(t0) {
		t.Fatalf("unexpected user: %+v hash=%q", got, hash)
	}
	if _, _, err := s.FindUserByID(ctx, "u1"); err != nil {
		t.Fatalf("FindUserByID: %v", err)
	}
	if _, err := s.GetUser(ctx, "missing"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("GetUser(missing) = %v, want ErrUserNotFound", err)
	}
	if n, err := s.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}
}

func TestCreateUserDuplicates(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	if err := s.CreateUser(ctx, newUser("u1", "alice", t0), "h"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := s.CreateUser(ctx, newUser("u2", "alice", t0), "h"); !errors.Is(err, auth.ErrUserAlreadyExists) {
		t.Fatalf("duplicate username = %v, want ErrUserAlreadyExists", err)
	}

	// Password users share an empty (provider, external_id); only OAuth
	// identities must be unique.
	if err := s.CreateUser(ctx, newUser("u3", "bob", t0), "h"); err != nil {
		t.Fatalf("second password user: %v", err)
	}
	gh := func(id, username string) *neoboxv1.User {
		u := newUser(id, username, t0)
		u.Provider, u.ExternalId = "github", "42"
		return u
	}
	if err := s.CreateUser(ctx, gh("u4", "gh-42"), ""); err != nil {
		t.Fatalf("oauth user: %v", err)
	}
	if err := s.CreateUser(ctx, gh("u5", "gh-42-again"), ""); !errors.Is(err, auth.ErrUserAlreadyExists) {
		t.Fatalf("duplicate external id = %v, want ErrUserAlreadyExists", err)
	}
	got, err := s.FindUserByExternalID(ctx, "github", "42")
	if err != nil || got.GetId() != "u4" {
		t.Fatalf("FindUserByExternalID = %+v, %v", got, err)
	}
	if _, err := s.FindUserByExternalID(ctx, "google", "42"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("FindUserByExternalID(google) = %v, want ErrUserNotFound", err)
	}
}

func TestListUsersOrderedByCreation(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	for i, name := range []string{"carol", "alice", "bob"} {
		if err := s.CreateUser(ctx, newUser(name, name, t0.Add(time.Duration(i)*time.Second)), ""); err != nil {
			t.Fatalf("CreateUser(%s): %v", name, err)
		}
	}
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.GetUsername())
	}
	if len(names) != 3 || names[0] != "carol" || names[1] != "alice" || names[2] != "bob" {
		t.Fatalf("ListUsers order = %v", names)
	}
}

func TestUpdateUser(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	u := newUser("u1", "alice", t0)
	u.AvatarUrl = "https://a/1.png"
	if err := s.CreateUser(ctx, u, "old"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	t1 := t0.Add(time.Minute)

	got, err := s.UpdateUserProfile(ctx, "u1", "Alice", nil, t1)
	if err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	if got.GetDisplayName() != "Alice" || got.GetAvatarUrl() != "https://a/1.png" || !got.GetUpdatedAt().AsTime().Equal(t1) {
		t.Fatalf("nil avatar should leave it untouched: %+v", got)
	}
	empty := ""
	if got, err = s.UpdateUserProfile(ctx, "u1", "Alice", &empty, t1); err != nil || got.GetAvatarUrl() != "" {
		t.Fatalf("empty avatar should clear it: %+v, %v", got, err)
	}

	if _, err := s.UpdateUserPassword(ctx, "u1", "new", t1); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if _, hash, _ := s.FindUserByID(ctx, "u1"); hash != "new" {
		t.Fatalf("password hash = %q, want new", hash)
	}
	if got, err = s.SetUserDisabled(ctx, "u1", true, t1); err != nil || !got.GetDisabled() {
		t.Fatalf("SetUserDisabled: %+v, %v", got, err)
	}

	if _, err := s.UpdateUserProfile(ctx, "missing", "x", nil, t1); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("UpdateUserProfile(missing) = %v", err)
	}
	if _, err := s.UpdateUserPassword(ctx, "missing", "x", t1); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("UpdateUserPassword(missing) = %v", err)
	}
	if _, err := s.SetUserDisabled(ctx, "missing", true, t1); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("SetUserDisabled(missing) = %v", err)
	}
}

func TestSessions(t *testing.T) {
	s := New(pgtest.NewClient(t))
	ctx := context.Background()

	if err := s.CreateUser(ctx, newUser("u1", "alice", t0), ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	create := func(id, hash string, expires time.Time) {
		t.Helper()
		err := s.CreateSession(ctx, &auth.Session{ID: id, UserID: "u1", TokenHash: hash, CreatedAt: t0, ExpiresAt: expires})
		if err != nil {
			t.Fatalf("CreateSession(%s): %v", id, err)
		}
	}
	create("live", "h-live", t0.Add(time.Hour))
	create("expired", "h-expired", t0.Add(-time.Hour))
	create("revoked", "h-revoked", t0.Add(time.Hour))
	if err := s.RevokeSession(ctx, "revoked"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}

	sess, u, err := s.LookupSession(ctx, "h-live", t0)
	if err != nil {
		t.Fatalf("LookupSession(live): %v", err)
	}
	if sess.ID != "live" || u.GetId() != "u1" || !sess.LastUsedAt.IsZero() {
		t.Fatalf("unexpected session %+v user %+v", sess, u)
	}
	for _, hash := range []string{"h-expired", "h-revoked", "h-unknown"} {
		if _, _, err := s.LookupSession(ctx, hash, t0); !errors.Is(err, auth.ErrSessionNotFound) {
			t.Fatalf("LookupSession(%s) = %v, want ErrSessionNotFound", hash, err)
		}
	}

	touched := t0.Add(time.Minute)
	if err := s.TouchSession(ctx, "live", touched); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	if sess, _, _ = s.LookupSession(ctx, "h-live", t0); !sess.LastUsedAt.Equal(touched) {
		t.Fatalf("LastUsedAt = %v, want %v", sess.LastUsedAt, touched)
	}

	if _, err := s.SetUserDisabled(ctx, "u1", true, t0); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, _, err := s.LookupSession(ctx, "h-live", t0); !errors.Is(err, auth.ErrUserDisabled) {
		t.Fatalf("LookupSession(disabled user) = %v, want ErrUserDisabled", err)
	}

	n, err := s.DeleteExpiredSessions(ctx, t0)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpiredSessions = %d, %v; want 1", n, err)
	}
}
