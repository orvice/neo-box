package auth

import (
	"context"
	"errors"
	"time"

	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrSessionNotFound   = errors.New("auth session not found")
	ErrUserDisabled      = errors.New("user disabled")
)

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

type Repository interface {
	CountUsers(ctx context.Context) (int64, error)
	ListUsers(ctx context.Context) ([]*neoboxv1.User, error)
	CreateUser(ctx context.Context, user *neoboxv1.User, passwordHash string) error
	UpdateUserPassword(ctx context.Context, id string, passwordHash string, updatedAt time.Time) (*neoboxv1.User, error)
	// UpdateUserProfile updates the user's display name and (optionally)
	// avatar URL. If avatarURL is nil the stored avatar is left untouched;
	// pass a pointer to an empty string to clear it.
	UpdateUserProfile(ctx context.Context, id string, displayName string, avatarURL *string, updatedAt time.Time) (*neoboxv1.User, error)
	SetUserDisabled(ctx context.Context, id string, disabled bool, updatedAt time.Time) (*neoboxv1.User, error)
	FindUserByUsername(ctx context.Context, username string) (*neoboxv1.User, string, error)
	FindUserByID(ctx context.Context, id string) (*neoboxv1.User, string, error)
	// FindUserByExternalID looks up a user previously created via OAuth by
	// (provider, externalID). Returns ErrUserNotFound when the pair is unknown.
	FindUserByExternalID(ctx context.Context, provider, externalID string) (*neoboxv1.User, error)
	GetUser(ctx context.Context, id string) (*neoboxv1.User, error)
	CreateSession(ctx context.Context, session *Session) error
	LookupSession(ctx context.Context, tokenHash string, now time.Time) (*Session, *neoboxv1.User, error)
	TouchSession(ctx context.Context, id string, at time.Time) error
	RevokeSession(ctx context.Context, id string) error
}

type contextKey string

const (
	userContextKey    contextKey = "auth_user"
	sessionContextKey contextKey = "auth_session"
	adminContextKey   contextKey = "auth_admin"
)

func WithAuthenticated(ctx context.Context, user *neoboxv1.User, session *Session) context.Context {
	ctx = context.WithValue(ctx, userContextKey, user)
	ctx = context.WithValue(ctx, sessionContextKey, session)
	return ctx
}

func UserFromContext(ctx context.Context) (*neoboxv1.User, bool) {
	user, ok := ctx.Value(userContextKey).(*neoboxv1.User)
	return user, ok && user != nil
}

func SessionFromContext(ctx context.Context) (*Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(*Session)
	return session, ok && session != nil
}

// WithAdmin tags a context as belonging to an admin-equivalent caller: a
// session user with role "admin" or the local-dev unauthenticated path.
func WithAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, adminContextKey, true)
}

// IsAdmin reports whether the context was tagged by WithAdmin, or whether the
// authenticated user carries role "admin".
func IsAdmin(ctx context.Context) bool {
	if flag, ok := ctx.Value(adminContextKey).(bool); ok && flag {
		return true
	}
	if user, ok := UserFromContext(ctx); ok && user.GetRole() == "admin" {
		return true
	}
	return false
}
