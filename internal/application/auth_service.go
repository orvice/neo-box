package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"butterfly.orx.me/core/log"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/auth/provider"
	"go.orx.me/apps/neo-box/internal/config"
	"go.orx.me/apps/neo-box/internal/repo/auth"
	"go.orx.me/apps/neo-box/internal/repo/oauthstate"
	"go.orx.me/apps/neo-box/internal/transport/connectx"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const sessionSecretLen = 32

type AuthServiceServer struct {
	repo       auth.Repository
	sessionTTL time.Duration
	providers  *provider.Registry
	stateRepo  oauthstate.Repository
}

func NewAuthServiceServer(repo auth.Repository, sessionTTL time.Duration) *AuthServiceServer {
	if sessionTTL <= 0 {
		sessionTTL = 7 * 24 * time.Hour
	}
	return &AuthServiceServer{repo: repo, sessionTTL: sessionTTL}
}

func (s *AuthServiceServer) SetRepo(repo auth.Repository) {
	s.repo = repo
}

// SetSessionTTL overrides the session lifetime once config is loaded.
func (s *AuthServiceServer) SetSessionTTL(ttl time.Duration) {
	if ttl > 0 {
		s.sessionTTL = ttl
	}
}

func (s *AuthServiceServer) Login(ctx context.Context, req *neoboxv1.LoginRequest) (*neoboxv1.LoginResponse, error) {
	logger := log.FromContext(ctx)
	if s.repo == nil {
		logger.Warn("login rejected: auth store not available")
		return nil, connect.NewError(connect.CodeFailedPrecondition, "auth store not available")
	}
	username := strings.TrimSpace(req.GetUsername())
	if username == "" {
		return nil, connectx.RequiredArgument(ctx, "username")
	}
	if req.GetPassword() == "" {
		return nil, connectx.RequiredArgument(ctx, "password")
	}

	logger.Debug("login attempt", "username", username)

	user, passwordHash, err := s.repo.FindUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			logger.Info("login failed: unknown user", "username", username)
			return nil, connect.NewError(connect.CodeUnauthenticated, "invalid username or password")
		}
		logger.Error("login failed: user lookup error", "username", username, "err", err)
		return nil, connectx.InternalWith(err)
	}
	if user.GetDisabled() {
		logger.Info("login rejected: user disabled", "username", username, "user_id", user.GetId())
		return nil, connect.NewError(connect.CodePermissionDenied, "user disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.GetPassword())); err != nil {
		logger.Info("login failed: password mismatch", "username", username, "user_id", user.GetId())
		return nil, connect.NewError(connect.CodeUnauthenticated, "invalid username or password")
	}

	secret, err := generateSessionSecret()
	if err != nil {
		logger.Error("login failed: cannot generate session secret", "user_id", user.GetId(), "err", err)
		return nil, connectx.InternalWith(err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	session := &auth.Session{
		ID:        uuid.NewString(),
		UserID:    user.GetId(),
		TokenHash: HashAuthSessionToken(secret),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		logger.Error("login failed: cannot create session", "user_id", user.GetId(), "err", err)
		return nil, connectx.InternalWith(err)
	}

	logger.Info("login succeeded",
		"username", username,
		"user_id", user.GetId(),
		"role", user.GetRole(),
		"session_id", session.ID,
	)
	return &neoboxv1.LoginResponse{
		Token:     secret,
		User:      user,
		ExpiresAt: timestamppb.New(expiresAt),
	}, nil
}

func (s *AuthServiceServer) Me(ctx context.Context, _ *neoboxv1.MeRequest) (*neoboxv1.MeResponse, error) {
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	return &neoboxv1.MeResponse{User: user}, nil
}

func (s *AuthServiceServer) Logout(ctx context.Context, _ *neoboxv1.LogoutRequest) (*neoboxv1.LogoutResponse, error) {
	logger := log.FromContext(ctx)
	if s.repo == nil {
		return &neoboxv1.LogoutResponse{}, nil
	}
	session, ok := auth.SessionFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	if err := s.repo.RevokeSession(ctx, session.ID); err != nil {
		logger.Error("logout failed: revoke session", "session_id", session.ID, "user_id", session.UserID, "err", err)
		return nil, connectx.InternalWith(err)
	}
	logger.Info("logout succeeded", "session_id", session.ID, "user_id", session.UserID)
	return &neoboxv1.LogoutResponse{}, nil
}

func (s *AuthServiceServer) ListUsers(ctx context.Context, _ *neoboxv1.ListUsersRequest) (*neoboxv1.ListUsersResponse, error) {
	if err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	users, err := s.repo.ListUsers(ctx)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	return &neoboxv1.ListUsersResponse{Users: users}, nil
}

func (s *AuthServiceServer) CreateUser(ctx context.Context, req *neoboxv1.CreateUserRequest) (*neoboxv1.CreateUserResponse, error) {
	if err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	username := strings.TrimSpace(req.GetUsername())
	if username == "" {
		return nil, connectx.RequiredArgument(ctx, "username")
	}
	if req.GetPassword() == "" {
		return nil, connectx.RequiredArgument(ctx, "password")
	}
	role := normalizeRole(req.GetRole())
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), bcrypt.DefaultCost)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	now := time.Now().UTC()
	user := &neoboxv1.User{
		Id:          uuid.NewString(),
		Username:    username,
		DisplayName: strings.TrimSpace(req.GetDisplayName()),
		Role:        role,
		Disabled:    req.GetDisabled(),
		CreatedAt:   timestamppb.New(now),
		UpdatedAt:   timestamppb.New(now),
	}
	if user.GetDisplayName() == "" {
		user.DisplayName = username
	}
	logger := log.FromContext(ctx)
	if err := s.repo.CreateUser(ctx, user, string(hash)); err != nil {
		if errors.Is(err, auth.ErrUserAlreadyExists) {
			logger.Info("create user rejected: username already exists", "username", username)
			return nil, connect.NewError(connect.CodeAlreadyExists, "username already exists")
		}
		logger.Error("create user failed", "username", username, "err", err)
		return nil, connectx.InternalWith(err)
	}
	logger.Info("user created", "username", username, "user_id", user.GetId(), "role", user.GetRole(), "disabled", user.GetDisabled())
	return &neoboxv1.CreateUserResponse{User: user}, nil
}

