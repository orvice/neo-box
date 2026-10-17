package logto

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go.orx.me/apps/neo-box/internal/repo/logtosession"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

type flow struct{ State, Nonce, Verifier, ReturnPath string }

type profile struct {
	Subject, Username, DisplayName, AvatarURL, Email string
}

type session struct {
	Token   *oauth2.Token
	IDToken string
	Nonce   string
	CSRF    string
	Profile *profile
}

type Identity struct {
	User *neoboxv1.User
	CSRF string
}

func opaque() string {
	value := make([]byte, 32)
	// crypto/rand.Read is guaranteed to fill the buffer or terminate the process.
	_, _ = rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// ReturnPath confines post-login navigation to the dashboard origin.
func ReturnPath(raw string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.IsAbs() {
		return "/"
	}
	resolved := (&url.URL{Scheme: "https", Host: "dashboard.invalid"}).ResolveReference(u)
	if strings.HasPrefix(resolved.Path, "/api/") || resolved.Path == "/sign-in" || resolved.Path == "/auth/callback" {
		return "/"
	}
	return resolved.RequestURI() + fragment(resolved.Fragment)
}

func fragment(value string) string {
	if value == "" {
		return ""
	}
	return "#" + url.PathEscape(value)
}

func (s *Service) save(ctx context.Context, kind, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sealed, err := s.cipher.Encrypt(raw)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.repo.Create(ctx, &logtosession.Record{ID: hash(key), Kind: kind, Ciphertext: sealed, CreatedAt: now, ExpiresAt: now.Add(ttl)})
}

func (s *Service) load(record *logtosession.Record, value any) error {
	raw, err := s.cipher.Decrypt(record.Ciphertext)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func (s *Service) Begin(ctx context.Context, returnPath string, reauthenticate bool) (cookie, destination string, err error) {
	cookie = opaque()
	f := flow{State: opaque(), Nonce: opaque(), Verifier: oauth2.GenerateVerifier(), ReturnPath: ReturnPath(returnPath)}
	if err := s.save(ctx, "flow", cookie, f, FlowTTL); err != nil {
		return "", "", err
	}
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(f.Verifier), oidc.Nonce(f.Nonce)}
	if reauthenticate {
		options = append(options, oauth2.SetAuthURLParam("prompt", "login"))
	}
	return cookie, s.oauth.AuthCodeURL(f.State, options...), nil
}

func (s *Service) Complete(ctx context.Context, cookie, state, code string) (sessionCookie, returnPath string, err error) {
	if cookie == "" {
		return "", "", ErrInvalidFlow
	}
	record, err := s.repo.TakeFlow(ctx, hash(cookie), time.Now().UTC())
	if errors.Is(err, logtosession.ErrNotFound) {
		return "", "", ErrInvalidFlow
	}
	if err != nil {
		return "", "", err
	}
	var f flow
	if err := s.load(record, &f); err != nil {
		return "", "", err
	}
	if code == "" || !hmac.Equal([]byte(f.State), []byte(state)) {
		return "", "", ErrInvalidFlow
	}
	ctx = oidc.ClientContext(ctx, s.httpClient)
	token, err := s.oauth.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return "", "", err
	}
	rawID, _ := token.Extra("id_token").(string)
	user, err := s.verifyIdentity(ctx, rawID, f.Nonce, true, token.AccessToken)
	if err != nil {
		return "", "", err
	}
	if token.RefreshToken == "" || token.AccessToken == "" || token.Expiry.IsZero() || !token.Expiry.After(time.Now()) {
		return "", "", errors.New("Logto did not issue tokens for an offline session")
	}
	sessionCookie = opaque()
	value := session{Token: token, IDToken: rawID, Nonce: f.Nonce, CSRF: opaque(), Profile: user}
	if err := s.save(ctx, "session", sessionCookie, value, s.SessionTTL()); err != nil {
		return "", "", err
	}
	return sessionCookie, f.ReturnPath, nil
}

