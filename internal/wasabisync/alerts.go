package wasabisync

import (
	"context"
	"fmt"
	"time"

	"butterfly.orx.me/core/log"

	"go.orx.me/apps/neo-box/internal/notify"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

// A day's deletes raise an alert when they are over largeDeleteShare of the
// previous day's active storage and over largeDeleteMinBytes. Only the last
// largeDeleteLookback days are checked, so a backfill doesn't report old
// deletes.
const (
	largeDeleteShare    = 0.25
	largeDeleteMinBytes = 10 << 30
	largeDeleteLookback = 3
)

// SetNotifier makes syncs raise budget, egress and large-delete alerts.
func (m *Manager) SetNotifier(n notify.Notifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = n
}

// checkAlerts raises the connection's alerts from the usage a sync stored.
// Each alert's key holds its period (or day), so it fires once per period.
func (m *Manager) checkAlerts(ctx context.Context, conn *connrepo.Connection) {
	m.mu.Lock()
	n := m.notifier
	m.mu.Unlock()
	if n == nil {
		return
	}
	logger := log.FromContext(ctx)
	var cfg wasabi.ConnectionConfig
	if err := m.conns.Open(conn, &cfg, nil); err != nil {
		logger.Warn("wasabi alerts: open connection failed", "connection_id", conn.ID, "err", err)
		return
	}
	today := wasabi.DayOf(m.now())
	days, err := m.repo.ListUsage(ctx, conn.ID, "", today.AddDate(0, 0, -2*wasabi.CycleDays), today)
	if err != nil {
		logger.Warn("wasabi alerts: list usage failed", "connection_id", conn.ID, "err", err)
		return
	}
	base := notify.Alert{UserID: conn.UserID, Source: "wasabi", ConnectionID: conn.ID, Link: "/connections/" + conn.ID}

	for _, a := range largeDeletes(conn, days, today) {
		n.Notify(ctx, merge(base, a))
	}

	if !cfg.CostEstimateEnabled {
		return
	}
	anchor, err := cfg.Anchor()
	if err != nil {
		return
	}
	e := wasabi.EstimateCost(days, cfg.Price(), anchor, today)
	if e.DaysWithData == 0 {
		return
	}
	// A rolling period moves daily; key it by month so it fires monthly.
	period, span := e.PeriodStart.Format(wasabi.DateLayout), fmt.Sprintf("this cycle (%s – %s)",
		e.PeriodStart.Format(wasabi.DateLayout), e.PeriodEnd.Format(wasabi.DateLayout))
	if e.Rolling {
		period, span = today.Format("2006-01"), "over the last 30 days"
	}
	key := func(kind string) string { return fmt.Sprintf("wasabi:%s:%s:%s", conn.ID, kind, period) }

	if budget := cfg.BudgetUSD; budget > 0 {
		switch {
		case e.CostToDate > budget:
			n.Notify(ctx, merge(base, notify.Alert{
				Kind: "budget_exceeded", Key: key("budget_exceeded"), Severity: notify.Critical,
				Title: fmt.Sprintf("%s is over its %s budget", conn.Name, usd(budget)),
				Body:  fmt.Sprintf("An estimated %s so far %s, against a budget of %s.", usd(e.CostToDate), span, usd(budget)),
			}))
		case e.ProjectedCost > budget || e.TrendProjectedCost > budget:
			n.Notify(ctx, merge(base, notify.Alert{
				Kind: "budget_projected", Key: key("budget_projected"), Severity: notify.Warning,
				Title: fmt.Sprintf("%s is on track to go over its %s budget", conn.Name, usd(budget)),
				Body: fmt.Sprintf("Projected %s %s (%s on the recent trend), against a budget of %s. %s so far.",
					usd(e.ProjectedCost), span, usd(e.TrendProjectedCost), usd(budget), usd(e.CostToDate)),
			}))
		}
	}
	if e.EgressExceedsStorage {
		latest := days[len(days)-1]
		n.Notify(ctx, merge(base, notify.Alert{
			Kind: "egress_over_storage", Key: key("egress_over_storage"), Severity: notify.Warning,
			Title: fmt.Sprintf("%s: egress is above the stored volume", conn.Name),
			Body: fmt.Sprintf("%s downloaded %s, against %s stored. Wasabi's free egress covers up to the stored "+
				"volume; going over it regularly can lead to charges or a plan change.",
				formatBytes(e.EgressBytes), span, formatBytes(latest.ActiveStorageBytes())),
		}))
	}
}

// largeDeletes finds recent days whose deletes are large next to the
// storage of the day before.
func largeDeletes(conn *connrepo.Connection, days []wasabi.Usage, today time.Time) []notify.Alert {
	var out []notify.Alert
	since := today.AddDate(0, 0, -largeDeleteLookback)
	for i := 1; i < len(days); i++ {
		d, prev := days[i], days[i-1]
		if d.Day.Before(since) || !prev.Day.Equal(d.Day.AddDate(0, 0, -1)) {
			continue
		}
		stored := prev.ActiveStorageBytes()
		if d.DeleteBytes <= largeDeleteMinBytes || float64(d.DeleteBytes) <= largeDeleteShare*float64(stored) {
			continue
		}
		day := d.Day.Format(wasabi.DateLayout)
		share := 100.0
		if stored > 0 {
			share = float64(d.DeleteBytes) / float64(stored) * 100
		}
		out = append(out, notify.Alert{
			Kind: "large_deletes", Key: fmt.Sprintf("wasabi:%s:large_deletes:%s", conn.ID, day), Severity: notify.Warning,
			Title: fmt.Sprintf("%s: %s deleted on %s", conn.Name, formatBytes(d.DeleteBytes), day),
			Body: fmt.Sprintf("That is %.0f%% of the %s stored the day before. Objects deleted before they are "+
				"90 days old stay billed until they reach 90 days, so this shows up as deleted storage on the bill "+
				"(now %s).", share, formatBytes(stored), formatBytes(d.DeletedStorageSizeBytes)),
		})
	}
	return out
}

func merge(base, a notify.Alert) notify.Alert {
	a.UserID, a.Source, a.ConnectionID, a.Link = base.UserID, base.Source, base.ConnectionID, base.Link
	return a
}

func usd(v float64) string { return fmt.Sprintf("$%.2f", v) }

// formatBytes formats a size in binary units, like the dashboard: "1.5 TB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), 0
	for v >= unit && i < 5 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", v, []string{"B", "KB", "MB", "GB", "TB", "PB"}[i])
}
