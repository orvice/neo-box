// Package tgbot implements the system-level Telegram bot for viewing Neo Box
// Connections from a linked private chat. All callback interactions are
// stateless so webhook deliveries can land on any pod.
package tgbot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/connection"
	authrepo "go.orx.me/apps/neo-box/internal/repo/auth"
	tgrepo "go.orx.me/apps/neo-box/internal/repo/telegram"
	"go.orx.me/apps/neo-box/internal/telegram"
)

const (
	ModeWebhook = "webhook"
	ModePolling = "polling"

	linkTokenTTL        = 10 * time.Minute
	maxWebhookBodyBytes = 1 << 20 // 1 MiB
	defaultWorkers      = 8
	defaultQueueSize    = 256
	updateHandleTimeout = 15 * time.Second
)

// Config configures the Telegram bot service.
type Config struct {
	BotToken       string
	APIEndpoint    string
	Mode           string
	WebhookURL     string
	WebhookSecret  string
	AutoSetWebhook bool
	DashboardURL   string
	Workers        int
}

// LinkInfo is returned to the dashboard when a user requests a one-time link.
type LinkInfo struct {
	DeepLink  string
	Command   string
	ExpiresAt time.Time
}

// Service runs the Telegram bot, handles webhooks (or optional local polling),
// and serves link/unbind operations for the RPC layer.
type Service struct {
	cfg    Config
	client *telegram.Client
	repo   tgrepo.Repository
	auth   authrepo.Repository
	conns  *connection.Service
	now    func() time.Time

	botUsername atomic.Value // string
	updates     chan telegram.Update
	wg          sync.WaitGroup
}

// New validates cfg and creates a bot Service.
func New(cfg Config, repo tgrepo.Repository, auth authrepo.Repository, conns *connection.Service) (*Service, error) {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return nil, errors.New("telegram.bot_token is required")
	}
	if cfg.Mode == "" {
		cfg.Mode = ModeWebhook
	}
	switch cfg.Mode {
	case ModeWebhook:
		if strings.TrimSpace(cfg.WebhookURL) == "" {
			return nil, errors.New("telegram.webhook_url is required in webhook mode")
		}
		if strings.TrimSpace(cfg.WebhookSecret) == "" {
			return nil, errors.New("telegram.webhook_secret is required in webhook mode")
		}
	case ModePolling:
	default:
		return nil, fmt.Errorf("telegram.mode must be %q or %q, got %q", ModeWebhook, ModePolling, cfg.Mode)
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	cfg.DashboardURL = strings.TrimRight(strings.TrimSpace(cfg.DashboardURL), "/")

	return &Service{
		cfg:     cfg,
		client:  telegram.NewClient(cfg.BotToken, cfg.APIEndpoint),
		repo:    repo,
		auth:    auth,
		conns:   conns,
		now:     time.Now,
		updates: make(chan telegram.Update, defaultQueueSize),
	}, nil
}

// BotUsername returns the bot's username (without @), or empty if getMe has
// not completed yet.
func (s *Service) BotUsername() string {
	v, _ := s.botUsername.Load().(string)
	return v
}

// Start launches the update workers and registers the bot's identity, command
// menu, and webhook (or starts the local polling loop). Transient Telegram API
// errors during startup are logged and retried in the background so pods still
// boot cleanly during Telegram outages.
func (s *Service) Start(ctx context.Context) error {
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	// Best-effort synchronous bootstrap with a short timeout so BotUsername is
	// usually ready immediately on startup; failures retry asynchronously.
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ok := s.initBot(initCtx)
	cancel()
	if !ok {
		s.wg.Add(1)
		go s.retryInit(ctx)
	}

	if s.cfg.Mode == ModePolling {
		s.wg.Add(1)
		go s.pollLoop(ctx)
	}
	return nil
}

