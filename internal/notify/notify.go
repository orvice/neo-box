// Package notify tells users about things that need their attention. Any
// Provider raises an Alert; the Service stores it once per dedupe key and
// delivers it in the background to every enabled channel of the user.
// Channels are per user and typed (Telegram first); a Sender per type does
// the delivery.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"butterfly.orx.me/core/log"
	"github.com/google/uuid"

	repo "go.orx.me/apps/neo-box/internal/repo/notify"
	"go.orx.me/apps/neo-box/internal/secretbox"
)

// Severity says how urgent an alert is.
type Severity = repo.Severity

const (
	Info     = repo.SeverityInfo
	Warning  = repo.SeverityWarning
	Critical = repo.SeverityCritical
)

// Alert is what a Provider raises. Key deduplicates: an alert whose key the
// user already has is dropped, so the raiser decides how often it repeats
// by what it puts in the key (an event ID, a period, a day).
type Alert struct {
	UserID       string
	Source       string
	ConnectionID string
	Kind         string
	Key          string
	Severity     Severity
	Title        string
	Body         string
	// Link is a dashboard path, e.g. "/connections/<id>".
	Link string
}

// Notifier is what alert raisers depend on.
type Notifier interface {
	Notify(ctx context.Context, a Alert)
}

// Message is what a Sender delivers.
type Message struct {
	Severity Severity
	Title    string
	Body     string
	// URL is an absolute dashboard link, or empty.
	URL string
}

// Sender delivers messages to one type of channel.
type Sender interface {
	// Type is the channel type stored on channels, e.g. "telegram".
	Type() string
	// Check validates settings (JSON, as stored) without contacting the
	// service.
	Check(config, secret json.RawMessage) error
	Send(ctx context.Context, config, secret json.RawMessage, m Message) error
}

var (
	// ErrNoCipher means the server has no crypto.encryption_key, so
	// channel secrets can be neither sealed nor opened.
	ErrNoCipher = errors.New("crypto.encryption_key is not configured on the server")
	// ErrUnknownType is returned for a channel type with no Sender.
	ErrUnknownType = errors.New("unknown notification channel type")
)

// CheckError is a Sender.Check failure: the settings are invalid.
type CheckError struct{ Err error }

func (e *CheckError) Error() string { return e.Err.Error() }
func (e *CheckError) Unwrap() error { return e.Err }

// redeliverWindow bounds which undelivered alerts Start picks up again.
const redeliverWindow = 24 * time.Hour

type Service struct {
	repo         repo.Repository
	cipher       *secretbox.Cipher
	dashboardURL string
	now          func() time.Time

	mu      sync.RWMutex
	senders map[string]Sender

	queue chan string
	wg    sync.WaitGroup
}

// NewService builds a service. dashboardURL (e.g. "https://neobox.example")
// turns alert links into absolute URLs in messages; empty leaves them out.
// cipher may be nil; channels then cannot be created or used.
func NewService(r repo.Repository, cipher *secretbox.Cipher, dashboardURL string) *Service {
	return &Service{
		repo: r, cipher: cipher, dashboardURL: strings.TrimRight(dashboardURL, "/"),
		now: time.Now, senders: map[string]Sender{}, queue: make(chan string, 256),
	}
}

// Register adds a channel type.
func (s *Service) Register(snd Sender) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.senders[snd.Type()] = snd
}

func (s *Service) sender(typ string) (Sender, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snd, ok := s.senders[typ]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownType, typ)
	}
	return snd, nil
}

// Start launches the delivery worker and queues alerts from the last day
// that a restart left undelivered. The worker stops when ctx is done.
func (s *Service) Start(ctx context.Context) error {
	pending, err := s.repo.ListUndelivered(ctx, s.now().Add(-redeliverWindow))
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go s.worker(ctx)
	for _, a := range pending {
		s.enqueue(ctx, a.ID)
	}
	return nil
}

// Wait blocks until the worker exits.
func (s *Service) Wait() { s.wg.Wait() }

// Notify stores a and queues its delivery, unless the user already has an
// alert with a.Key. Failures are logged: raisers are background work that
// has nothing better to do with them.
func (s *Service) Notify(ctx context.Context, a Alert) {
	logger := log.FromContext(ctx)
	row := &repo.Alert{
		ID: uuid.NewString(), UserID: a.UserID, Source: a.Source, ConnectionID: a.ConnectionID,
		Kind: a.Kind, Key: a.Key, Severity: a.Severity, Title: a.Title, Body: a.Body, Link: a.Link,
		CreatedAt: s.now().UTC(),
	}
	created, err := s.repo.CreateAlert(ctx, row)
	if err != nil {
		logger.Warn("store alert failed", "key", a.Key, "err", err)
		return
	}
	if !created {
		return
	}
	logger.Info("alert raised", "user_id", a.UserID, "kind", a.Kind, "key", a.Key)
	s.enqueue(ctx, row.ID)
}

func (s *Service) enqueue(ctx context.Context, id string) {
	select {
	case s.queue <- id:
	default:
		// Left undelivered; the next start picks it up.
		log.FromContext(ctx).Warn("alert queue is full", "alert_id", id)
	}
}

func (s *Service) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.queue:
			s.deliver(ctx, id)
		}
	}
}

