// Package connection manages Connections across Providers. It seals
// secrets, asks the provider to verify credentials, records each
// connection's health, and runs the provider's cleanup before deleting a
// connection. Everything else a provider does (its resources, its
// scheduling) lives with the provider.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/google/uuid"

	repo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

// Provider is the provider-specific part of a Connection.
type Provider interface {
	// Type is the provider key stored on connections, e.g. "nocodb".
	Type() string
	// Verify checks the settings (JSON, as stored) against the service.
	Verify(ctx context.Context, config, secret json.RawMessage) error
	// Cleanup removes everything the provider stored for c. It runs before
	// the connection is deleted; an error keeps the connection.
	Cleanup(ctx context.Context, c *repo.Connection) error
}

var (
	// ErrNoCipher means the server has no crypto.encryption_key, so
	// secrets can be neither sealed nor opened.
	ErrNoCipher = errors.New("crypto.encryption_key is not configured on the server")
	// ErrBusy is wrapped by a Provider's Cleanup when the connection has
	// work in progress and cannot be deleted yet.
	ErrBusy = errors.New("connection is busy")
)

// VerifyError is a Provider.Verify failure: the settings did not work.
type VerifyError struct{ Err error }

func (e *VerifyError) Error() string { return e.Err.Error() }
func (e *VerifyError) Unwrap() error { return e.Err }

type Service struct {
	repo   repo.Repository
	cipher *secretbox.Cipher
	now    func() time.Time

	mu        sync.RWMutex
	providers map[string]Provider
}

// NewService builds a service. cipher may be nil; creating, updating, or
// opening a connection then fails with ErrNoCipher.
func NewService(r repo.Repository, cipher *secretbox.Cipher) *Service {
	return &Service{repo: r, cipher: cipher, now: time.Now, providers: make(map[string]Provider)}
}

// Register adds a provider. Connections of unregistered providers can be
// listed and deleted but not created, updated, or tested.
func (s *Service) Register(p Provider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[p.Type()] = p
}

func (s *Service) provider(key string) (Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.providers[key]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", key)
	}
	return p, nil
}

func (s *Service) Get(ctx context.Context, userID, id string) (*repo.Connection, error) {
	return s.repo.Get(ctx, userID, id)
}

func (s *Service) GetByID(ctx context.Context, id string) (*repo.Connection, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) List(ctx context.Context, f repo.Filter) ([]*repo.Connection, error) {
	return s.repo.List(ctx, f)
}

// Create verifies the settings with the provider, then stores a new
// connection whose status is ok. config and secret are marshalled to JSON.
func (s *Service) Create(ctx context.Context, userID, provider, name string, config, secret any) (*repo.Connection, error) {
	p, err := s.provider(provider)
	if err != nil {
		return nil, err
	}
	cfgJSON, secJSON, err := marshalSettings(config, secret)
	if err != nil {
		return nil, err
	}
	if err := p.Verify(ctx, cfgJSON, secJSON); err != nil {
		return nil, &VerifyError{Err: err}
	}
	sealed, err := s.seal(secJSON)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	c := &repo.Connection{
		ID: uuid.NewString(), UserID: userID, Provider: provider, Name: name,
		Config: cfgJSON, SecretCiphertext: sealed,
		Status: repo.StatusOK, StatusCheckedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Update sets c's name and config, and its secret unless secret is nil.
// When the config or secret change, the resulting settings are verified
// first and the status becomes ok. c is updated in place.
func (s *Service) Update(ctx context.Context, c *repo.Connection, name string, config, secret any) error {
	p, err := s.provider(c.Provider)
	if err != nil {
		return err
	}
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	sealed := c.SecretCiphertext
	if secret != nil || !sameJSON(cfgJSON, c.Config) {
		var secJSON json.RawMessage
		if secret != nil {
			if secJSON, err = json.Marshal(secret); err != nil {
				return fmt.Errorf("marshal secret: %w", err)
			}
			if sealed, err = s.seal(secJSON); err != nil {
				return err
			}
		} else if secJSON, err = s.unseal(c.SecretCiphertext); err != nil {
			return err
		}
		if err := p.Verify(ctx, cfgJSON, secJSON); err != nil {
			return &VerifyError{Err: err}
		}
		c.Status, c.StatusMessage, c.StatusCheckedAt = repo.StatusOK, "", s.now().UTC()
	}
	c.Name, c.Config, c.SecretCiphertext = name, cfgJSON, sealed
	c.UpdatedAt = s.now().UTC()
	return s.repo.Update(ctx, c)
}

// Test verifies the stored settings and records the outcome as c's
// status. It returns an error only when the check could not be made.
func (s *Service) Test(ctx context.Context, c *repo.Connection) error {
	p, err := s.provider(c.Provider)
	if err != nil {
		return err
	}
	secJSON, err := s.unseal(c.SecretCiphertext)
	if err != nil {
		return err
	}
	verr := p.Verify(ctx, c.Config, secJSON)
	return s.recordStatus(ctx, c, verr)
}

// Delete runs the provider's cleanup, then deletes the connection.
func (s *Service) Delete(ctx context.Context, c *repo.Connection) error {
	if p, err := s.provider(c.Provider); err == nil {
		if err := p.Cleanup(ctx, c); err != nil {
			return err
		}
	}
	return s.repo.Delete(ctx, c.UserID, c.ID)
}

// Open decodes c's config into config and its decrypted secret into
// secret. Either may be nil to skip it.
func (s *Service) Open(c *repo.Connection, config, secret any) error {
	if config != nil {
		if err := json.Unmarshal(c.Config, config); err != nil {
			return fmt.Errorf("decode connection config: %w", err)
		}
	}
	if secret != nil {
		secJSON, err := s.unseal(c.SecretCiphertext)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(secJSON, secret); err != nil {
			return fmt.Errorf("decode connection secret: %w", err)
		}
	}
	return nil
}

// RecordStatus records the outcome of provider background work on the
// connection: ok when err is nil, error with err's message otherwise.
// Failing to record it is logged, not returned.
func (s *Service) RecordStatus(ctx context.Context, id string, err error) {
	status, msg := repo.StatusOK, ""
	if err != nil {
		status, msg = repo.StatusError, err.Error()
	}
	if serr := s.repo.SetStatus(ctx, id, status, msg, s.now().UTC()); serr != nil {
		log.FromContext(ctx).Warn("record connection status failed", "connection_id", id, "err", serr)
	}
}

func (s *Service) recordStatus(ctx context.Context, c *repo.Connection, verr error) error {
	c.Status, c.StatusMessage, c.StatusCheckedAt = repo.StatusOK, "", s.now().UTC()
	if verr != nil {
		c.Status, c.StatusMessage = repo.StatusError, verr.Error()
	}
	return s.repo.SetStatus(ctx, c.ID, c.Status, c.StatusMessage, c.StatusCheckedAt)
}

func (s *Service) seal(plain []byte) (string, error) {
	if s.cipher == nil {
		return "", ErrNoCipher
	}
	return s.cipher.Encrypt(plain)
}

func (s *Service) unseal(sealed string) (json.RawMessage, error) {
	if s.cipher == nil {
		return nil, ErrNoCipher
	}
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("decrypt connection secret: %w", err)
	}
	return plain, nil
}

func marshalSettings(config, secret any) (json.RawMessage, json.RawMessage, error) {
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal config: %w", err)
	}
	secJSON, err := json.Marshal(secret)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal secret: %w", err)
	}
	return cfgJSON, secJSON, nil
}

// sameJSON compares two JSON documents by value. Stored configs come back
// from jsonb with keys reordered, so bytes cannot be compared.
func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}
