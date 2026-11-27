package tgbot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"butterfly.orx.me/core/log"
	"github.com/google/uuid"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/nocodb"
	authrepo "go.orx.me/apps/neo-box/internal/repo/auth"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	tgrepo "go.orx.me/apps/neo-box/internal/repo/telegram"
	"go.orx.me/apps/neo-box/internal/telegram"
	"go.orx.me/apps/neo-box/internal/wasabi"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

func (s *Service) handleUpdate(ctx context.Context, u telegram.Update) {
	switch {
	case u.Message != nil:
		s.handleMessage(ctx, u.Message)
	case u.CallbackQuery != nil:
		s.handleCallback(ctx, u.CallbackQuery)
	}
}

func (s *Service) handleMessage(ctx context.Context, msg *telegram.Message) {
	// Only respond to private chats with human users so Connection data never
	// leaks into groups or channels.
	if msg.Chat.Type != "private" || msg.From == nil || msg.From.IsBot {
		return
	}
	cmd, arg := parseCommand(msg.Text)
	if cmd == "" {
		return
	}

	switch cmd {
	case "start":
		if arg != "" {
			s.handleLink(ctx, msg, arg)
			return
		}
		s.handleStart(ctx, msg)
	case "connections", "list":
		s.handleConnectionsCommand(ctx, msg)
	case "status":
		s.handleStatusCommand(ctx, msg)
	case "unbind":
		s.handleUnbindCommand(ctx, msg)
	case "help":
		s.sendHTML(ctx, msg.Chat.ID, renderHelp(), nil)
	default:
		s.sendHTML(ctx, msg.Chat.ID, "Unknown command. Send /help to see available commands.", nil)
	}
}