// deliver sends an alert to each of the user's enabled channels and
// records the outcome on the alert and on each channel.
func (s *Service) deliver(ctx context.Context, id string) {
	logger := log.FromContext(ctx)
	a, err := s.repo.GetAlert(ctx, id)
	if err != nil {
		logger.Warn("load alert failed", "alert_id", id, "err", err)
		return
	}
	channels, err := s.repo.ListChannels(ctx, a.UserID)
	if err != nil {
		logger.Warn("list notification channels failed", "user_id", a.UserID, "err", err)
		return
	}
	msg := Message{Severity: a.Severity, Title: a.Title, Body: a.Body, URL: s.link(a.Link)}
	var (
		delivered time.Time
		errs      []string
		tried     int
	)
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		tried++
		if err := s.send(ctx, ch, msg); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ch.Name, err))
			continue
		}
		delivered = s.now().UTC()
	}
	if tried == 0 {
		errs = append(errs, "no enabled notification channels")
	}
	if err := s.repo.SetAlertDelivery(ctx, id, delivered, strings.Join(errs, "; ")); err != nil {
		logger.Warn("record alert delivery failed", "alert_id", id, "err", err)
	}
}

// send delivers one message to one channel and records the result on it.
func (s *Service) send(ctx context.Context, ch *repo.Channel, msg Message) error {
	err := func() error {
		snd, err := s.sender(ch.Type)
		if err != nil {
			return err
		}
		secret, err := s.unseal(ch.SecretCiphertext)
		if err != nil {
			return err
		}
		return snd.Send(ctx, ch.Config, secret, msg)
	}()
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if rerr := s.repo.RecordChannelResult(ctx, ch.ID, s.now().UTC(), errMsg); rerr != nil {
		log.FromContext(ctx).Warn("record channel result failed", "channel_id", ch.ID, "err", rerr)
	}
	return err
}

func (s *Service) link(path string) string {
	if path == "" || s.dashboardURL == "" {
		return ""
	}
	return s.dashboardURL + path
}

// --- channels ---

func (s *Service) ListChannels(ctx context.Context, userID string) ([]*repo.Channel, error) {
	return s.repo.ListChannels(ctx, userID)
}

func (s *Service) GetChannel(ctx context.Context, userID, id string) (*repo.Channel, error) {
	return s.repo.GetChannel(ctx, userID, id)
}

// CreateChannel checks and stores a new, enabled channel.
func (s *Service) CreateChannel(ctx context.Context, userID, typ, name string, config, secret any) (*repo.Channel, error) {
	cfgJSON, secJSON, err := s.encode(typ, config, secret)
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(secJSON)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	c := &repo.Channel{
		ID: uuid.NewString(), UserID: userID, Type: typ, Name: name, Enabled: true,
		Config: cfgJSON, SecretCiphertext: sealed, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.CreateChannel(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateChannel changes c's name, enabled flag and config. A nil secret
// keeps the stored one.
func (s *Service) UpdateChannel(ctx context.Context, c *repo.Channel, name string, enabled bool, config, secret any) error {
	if secret == nil {
		stored, err := s.unseal(c.SecretCiphertext)
		if err != nil {
			return err
		}
		secret = stored // a json.RawMessage marshals as itself
	}
	cfgJSON, secJSON, err := s.encode(c.Type, config, secret)
	if err != nil {
		return err
	}
	sealed, err := s.seal(secJSON)
	if err != nil {
		return err
	}
	c.Name, c.Enabled, c.Config, c.SecretCiphertext, c.UpdatedAt = name, enabled, cfgJSON, sealed, s.now().UTC()
	return s.repo.UpdateChannel(ctx, c)
}

func (s *Service) DeleteChannel(ctx context.Context, userID, id string) error {
	return s.repo.DeleteChannel(ctx, userID, id)
}

// TestChannel sends a test message to c now and returns the outcome; it is
// also recorded on the channel.
func (s *Service) TestChannel(ctx context.Context, c *repo.Channel) error {
	return s.send(ctx, c, Message{
		Severity: Info,
		Title:    "Neo Box test message",
		Body:     fmt.Sprintf("Channel %q will receive Neo Box alerts.", c.Name),
		URL:      s.link("/settings/notifications"),
	})
}

func (s *Service) ListAlerts(ctx context.Context, userID string, limit int) ([]*repo.Alert, error) {
	return s.repo.ListAlerts(ctx, userID, limit)
}

// encode marshals config and secret and has the type's Sender check them.
func (s *Service) encode(typ string, config, secret any) (json.RawMessage, json.RawMessage, error) {
	snd, err := s.sender(typ)
	if err != nil {
		return nil, nil, err
	}
	cfgJSON, err := json.Marshal(config)
	if err != nil {
		return nil, nil, fmt.Errorf("encode channel config: %w", err)
	}
	secJSON, err := json.Marshal(secret)
	if err != nil {
		return nil, nil, fmt.Errorf("encode channel secret: %w", err)
	}
	if err := snd.Check(cfgJSON, secJSON); err != nil {
		return nil, nil, &CheckError{Err: err}
	}
	return cfgJSON, secJSON, nil
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
		return nil, fmt.Errorf("open channel secret: %w", err)
	}
	return plain, nil
}
