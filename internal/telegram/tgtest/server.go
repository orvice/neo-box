// Package tgtest provides an in-process fake Telegram Bot API server for tests.
package tgtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"go.orx.me/apps/neo-box/internal/telegram"
)

// Server is a stateful fake Telegram Bot API server.
type Server struct {
	*httptest.Server

	mu             sync.Mutex
	token          string
	botUser        telegram.User
	nextMessageID  int
	sent           []telegram.SendMessageRequest
	edited         []telegram.EditMessageTextRequest
	callbackAnswer []telegram.AnswerCallbackQueryRequest
	webhooks       []telegram.SetWebhookRequest
	deleteWebhooks int
	commands       []telegram.BotCommand
	pendingUpdates []telegram.Update
}

// New creates and starts a fake Telegram Bot API server expecting token.
func New(token string, botUsername string) *Server {
	s := &Server{
		token: token,
		botUser: telegram.User{
			ID:        999000,
			IsBot:     true,
			FirstName: "Neo Box",
			Username:  botUsername,
		},
		nextMessageID: 100,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *Server) EnqueueUpdates(updates ...telegram.Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingUpdates = append(s.pendingUpdates, updates...)
}

func (s *Server) SentMessages() []telegram.SendMessageRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telegram.SendMessageRequest, len(s.sent))
	copy(out, s.sent)
	return out
}

func (s *Server) EditedMessages() []telegram.EditMessageTextRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telegram.EditMessageTextRequest, len(s.edited))
	copy(out, s.edited)
	return out
}

func (s *Server) CallbackAnswers() []telegram.AnswerCallbackQueryRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telegram.AnswerCallbackQueryRequest, len(s.callbackAnswer))
	copy(out, s.callbackAnswer)
	return out
}

func (s *Server) Webhooks() []telegram.SetWebhookRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telegram.SetWebhookRequest, len(s.webhooks))
	copy(out, s.webhooks)
	return out
}

func (s *Server) Commands() []telegram.BotCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]telegram.BotCommand, len(s.commands))
	copy(out, s.commands)
	return out
}

func (s *Server) DeleteWebhookCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteWebhooks
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + s.token + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeErr(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)

	switch method {
	case "getMe":
		s.mu.Lock()
		u := s.botUser
		s.mu.Unlock()
		writeOK(w, u)
	case "setWebhook":
		var req telegram.SetWebhookRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		s.mu.Lock()
		s.webhooks = append(s.webhooks, req)
		s.mu.Unlock()
		writeOK(w, true)
	case "deleteWebhook":
		s.mu.Lock()
		s.deleteWebhooks++
		s.mu.Unlock()
		writeOK(w, true)
	case "setMyCommands":
		var req struct {
			Commands []telegram.BotCommand `json:"commands"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		s.mu.Lock()
		s.commands = append([]telegram.BotCommand(nil), req.Commands...)
		s.mu.Unlock()
		writeOK(w, true)
	case "getUpdates":
		var req telegram.GetUpdatesRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		var out []telegram.Update
		var keep []telegram.Update
		for _, u := range s.pendingUpdates {
			if u.UpdateID >= req.Offset {
				out = append(out, u)
			} else {
				keep = append(keep, u)
			}
		}
		s.pendingUpdates = keep
		s.mu.Unlock()
		if out == nil {
			out = []telegram.Update{}
		}
		writeOK(w, out)
	case "sendMessage":
		var req telegram.SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		if err := validateMarkup(req.ReplyMarkup); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		s.nextMessageID++
		id := s.nextMessageID
		s.sent = append(s.sent, req)
		s.mu.Unlock()
		writeOK(w, telegram.Message{
			MessageID: id,
			Chat:      telegram.Chat{ID: req.ChatID, Type: "private"},
			Text:      req.Text,
		})
	case "editMessageText":
		var req telegram.EditMessageTextRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		if err := validateMarkup(req.ReplyMarkup); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.mu.Lock()
		s.edited = append(s.edited, req)
		s.mu.Unlock()
		writeOK(w, true)
	case "answerCallbackQuery":
		var req telegram.AnswerCallbackQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad json")
			return
		}
		s.mu.Lock()
		s.callbackAnswer = append(s.callbackAnswer, req)
		s.mu.Unlock()
		writeOK(w, true)
	default:
		writeErr(w, http.StatusNotFound, "unknown method: "+method)
	}
}

func validateMarkup(m *telegram.InlineKeyboardMarkup) error {
	if m == nil {
		return nil
	}
	for _, row := range m.InlineKeyboard {
		for _, btn := range row {
			if len(btn.CallbackData) > telegram.MaxCallbackDataBytes {
				return &telegram.APIError{ErrorCode: http.StatusBadRequest, Description: "Bad Request: BUTTON_DATA_INVALID"}
			}
		}
	}
	return nil
}

func writeOK(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func writeErr(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": desc})
}
