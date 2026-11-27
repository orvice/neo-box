package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// TelegramWebhookPath is the public endpoint Telegram posts bot updates to.
// It is authenticated by X-Telegram-Bot-Api-Secret-Token inside the handler,
// not by user Bearer sessions.
const TelegramWebhookPath = "/api/telegram/webhook"

// TelegramWebhookProvider returns the active webhook handler once bootstrap
// has started the Telegram bot, or nil when the bot is disabled.
type TelegramWebhookProvider func() http.HandlerFunc

// RegisterTelegramWebhook mounts POST /api/telegram/webhook.
func RegisterTelegramWebhook(r *gin.Engine, provider TelegramWebhookProvider) {
	r.POST(TelegramWebhookPath, func(c *gin.Context) {
		var h http.HandlerFunc
		if provider != nil {
			h = provider()
		}
		if h == nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "telegram bot is not enabled"})
			return
		}
		h(c.Writer, c.Request)
	})
}