func (s *AuthServiceServer) UpdateUserPassword(ctx context.Context, req *neoboxv1.UpdateUserPasswordRequest) (*neoboxv1.UpdateUserPasswordResponse, error) {
	if err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, connectx.RequiredArgument(ctx, "id")
	}
	if req.GetPassword() == "" {
		return nil, connectx.RequiredArgument(ctx, "password")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), bcrypt.DefaultCost)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	logger := log.FromContext(ctx)
	user, err := s.repo.UpdateUserPassword(ctx, id, string(hash), time.Now().UTC())
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, connectx.NotFound("user")
		}
		logger.Error("update user password failed", "user_id", id, "err", err)
		return nil, connectx.InternalWith(err)
	}
	logger.Info("user password updated", "user_id", id, "username", user.GetUsername())
	return &neoboxv1.UpdateUserPasswordResponse{User: user}, nil
}

func (s *AuthServiceServer) UpdateProfile(ctx context.Context, req *neoboxv1.UpdateProfileRequest) (*neoboxv1.UpdateProfileResponse, error) {
	if s.repo == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "auth store not available")
	}
	current, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	displayName := strings.TrimSpace(req.GetDisplayName())
	if displayName == "" {
		return nil, connectx.RequiredArgument(ctx, "display_name")
	}
	// req.Msg.AvatarUrl is nil when the client did not include the field — leave
	// the stored avatar untouched. A non-nil pointer (including empty string)
	// is a deliberate update; trim whitespace but preserve the explicit clear.
	var avatarURL *string
	if req.AvatarUrl != nil {
		trimmed := strings.TrimSpace(*req.AvatarUrl)
		avatarURL = &trimmed
	}
	user, err := s.repo.UpdateUserProfile(ctx, current.GetId(), displayName, avatarURL, time.Now().UTC())
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, connectx.NotFound("user")
		}
		return nil, connectx.InternalWith(err)
	}
	return &neoboxv1.UpdateProfileResponse{User: user}, nil
}