func (s *Service) handleCallback(ctx context.Context, cb *telegram.CallbackQuery) {
	if cb.Message == nil || cb.Message.Chat.Type != "private" || cb.From.IsBot {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	user, ok := s.resolveUser(ctx, cb.From.ID)
	if !ok {
		s.answerCallback(ctx, cb.ID, "Please link your Neo Box account first.", true)
		return
	}

	parts := strings.Split(cb.Data, ":")
	switch parts[0] {
	case "cl":
		providerCode := "a"
		page := 1
		if len(parts) >= 2 && parts[1] != "" {
			providerCode = parts[1]
		}
		if len(parts) >= 3 {
			if p, err := strconv.Atoi(parts[2]); err == nil && p > 0 {
				page = p
			}
		}
		text, markup, err := s.buildConnectionList(ctx, user.GetId(), providerCode, page)
		if err != nil {
			log.FromContext(ctx).Warn("telegram list connections failed", "user_id", user.GetId(), "err", err)
			s.answerCallback(ctx, cb.ID, "Failed to load connections.", true)
			return
		}
		s.editHTML(ctx, chatID, msgID, text, markup)
		s.answerCallback(ctx, cb.ID, "", false)

	case "cv":
		if len(parts) < 2 || parts[1] == "" {
			s.answerCallback(ctx, cb.ID, "Invalid selection.", false)
			return
		}
		connID := parts[1]
		providerCode := "a"
		page := 1
		if len(parts) >= 3 && parts[2] != "" {
			providerCode = parts[2]
		}
		if len(parts) >= 4 {
			if p, err := strconv.Atoi(parts[3]); err == nil && p > 0 {
				page = p
			}
		}
		text, markup, found, err := s.buildConnectionDetail(ctx, user.GetId(), connID, providerCode, page)
		if err != nil {
			log.FromContext(ctx).Warn("telegram get connection failed", "user_id", user.GetId(), "connection_id", connID, "err", err)
			s.answerCallback(ctx, cb.ID, "Failed to load connection.", true)
			return
		}
		if !found {
			s.answerCallback(ctx, cb.ID, "Connection no longer exists.", true)
			listText, listMarkup, lerr := s.buildConnectionList(ctx, user.GetId(), providerCode, page)
			if lerr == nil {
				s.editHTML(ctx, chatID, msgID, listText, listMarkup)
			}
			return
		}
		s.editHTML(ctx, chatID, msgID, text, markup)
		s.answerCallback(ctx, cb.ID, "", false)

	case "cs":
		text, markup, err := s.buildStatusOverview(ctx, user.GetId())
		if err != nil {
			log.FromContext(ctx).Warn("telegram status failed", "user_id", user.GetId(), "err", err)
			s.answerCallback(ctx, cb.ID, "Failed to load status.", true)
			return
		}
		s.editHTML(ctx, chatID, msgID, text, markup)
		s.answerCallback(ctx, cb.ID, "", false)

	case "ub":
		if len(parts) >= 2 && parts[1] == "y" {
			err := s.repo.DeleteBindingByTelegramUser(ctx, cb.From.ID)
			if err != nil && !errors.Is(err, tgrepo.ErrNotFound) {
				log.FromContext(ctx).Warn("telegram unbind failed", "telegram_user_id", cb.From.ID, "err", err)
				s.answerCallback(ctx, cb.ID, "Failed to disconnect.", true)
				return
			}
			log.FromContext(ctx).Info("telegram account unbound", "user_id", user.GetId(), "telegram_user_id", cb.From.ID)
			s.editHTML(ctx, chatID, msgID, "Telegram account disconnected from Neo Box.", nil)
			s.answerCallback(ctx, cb.ID, "Disconnected", false)
			return
		}
		s.editHTML(ctx, chatID, msgID, "Disconnect cancelled.", nil)
		s.answerCallback(ctx, cb.ID, "Cancelled", false)

	default:
		s.answerCallback(ctx, cb.ID, "Unknown action.", false)
	}
}

func (s *Service) handleLink(ctx context.Context, msg *telegram.Message, rawToken string) {
	logger := log.FromContext(ctx)
	now := s.now().UTC()

	tok, err := s.repo.ConsumeLinkToken(ctx, hashToken(rawToken), now)
	if err != nil {
		if errors.Is(err, tgrepo.ErrNotFound) {
			s.sendHTML(ctx, msg.Chat.ID,
				"This link is invalid or has expired. Generate a new one in Neo Box under <b>Settings → Telegram Bot</b>.",
				nil,
			)
			return
		}
		logger.Warn("consume telegram link token failed", "err", err)
		s.sendHTML(ctx, msg.Chat.ID, "Could not complete linking right now. Please try again.", nil)
		return
	}

	user, err := s.auth.GetUser(ctx, tok.UserID)
	if err != nil || user == nil || user.GetDisabled() {
		s.sendHTML(ctx, msg.Chat.ID, "This Neo Box account is unavailable or disabled.", nil)
		return
	}

	binding := &tgrepo.Binding{
		ID:               uuid.NewString(),
		UserID:           tok.UserID,
		TelegramUserID:   msg.From.ID,
		ChatID:           msg.Chat.ID,
		TelegramUsername: msg.From.Username,
		DisplayName:      msg.From.DisplayName(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.repo.SaveBinding(ctx, binding); err != nil {
		if errors.Is(err, tgrepo.ErrTelegramUserAlreadyBound) {
			s.sendHTML(ctx, msg.Chat.ID,
				"This Telegram account is already linked to another Neo Box user. Send /unbind first to disconnect it.",
				nil,
			)
			return
		}
		logger.Warn("save telegram binding failed", "user_id", tok.UserID, "err", err)
		s.sendHTML(ctx, msg.Chat.ID, "Could not save Telegram link. Please try again.", nil)
		return
	}

	logger.Info("telegram account linked", "user_id", tok.UserID, "telegram_user_id", msg.From.ID)
	label := user.GetDisplayName()
	if label == "" {
		label = user.GetUsername()
	}
	text := fmt.Sprintf("✅ Linked to Neo Box account <b>%s</b>.\n\nUse /connections to browse your Connections or /status for a health summary.", telegram.Escape(label))
	s.sendHTML(ctx, msg.Chat.ID, text, mainMenuMarkup())
}

func (s *Service) handleStart(ctx context.Context, msg *telegram.Message) {
	user, status := s.lookupUser(ctx, msg.From.ID)
	switch status {
	case authUnbound:
		s.sendHTML(ctx, msg.Chat.ID, s.renderUnboundPrompt(), nil)
	case authDisabled:
		s.sendHTML(ctx, msg.Chat.ID, "Your Neo Box account is disabled.", nil)
	case authError:
		s.sendHTML(ctx, msg.Chat.ID, "Could not verify your Neo Box account right now. Please try again.", nil)
	case authOK:
		label := user.GetDisplayName()
		if label == "" {
			label = user.GetUsername()
		}
		text := fmt.Sprintf("👋 Hello <b>%s</b>!\n\nChoose an option below to view your Neo Box Connections.", telegram.Escape(label))
		s.sendHTML(ctx, msg.Chat.ID, text, mainMenuMarkup())
	}
}

func (s *Service) handleConnectionsCommand(ctx context.Context, msg *telegram.Message) {
	user, ok := s.requireUserMessage(ctx, msg)
	if !ok {
		return
	}
	text, markup, err := s.buildConnectionList(ctx, user.GetId(), "a", 1)
	if err != nil {
		log.FromContext(ctx).Warn("telegram list connections failed", "user_id", user.GetId(), "err", err)
		s.sendHTML(ctx, msg.Chat.ID, "Failed to load Connections. Please try again.", nil)
		return
	}
	s.sendHTML(ctx, msg.Chat.ID, text, markup)
}

func (s *Service) handleStatusCommand(ctx context.Context, msg *telegram.Message) {
	user, ok := s.requireUserMessage(ctx, msg)
	if !ok {
		return
	}
	text, markup, err := s.buildStatusOverview(ctx, user.GetId())
	if err != nil {
		log.FromContext(ctx).Warn("telegram status failed", "user_id", user.GetId(), "err", err)
		s.sendHTML(ctx, msg.Chat.ID, "Failed to load Connection status. Please try again.", nil)
		return
	}
	s.sendHTML(ctx, msg.Chat.ID, text, markup)
}

func (s *Service) handleUnbindCommand(ctx context.Context, msg *telegram.Message) {
	user, ok := s.requireUserMessage(ctx, msg)
	if !ok {
		return
	}
	label := user.GetUsername()
	text := fmt.Sprintf("Disconnect this Telegram account from Neo Box user <b>%s</b>?", telegram.Escape(label))
	markup := &telegram.InlineKeyboardMarkup{
		InlineKeyboard: [][]telegram.InlineKeyboardButton{
			{
				{Text: "Disconnect", CallbackData: "ub:y"},
				{Text: "Cancel", CallbackData: "ub:n"},
			},
		},
	}
	s.sendHTML(ctx, msg.Chat.ID, text, markup)
}

type authResult int

const (
	authOK authResult = iota
	authUnbound
	authDisabled
	authError
)

func (s *Service) lookupUser(ctx context.Context, telegramUserID int64) (*neoboxv1.User, authResult) {
	binding, err := s.repo.GetBindingByTelegramUser(ctx, telegramUserID)
	if err != nil {
		if errors.Is(err, tgrepo.ErrNotFound) {
			return nil, authUnbound
		}
		log.FromContext(ctx).Warn("lookup telegram binding failed", "telegram_user_id", telegramUserID, "err", err)
		return nil, authError
	}
	user, err := s.auth.GetUser(ctx, binding.UserID)
	if err != nil {
		if errors.Is(err, authrepo.ErrUserNotFound) {
			return nil, authUnbound
		}
		log.FromContext(ctx).Warn("lookup bound user failed", "user_id", binding.UserID, "err", err)
		return nil, authError
	}
	if user == nil || user.GetDisabled() {
		return nil, authDisabled
	}
	return user, authOK
}

func (s *Service) resolveUser(ctx context.Context, telegramUserID int64) (*neoboxv1.User, bool) {
	user, status := s.lookupUser(ctx, telegramUserID)
	return user, status == authOK
}

func (s *Service) requireUserMessage(ctx context.Context, msg *telegram.Message) (*neoboxv1.User, bool) {
	user, status := s.lookupUser(ctx, msg.From.ID)
	switch status {
	case authOK:
		return user, true
	case authUnbound:
		s.sendHTML(ctx, msg.Chat.ID, s.renderUnboundPrompt(), nil)
	case authDisabled:
		s.sendHTML(ctx, msg.Chat.ID, "Your Neo Box account is disabled.", nil)
	default:
		s.sendHTML(ctx, msg.Chat.ID, "Could not verify your Neo Box account right now. Please try again.", nil)
	}
	return nil, false
}

func (s *Service) sendHTML(ctx context.Context, chatID int64, text string, markup *telegram.InlineKeyboardMarkup) {
	_, err := s.client.SendMessage(ctx, telegram.SendMessageRequest{
		ChatID:                chatID,
		Text:                  text,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})
	if err != nil {
		log.FromContext(ctx).Warn("telegram sendMessage failed", "chat_id", chatID, "err", err)
	}
}

func (s *Service) editHTML(ctx context.Context, chatID int64, msgID int, text string, markup *telegram.InlineKeyboardMarkup) {
	err := s.client.EditMessageText(ctx, telegram.EditMessageTextRequest{
		ChatID:                chatID,
		MessageID:             msgID,
		Text:                  text,
		ParseMode:             "HTML",
		DisableWebPagePreview: true,
		ReplyMarkup:           markup,
	})
	if err != nil {
		log.FromContext(ctx).Warn("telegram editMessageText failed", "chat_id", chatID, "message_id", msgID, "err", err)
	}
}

func (s *Service) answerCallback(ctx context.Context, callbackID, text string, alert bool) {
	err := s.client.AnswerCallbackQuery(ctx, telegram.AnswerCallbackQueryRequest{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       alert,
	})
	if err != nil {
		log.FromContext(ctx).Warn("telegram answerCallbackQuery failed", "err", err)
	}
}

// parseCommand parses "/cmd", "/cmd@botname", or "/cmd arg".
func parseCommand(text string) (cmd, arg string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	head := strings.TrimPrefix(fields[0], "/")
	if i := strings.IndexByte(head, '@'); i >= 0 {
		head = head[:i]
	}
	cmd = strings.ToLower(head)
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	return cmd, arg
}

// providerMeta maps compact callback codes (1 char to stay well under
// Telegram's 64-byte callback_data limit) to provider keys and display labels.
type providerMeta struct {
	code  string
	key   string
	label string
	short string
}

var providerCatalog = []providerMeta{
	{code: "a", key: "", label: "All", short: "All"},
	{code: "n", key: nocodb.ProviderType, label: "NocoDB", short: "NocoDB"},
	{code: "w", key: wasabi.ProviderType, label: "Wasabi", short: "Wasabi"},
	{code: "c", key: cloudflare.ProviderType, label: "Cloudflare", short: "Cloudflare"},
	{code: "g", key: googleworkspace.ProviderType, label: "Google Workspace", short: "Workspace"},
}

func providerByCode(code string) providerMeta {
	for _, p := range providerCatalog {
		if p.code == code {
			return p
		}
	}
	return providerCatalog[0]
}

func providerLabel(key string) string {
	for _, p := range providerCatalog {
		if p.key == key && key != "" {
			return p.label
		}
	}
	return key
}

func statusIcon(st connrepo.Status) string {
	switch st {
	case connrepo.StatusOK:
		return "✅"
	case connrepo.StatusError:
		return "❌"
	default:
		return "❔"
	}
}

func statusText(st connrepo.Status) string {
	switch st {
	case connrepo.StatusOK:
		return "✅ OK"
	case connrepo.StatusError:
		return "❌ Error"
	default:
		return "❔ Unknown"
	}
}
