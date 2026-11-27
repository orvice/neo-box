package tgbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/googleworkspace"
	"go.orx.me/apps/neo-box/internal/nocodb"
	"go.orx.me/apps/neo-box/internal/porkbun"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/telegram"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

const (
	connectionsPageSize = 8
	maxStatusErrorsList = 10
	timeFormatUTC       = "2006-01-02 15:04 UTC"
)

func mainMenuMarkup() *telegram.InlineKeyboardMarkup {
	return &telegram.InlineKeyboardMarkup{
		InlineKeyboard: [][]telegram.InlineKeyboardButton{
			{
				{Text: "📋 Connections", CallbackData: "cl:a:1"},
				{Text: "📊 Status", CallbackData: "cs"},
			},
		},
	}
}

func (s *Service) renderUnboundPrompt() string {
	var b strings.Builder
	b.WriteString("This Telegram account is not linked to a Neo Box user yet.\n\n")
	b.WriteString("Open <b>Settings → Telegram Bot</b> in Neo Box to generate a one-time link.")
	if s.cfg.DashboardURL != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s/settings/telegram\">Open Neo Box Settings</a>", telegram.Escape(s.cfg.DashboardURL))
	}
	return b.String()
}

func renderHelp() string {
	return strings.Join([]string{
		"<b>Neo Box Bot Commands</b>",
		"",
		"/connections — List and inspect your Connections",
		"/status — Connection health overview",
		"/unbind — Disconnect this Telegram account",
		"/help — Show this help message",
	}, "\n")
}

func (s *Service) buildConnectionList(ctx context.Context, userID, providerCode string, page int) (string, *telegram.InlineKeyboardMarkup, error) {
	pm := providerByCode(providerCode)
	conns, err := s.conns.List(ctx, connrepo.Filter{
		UserID:   userID,
		Provider: pm.key,
	})
	if err != nil {
		return "", nil, err
	}

	total := len(conns)
	totalPages := max((total+connectionsPageSize-1)/connectionsPageSize, 1)
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	var b strings.Builder
	if pm.key == "" {
		b.WriteString("<b>Connections</b>")
	} else {
		fmt.Fprintf(&b, "<b>Connections · %s</b>", telegram.Escape(pm.label))
	}

	var rows [][]telegram.InlineKeyboardButton

	// Provider filter rows.
	rows = append(rows,
		[]telegram.InlineKeyboardButton{
			filterButton(providerCatalog[0], pm.code),
			filterButton(providerCatalog[1], pm.code),
			filterButton(providerCatalog[2], pm.code),
		},
		[]telegram.InlineKeyboardButton{
			filterButton(providerCatalog[3], pm.code),
			filterButton(providerCatalog[4], pm.code),
			filterButton(providerCatalog[5], pm.code),
		},
	)

	if total == 0 {
		if pm.key == "" {
			b.WriteString("\n\nNo Connections yet. Add one in the Neo Box dashboard.")
		} else {
			fmt.Fprintf(&b, "\n\nNo %s Connections found.", telegram.Escape(pm.label))
		}
	} else {
		start := (page - 1) * connectionsPageSize
		end := min(start+connectionsPageSize, total)
		fmt.Fprintf(&b, "\nShowing %d–%d of %d\n", start+1, end, total)

		for _, c := range conns[start:end] {
			name := telegram.Truncate(c.Name, 60)
			if pm.key == "" {
				fmt.Fprintf(&b, "\n%s <b>%s</b> · %s", statusIcon(c.Status), telegram.Escape(name), telegram.Escape(providerLabel(c.Provider)))
			} else {
				fmt.Fprintf(&b, "\n%s <b>%s</b>", statusIcon(c.Status), telegram.Escape(name))
			}

			btnLabel := fmt.Sprintf("%s %s", statusIcon(c.Status), telegram.Truncate(c.Name, 36))
			rows = append(rows, []telegram.InlineKeyboardButton{
				{
					Text:         btnLabel,
					CallbackData: detailCallbackData(c.ID, pm.code, page),
				},
			})
		}
	}

	if totalPages > 1 {
		var nav []telegram.InlineKeyboardButton
		if page > 1 {
			nav = append(nav, telegram.InlineKeyboardButton{
				Text:         "◀ Prev",
				CallbackData: fmt.Sprintf("cl:%s:%d", pm.code, page-1),
			})
		}
		nav = append(nav, telegram.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d / %d", page, totalPages),
			CallbackData: fmt.Sprintf("cl:%s:%d", pm.code, page),
		})
		if page < totalPages {
			nav = append(nav, telegram.InlineKeyboardButton{
				Text:         "Next ▶",
				CallbackData: fmt.Sprintf("cl:%s:%d", pm.code, page+1),
			})
		}
		rows = append(rows, nav)
	}

	rows = append(rows, []telegram.InlineKeyboardButton{
		{Text: "🔄 Refresh", CallbackData: fmt.Sprintf("cl:%s:%d", pm.code, page)},
		{Text: "📊 Status", CallbackData: "cs"},
	})

	return b.String(), &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

