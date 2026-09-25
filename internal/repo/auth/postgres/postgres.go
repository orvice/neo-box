package postgres

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/ent"
	"go.orx.me/apps/neo-box/internal/ent/session"
	"go.orx.me/apps/neo-box/internal/ent/user"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type Store struct {
	client *ent.Client
}

var _ auth.Repository = (*Store)(nil)

func New(client *ent.Client) *Store {
	return &Store{client: client}
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	count, err := s.client.User.Query().Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return int64(count), nil
}

func (s *Store) ListUsers(ctx context.Context) ([]*neoboxv1.User, error) {
	rows, err := s.client.User.Query().Order(ent.Asc(user.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]*neoboxv1.User, 0, len(rows))
	for _, row := range rows {
		users = append(users, userToProto(row))
	}
	return users, nil
}

func (s *Store) CreateUser(ctx context.Context, u *neoboxv1.User, passwordHash string) error {
	err := s.client.User.Create().
		SetID(u.GetId()).
		SetUsername(u.GetUsername()).
		SetDisplayName(u.GetDisplayName()).
		SetAvatarURL(u.GetAvatarUrl()).
		SetEmail(u.GetEmail()).
		SetProvider(u.GetProvider()).
		SetExternalID(u.GetExternalId()).
		SetPasswordHash(passwordHash).
		SetRole(u.GetRole()).
		SetDisabled(u.GetDisabled()).
		SetCreatedAt(u.GetCreatedAt().AsTime()).
		SetUpdatedAt(u.GetUpdatedAt().AsTime()).
		Exec(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return auth.ErrUserAlreadyExists
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Store) FindUserByExternalID(ctx context.Context, provider, externalID string) (*neoboxv1.User, error) {
	if provider == "" || externalID == "" {
		return nil, auth.ErrUserNotFound
	}
	row, err := s.client.User.Query().
		Where(user.Provider(provider), user.ExternalID(externalID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("find user by external id: %w", err)
	}
	return userToProto(row), nil
}

func (s *Store) UpdateUserProfile(ctx context.Context, id string, displayName string, avatarURL *string, updatedAt time.Time) (*neoboxv1.User, error) {
	row, err := s.client.User.UpdateOneID(id).
		SetDisplayName(displayName).
		SetNillableAvatarURL(avatarURL).
		SetUpdatedAt(updatedAt).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("update user profile: %w", err)
	}
	return userToProto(row), nil
}

func (s *Store) UpdateUserPassword(ctx context.Context, id string, passwordHash string, updatedAt time.Time) (*neoboxv1.User, error) {
	row, err := s.client.User.UpdateOneID(id).
		SetPasswordHash(passwordHash).
		SetUpdatedAt(updatedAt).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("update user password: %w", err)
	}
	return userToProto(row), nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool, updatedAt time.Time) (*neoboxv1.User, error) {
	row, err := s.client.User.UpdateOneID(id).
		SetDisabled(disabled).
		SetUpdatedAt(updatedAt).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, auth.ErrUserNotFound
		}
		return nil, fmt.Errorf("set user disabled: %w", err)
	}
	return userToProto(row), nil
}

func (s *Store) FindUserByUsername(ctx context.Context, username string) (*neoboxv1.User, string, error) {
	row, err := s.client.User.Query().Where(user.Username(username)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", auth.ErrUserNotFound
		}
		return nil, "", fmt.Errorf("find user by username: %w", err)
	}
	return userToProto(row), row.PasswordHash, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (*neoboxv1.User, error) {
	u, _, err := s.FindUserByID(ctx, id)
	return u, err
}

func (s *Store) FindUserByID(ctx context.Context, id string) (*neoboxv1.User, string, error) {
	row, err := s.client.User.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, "", auth.ErrUserNotFound
		}
		return nil, "", fmt.Errorf("find user by id: %w", err)
	}
	return userToProto(row), row.PasswordHash, nil
}

func (s *Store) CreateSession(ctx context.Context, sess *auth.Session) error {
	err := s.client.Session.Create().
		SetID(sess.ID).
		SetUserID(sess.UserID).
		SetTokenHash(sess.TokenHash).
		SetCreatedAt(sess.CreatedAt).
		SetExpiresAt(sess.ExpiresAt).
		SetNillableLastUsedAt(nilIfZero(sess.LastUsedAt)).
		SetRevoked(sess.Revoked).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert auth_session: %w", err)
	}
	return nil
}

func (s *Store) LookupSession(ctx context.Context, tokenHash string, now time.Time) (*auth.Session, *neoboxv1.User, error) {
	row, err := s.client.Session.Query().
		Where(
			session.TokenHash(tokenHash),
			session.Revoked(false),
			session.ExpiresAtGT(now),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil, auth.ErrSessionNotFound
		}
		return nil, nil, fmt.Errorf("lookup auth_session: %w", err)
	}

	u, err := s.GetUser(ctx, row.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u.GetDisabled() {
		return nil, nil, auth.ErrUserDisabled
	}
	return sessionToModel(row), u, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	return s.client.Session.Update().Where(session.ID(id)).SetLastUsedAt(at).Exec(ctx)
}

func (s *Store) RevokeSession(ctx context.Context, id string) error {
	return s.client.Session.Update().Where(session.ID(id)).SetRevoked(true).Exec(ctx)
}

// DeleteExpiredSessions removes sessions that expired before now. Lookups
// already ignore them; this only keeps the table small.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	n, err := s.client.Session.Delete().Where(session.ExpiresAtLT(now)).Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired auth_sessions: %w", err)
	}
	return n, nil
}

func userToProto(row *ent.User) *neoboxv1.User {
	return &neoboxv1.User{
		Id:          row.ID,
		Username:    row.Username,
		DisplayName: row.DisplayName,
		AvatarUrl:   row.AvatarURL,
		Email:       row.Email,
		Provider:    row.Provider,
		ExternalId:  row.ExternalID,
		Role:        row.Role,
		Disabled:    row.Disabled,
		CreatedAt:   timestamppb.New(row.CreatedAt),
		UpdatedAt:   timestamppb.New(row.UpdatedAt),
	}
}

func sessionToModel(row *ent.Session) *auth.Session {
	return &auth.Session{
		ID:         row.ID,
		UserID:     row.UserID,
		TokenHash:  row.TokenHash,
		CreatedAt:  row.CreatedAt,
		ExpiresAt:  row.ExpiresAt,
		LastUsedAt: zeroIfNil(row.LastUsedAt),
		Revoked:    row.Revoked,
	}
}

func nilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func zeroIfNil(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