func (s *AuthServiceServer) ChangePassword(ctx context.Context, req *neoboxv1.ChangePasswordRequest) (*neoboxv1.ChangePasswordResponse, error) {
	if s.repo == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "auth store not available")
	}
	current, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	if req.GetCurrentPassword() == "" {
		return nil, connectx.RequiredArgument(ctx, "current_password")
	}
	if req.GetNewPassword() == "" {
		return nil, connectx.RequiredArgument(ctx, "new_password")
	}
	_, passwordHash, err := s.repo.FindUserByID(ctx, current.GetId())
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, connectx.NotFound("user")
		}
		return nil, connectx.InternalWith(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.GetCurrentPassword())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, "current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetNewPassword()), bcrypt.DefaultCost)
	if err != nil {
		return nil, connectx.InternalWith(err)
	}
	user, err := s.repo.UpdateUserPassword(ctx, current.GetId(), string(hash), time.Now().UTC())
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, connectx.NotFound("user")
		}
		return nil, connectx.InternalWith(err)
	}
	return &neoboxv1.ChangePasswordResponse{User: user}, nil
}

func (s *AuthServiceServer) SetUserDisabled(ctx context.Context, req *neoboxv1.SetUserDisabledRequest) (*neoboxv1.SetUserDisabledResponse, error) {
	if err := s.requireAdmin(ctx); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, connectx.RequiredArgument(ctx, "id")
	}
	current, _ := auth.UserFromContext(ctx)
	if current != nil && current.GetId() == id && req.GetDisabled() {
		return nil, connect.NewError(connect.CodePermissionDenied, "cannot disable current user")
	}
	logger := log.FromContext(ctx)
	user, err := s.repo.SetUserDisabled(ctx, id, req.GetDisabled(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return nil, connectx.NotFound("user")
		}
		logger.Error("set user disabled failed", "user_id", id, "disabled", req.GetDisabled(), "err", err)
		return nil, connectx.InternalWith(err)
	}
	logger.Info("user disabled flag updated", "user_id", id, "username", user.GetUsername(), "disabled", user.GetDisabled())
	return &neoboxv1.SetUserDisabledResponse{User: user}, nil
}

func (s *AuthServiceServer) requireAdmin(ctx context.Context) error {
	if s.repo == nil {
		return connect.NewError(connect.CodeFailedPrecondition, "auth store not available")
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "unauthenticated")
	}
	if user.GetRole() != "admin" {
		return connect.NewError(connect.CodePermissionDenied, "admin role required")
	}
	return nil
}

func normalizeRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "" {
		return "user"
	}
	return role
}

func BootstrapInitialAdmin(ctx context.Context, repo auth.Repository, cfg config.AuthConfig) error {
	logger := log.FromContext(ctx)
	if repo == nil {
		logger.Info("skipping initial admin bootstrap, auth repo is nil")
		return nil
	}

	username := strings.TrimSpace(cfg.InitialAdminUsername)
	password := cfg.InitialAdminPassword
	logger.Info("initial admin bootstrap started",
		"initial_admin_username_set", username != "",
		"initial_admin_password_set", password != "",
	)

	count, err := repo.CountUsers(ctx)
	if err != nil {
		logger.Error("failed to count auth users", "err", err)
		return err
	}
	logger.Info("auth user count loaded", "user_count", count)
	if count > 0 {
		logger.Info("skipping initial admin bootstrap, users already exist", "user_count", count)
		return nil
	}

	if username == "" || password == "" {
		logger.Error("initial admin config missing",
			"initial_admin_username_set", username != "",
			"initial_admin_password_set", password != "",
		)
		return errors.New("auth initial admin is required: set auth.initial_admin_username and auth.initial_admin_password")
	}

	logger.Info("creating initial admin user", "username", username, "role", "admin")
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("failed to hash initial admin password", "username", username, "err", err)
		return err
	}
	now := time.Now().UTC()
	user := &neoboxv1.User{
		Id:          uuid.NewString(),
		Username:    username,
		DisplayName: username,
		Role:        "admin",
		CreatedAt:   timestamppb.New(now),
		UpdatedAt:   timestamppb.New(now),
	}
	if err := repo.CreateUser(ctx, user, string(hash)); err != nil {
		logger.Error("failed to create initial admin user", "username", username, "user_id", user.GetId(), "err", err)
		return err
	}
	logger.Info("initial admin user created", "username", username, "user_id", user.GetId(), "role", user.GetRole())
	return nil
}

func HashAuthSessionToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func generateSessionSecret() (string, error) {
	buf := make([]byte, sessionSecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "nb_" + hex.EncodeToString(buf), nil
}