func (s *Service) buildConnectionDetail(ctx context.Context, userID, connID, providerCode string, page int) (string, *telegram.InlineKeyboardMarkup, bool, error) {
	c, err := s.conns.Get(ctx, userID, connID)
	if err != nil {
		if errors.Is(err, connrepo.ErrNotFound) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	pm := providerByCode(providerCode)

	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b>\n", telegram.Escape(telegram.Truncate(c.Name, 120)))
	fmt.Fprintf(&b, "Provider: %s\n", telegram.Escape(providerLabel(c.Provider)))
	fmt.Fprintf(&b, "Status: %s\n", statusText(c.Status))
	if msg := strings.TrimSpace(c.StatusMessage); msg != "" {
		fmt.Fprintf(&b, "Error: <code>%s</code>\n", telegram.Escape(telegram.Truncate(msg, 400)))
	}
	fmt.Fprintf(&b, "Checked: %s\n", formatTime(c.StatusCheckedAt))

	if lines := nonSecretConfigLines(c); len(lines) > 0 {
		b.WriteString("\n<b>Configuration</b>\n")
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	fmt.Fprintf(&b, "\nCreated: %s\nUpdated: %s", formatTime(c.CreatedAt), formatTime(c.UpdatedAt))

	actionRow := []telegram.InlineKeyboardButton{
		{Text: "🔄 Refresh", CallbackData: detailCallbackData(c.ID, pm.code, page)},
	}
	if s.cfg.DashboardURL != "" {
		actionRow = append(actionRow, telegram.InlineKeyboardButton{
			Text: "↗ Open in Neo Box",
			URL:  s.cfg.DashboardURL + "/connections/" + c.ID,
		})
	}
	backRow := []telegram.InlineKeyboardButton{
		{Text: "« Back to list", CallbackData: fmt.Sprintf("cl:%s:%d", pm.code, max(page, 1))},
	}

	markup := &telegram.InlineKeyboardMarkup{
		InlineKeyboard: [][]telegram.InlineKeyboardButton{actionRow, backRow},
	}
	return b.String(), markup, true, nil
}

func (s *Service) buildStatusOverview(ctx context.Context, userID string) (string, *telegram.InlineKeyboardMarkup, error) {
	conns, err := s.conns.List(ctx, connrepo.Filter{UserID: userID})
	if err != nil {
		return "", nil, err
	}

	var okCount, errCount, unkCount int
	byProvider := map[string]int{}
	var failing []*connrepo.Connection
	for _, c := range conns {
		byProvider[c.Provider]++
		switch c.Status {
		case connrepo.StatusOK:
			okCount++
		case connrepo.StatusError:
			errCount++
			failing = append(failing, c)
		default:
			unkCount++
		}
	}

	var b strings.Builder
	b.WriteString("<b>Connection Status</b>\n")
	if len(conns) == 0 {
		b.WriteString("\nNo Connections configured yet.")
	} else {
		fmt.Fprintf(&b, "\nTotal: <b>%d</b> · ✅ %d OK · ❌ %d Error", len(conns), okCount, errCount)
		if unkCount > 0 {
			fmt.Fprintf(&b, " · ❔ %d Unknown", unkCount)
		}

		var provParts []string
		for _, pm := range providerCatalog[1:] {
			if n := byProvider[pm.key]; n > 0 {
				provParts = append(provParts, fmt.Sprintf("%s: %d", pm.short, n))
			}
		}
		if len(provParts) > 0 {
			fmt.Fprintf(&b, "\nProviders: %s", telegram.Escape(strings.Join(provParts, " · ")))
		}
	}

	var rows [][]telegram.InlineKeyboardButton
	if len(failing) > 0 {
		b.WriteString("\n\n<b>Failing Connections</b>")
		limit := min(len(failing), maxStatusErrorsList)
		for _, c := range failing[:limit] {
			fmt.Fprintf(&b, "\n❌ <b>%s</b> (%s)", telegram.Escape(telegram.Truncate(c.Name, 60)), telegram.Escape(providerLabel(c.Provider)))
			if msg := strings.TrimSpace(c.StatusMessage); msg != "" {
				fmt.Fprintf(&b, "\n   <code>%s</code>", telegram.Escape(telegram.Truncate(msg, 120)))
			}
			rows = append(rows, []telegram.InlineKeyboardButton{
				{
					Text:         fmt.Sprintf("❌ %s", telegram.Truncate(c.Name, 36)),
					CallbackData: detailCallbackData(c.ID, "a", 1),
				},
			})
		}
		if len(failing) > limit {
			fmt.Fprintf(&b, "\n… and %d more", len(failing)-limit)
		}
	}

	rows = append(rows, []telegram.InlineKeyboardButton{
		{Text: "🔄 Refresh", CallbackData: "cs"},
		{Text: "📋 Connections", CallbackData: "cl:a:1"},
	})

	return b.String(), &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}, nil
}

// nonSecretConfigLines formats a connection's non-secret JSON config for
// display. It never reads or decrypts SecretCiphertext.
func nonSecretConfigLines(c *connrepo.Connection) []string {
	switch c.Provider {
	case nocodb.ProviderType:
		var cfg nocodb.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) != nil {
			return nil
		}
		return []string{
			fmt.Sprintf("• Base URL: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.BaseURL, 200))),
			fmt.Sprintf("• Rate limit: %g req/s", cfg.RateLimit()),
		}
	case wasabi.ProviderType:
		var cfg wasabi.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) != nil {
			return nil
		}
		lines := []string{
			fmt.Sprintf("• Access key ID: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.AccessKeyID, 120))),
			fmt.Sprintf("• Price: $%.2f / TB-month", cfg.Price()),
		}
		if cfg.BillingCycleAnchor != "" {
			lines = append(lines, fmt.Sprintf("• Billing anchor: <code>%s</code>", telegram.Escape(cfg.BillingCycleAnchor)))
		}
		if cfg.CostEstimateEnabled {
			lines = append(lines, "• Cost estimate: Enabled")
		} else {
			lines = append(lines, "• Cost estimate: Disabled")
		}
		if cfg.BudgetUSD > 0 {
			lines = append(lines, fmt.Sprintf("• Budget: $%.2f / cycle", cfg.BudgetUSD))
		}
		return lines
	case cloudflare.ProviderType:
		var cfg cloudflare.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) != nil {
			return nil
		}
		return []string{
			fmt.Sprintf("• Account ID: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.AccountID, 120))),
		}
	case porkbun.ProviderType:
		var cfg porkbun.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) != nil {
			return nil
		}
		env := "Production"
		if cfg.IsSandbox {
			env = "Sandbox"
		}
		return []string{
			fmt.Sprintf("• API key: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.APIKey, 120))),
			fmt.Sprintf("• Environment: %s", env),
		}
	case googleworkspace.ProviderType:
		var cfg googleworkspace.ConnectionConfig
		if json.Unmarshal(c.Config, &cfg) != nil {
			return nil
		}
		return []string{
			fmt.Sprintf("• Service Account: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.ServiceAccountEmail, 200))),
			fmt.Sprintf("• Client ID: <code>%s</code>", telegram.Escape(telegram.Truncate(cfg.ClientID, 120))),
		}
	default:
		return nil
	}
}

func filterButton(pm providerMeta, activeCode string) telegram.InlineKeyboardButton {
	text := pm.short
	if pm.code == activeCode {
		text = "• " + text + " •"
	}
	return telegram.InlineKeyboardButton{
		Text:         text,
		CallbackData: fmt.Sprintf("cl:%s:1", pm.code),
	}
}

// detailCallbackData encodes connection detail navigation while guaranteeing
// the payload never exceeds Telegram's 64-byte callback_data limit.
func detailCallbackData(connID, providerCode string, page int) string {
	full := fmt.Sprintf("cv:%s:%s:%d", connID, providerCode, max(page, 1))
	if len(full) <= telegram.MaxCallbackDataBytes {
		return full
	}
	short := "cv:" + connID
	if len(short) <= telegram.MaxCallbackDataBytes {
		return short
	}
	return short[:telegram.MaxCallbackDataBytes]
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return t.UTC().Format(timeFormatUTC)
}