func (s *Service) verifyIdentity(ctx context.Context, raw, nonce string, requireNonce bool, accessToken string) (*profile, error) {
	id, err := s.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	if id.Subject != s.cfg.OwnerSubject {
		return nil, ErrForbidden
	}
	if (requireNonce || id.Nonce != "") && !hmac.Equal([]byte(id.Nonce), []byte(nonce)) {
		return nil, ErrInvalidFlow
	}
	if id.AccessTokenHash != "" {
		if err := id.VerifyAccessToken(accessToken); err != nil {
			return nil, err
		}
	}
	var claims struct{ Name, Username, Picture, Email string }
	if err := id.Claims(&claims); err != nil {
		return nil, err
	}
	username := claims.Username
	if username == "" {
		username = claims.Email
	}
	if username == "" {
		username = id.Subject
	}
	name := claims.Name
	if name == "" {
		name = username
	}
	return &profile{Subject: id.Subject, Username: username, DisplayName: name, AvatarURL: claims.Picture, Email: claims.Email}, nil
}

func (s *Service) Authenticate(ctx context.Context, cookie string) (*Identity, error) {
	return s.authenticate(ctx, cookie, true)
}

// IdentityForLogout lets a valid browser session revoke itself even when
// Logto is unavailable or its refresh grant has expired.
func (s *Service) IdentityForLogout(ctx context.Context, cookie string) (*Identity, error) {
	return s.authenticate(ctx, cookie, false)
}

func (s *Service) authenticate(ctx context.Context, cookie string, refresh bool) (*Identity, error) {
	if cookie == "" {
		return nil, ErrUnauthenticated
	}
	var value session
	err := s.repo.WithSession(ctx, hash(cookie), time.Now().UTC(), func(record *logtosession.Record) error {
		if err := s.load(record, &value); err != nil {
			return err
		}
		if value.Profile == nil || value.Profile.Subject != s.cfg.OwnerSubject {
			return ErrForbidden
		}
		if value.Token == nil || value.CSRF == "" || value.Token.Expiry.IsZero() {
			return ErrUnauthenticated
		}
		if !refresh || value.Token.Expiry.After(time.Now().Add(30*time.Second)) {
			return nil
		}
		if value.Token.RefreshToken == "" {
			return ErrUnauthenticated
		}
		previous := *value.Token
		previous.Expiry = time.Now().Add(-time.Minute)
		token, err := s.oauth.TokenSource(oidc.ClientContext(ctx, s.httpClient), &previous).Token()
		if err != nil {
			var response *oauth2.RetrieveError
			if errors.As(err, &response) && (response.ErrorCode == "invalid_grant" || response.ErrorCode == "invalid_token") {
				return ErrUnauthenticated
			}
			return err
		}
		if rawID, _ := token.Extra("id_token").(string); rawID != "" {
			user, err := s.verifyIdentity(ctx, rawID, value.Nonce, false, token.AccessToken)
			if err != nil {
				return ErrUnauthenticated
			}
			value.IDToken, value.Profile = rawID, user
		}
		if token.Expiry.IsZero() || !token.Expiry.After(time.Now()) {
			return ErrUnauthenticated
		}
		value.Token = token
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		record.Ciphertext, err = s.cipher.Encrypt(raw)
		return err
	})
	if errors.Is(err, logtosession.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrForbidden) {
		if deleteErr := s.repo.Delete(ctx, hash(cookie)); deleteErr != nil {
			return nil, deleteErr
		}
	}
	if err != nil {
		return nil, err
	}
	u := value.Profile
	user := &neoboxv1.User{Id: s.cfg.EffectiveDataOwnerID(), Username: u.Username, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL, Email: u.Email, Provider: "logto", ExternalId: u.Subject, Role: "user"}
	return &Identity{User: user, CSRF: value.CSRF}, nil
}

func (s *Service) Logout(ctx context.Context, cookie string) (string, error) {
	var value session
	err := s.repo.WithSession(ctx, hash(cookie), time.Now().UTC(), func(record *logtosession.Record) error { return s.load(record, &value) })
	if err != nil && !errors.Is(err, logtosession.ErrNotFound) {
		return "", err
	}
	if err := s.repo.Delete(ctx, hash(cookie)); err != nil {
		return "", err
	}
	u, err := url.Parse(s.endSession)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", s.cfg.AppID)
	if value.IDToken != "" {
		q.Set("id_token_hint", value.IDToken)
	}
	q.Set("post_logout_redirect_uri", s.Origin()+"/sign-in")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
