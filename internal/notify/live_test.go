package notify

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestLiveTelegram sends a real message. It is skipped unless
// NEOBOX_TEST_TELEGRAM_BOT_TOKEN and NEOBOX_TEST_TELEGRAM_CHAT_ID are set.
func TestLiveTelegram(t *testing.T) {
	token, chat := os.Getenv("NEOBOX_TEST_TELEGRAM_BOT_TOKEN"), os.Getenv("NEOBOX_TEST_TELEGRAM_CHAT_ID")
	if token == "" || chat == "" {
		t.Skip("NEOBOX_TEST_TELEGRAM_BOT_TOKEN / NEOBOX_TEST_TELEGRAM_CHAT_ID are not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, _ := json.Marshal(TelegramConfig{ChatID: chat})
	sec, _ := json.Marshal(TelegramSecret{BotToken: token})
	err := (&Telegram{}).Send(ctx, cfg, sec, Message{
		Severity: Warning,
		Title:    `Neo Box live test: "quotes" & <tags>`,
		Body:     "If this reads correctly, HTML escaping works.\nSecond line.",
		URL:      "https://example.com/connections?a=1&b=2",
	})
	if err != nil {
		t.Fatal(err)
	}
}