// Wait blocks until all bot workers and loops exit.
func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) initBot(ctx context.Context) bool {
	logger := log.FromContext(ctx)
	me, err := s.client.GetMe(ctx)
	if err != nil {
		logger.Warn("telegram getMe failed", "err", err)
		return false
	}
	s.botUsername.Store(me.Username)
	logger.Info("telegram bot ready", "username", me.Username, "mode", s.cfg.Mode)

	if err := s.client.SetMyCommands(ctx, botCommands()); err != nil {
		logger.Warn("telegram setMyCommands failed", "err", err)
	}

	if s.cfg.Mode == ModeWebhook && s.cfg.AutoSetWebhook {
		if err := s.client.SetWebhook(ctx, telegram.SetWebhookRequest{
			URL:            s.cfg.WebhookURL,
			SecretToken:    s.cfg.WebhookSecret,
			AllowedUpdates: []string{"message", "callback_query"},
		}); err != nil {
			logger.Warn("telegram setWebhook failed", "err", err)
			return false
		}
		logger.Info("telegram webhook registered", "url", s.cfg.WebhookURL)
	}
	if s.cfg.Mode == ModePolling {
		if err := s.client.DeleteWebhook(ctx, false); err != nil {
			logger.Warn("telegram deleteWebhook failed", "err", err)
			return false
		}
	}
	return true
}

func (s *Service) retryInit(ctx context.Context) {
	defer s.wg.Done()
	delay := 5 * time.Second
	for {
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ok := s.initBot(initCtx)
		cancel()
		if ok {
			return
		}
		delay = min(delay*2, 2*time.Minute)
	}
}

func (s *Service) pollLoop(ctx context.Context) {
	defer s.wg.Done()
	logger := log.FromContext(ctx)
	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := s.client.GetUpdates(ctx, telegram.GetUpdatesRequest{
			Offset:         offset,
			Timeout:        25,
			AllowedUpdates: []string{"message", "callback_query"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("telegram getUpdates failed", "err", err)
			t := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			select {
			case s.updates <- u:
			case <-ctx.Done():
				return
			}
		}
	}
}

// HandleWebhook validates Telegram's secret header, parses the update, and
// enqueues it for async processing so Telegram always receives a fast response.
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode != ModeWebhook {
		http.Error(w, "telegram webhook is disabled", http.StatusNotFound)
		return
	}
	gotSecret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(gotSecret), []byte(s.cfg.WebhookSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	if len(raw) > maxWebhookBodyBytes {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	var u telegram.Update
	if err := json.Unmarshal(raw, &u); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	select {
	case s.updates <- u:
		w.WriteHeader(http.StatusOK)
	default:
		// Return 503 so Telegram retries later instead of dropping the update.
		http.Error(w, "bot busy", http.StatusServiceUnavailable)
	}
}

func (s *Service) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-s.updates:
			s.safeHandleUpdate(ctx, u)
		}
	}
}

func (s *Service) safeHandleUpdate(ctx context.Context, u telegram.Update) {
	handleCtx, cancel := context.WithTimeout(ctx, updateHandleTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.FromContext(handleCtx).Error("telegram bot panic recovered", "update_id", u.UpdateID, "panic", r)
		}
	}()
	s.handleUpdate(handleCtx, u)
}

// --- RPC-facing methods ---

// GetBinding returns the Neo Box user's current Telegram link, or
// tgrepo.ErrNotFound.
func (s *Service) GetBinding(ctx context.Context, userID string) (*tgrepo.Binding, error) {
	return s.repo.GetBindingByUser(ctx, userID)
}

// CreateLinkToken generates a single-use, 10-minute deep link token for userID.
func (s *Service) CreateLinkToken(ctx context.Context, userID string) (*LinkInfo, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate telegram link token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	expiresAt := now.Add(linkTokenTTL)

	if err := s.repo.CreateLinkToken(ctx, &tgrepo.LinkToken{
		TokenHash: hashToken(token),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, err
	}

	info := &LinkInfo{
		Command:   "/start " + token,
		ExpiresAt: expiresAt,
	}
	if username := s.BotUsername(); username != "" {
		info.DeepLink = fmt.Sprintf("https://t.me/%s?start=%s", username, token)
	}
	return info, nil
}

// Unbind removes the user's Telegram binding. Returns nil if already unbound.
func (s *Service) Unbind(ctx context.Context, userID string) error {
	err := s.repo.DeleteBindingByUser(ctx, userID)
	if errors.Is(err, tgrepo.ErrNotFound) {
		return nil
	}
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func botCommands() []telegram.BotCommand {
	return []telegram.BotCommand{
		{Command: "connections", Description: "List and inspect your Connections"},
		{Command: "status", Description: "View Connection health summary"},
		{Command: "unbind", Description: "Disconnect this Telegram account"},
		{Command: "help", Description: "Show available commands"},
	}
}
