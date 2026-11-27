package telegram

import (
	"html"
	"unicode/utf8"
)

// MaxMessageRunes is Telegram's 4096-character message limit.
const MaxMessageRunes = 4096

// MaxCallbackDataBytes is Telegram's strict 64-byte limit on inline keyboard
// callback_data.
const MaxCallbackDataBytes = 64

// Escape escapes user or provider text for Telegram HTML parse_mode.
func Escape(s string) string {
	return html.EscapeString(s)
}

// Truncate shortens s to at most maxRunes runes, appending an ellipsis when
// shortened. Always truncate plain text BEFORE wrapping it in HTML tags so
// tags cannot be split mid-element.
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "…"
}
