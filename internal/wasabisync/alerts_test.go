package wasabisync

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.orx.me/apps/neo-box/internal/notify"
	connrepo "go.orx.me/apps/neo-box/internal/repo/connection"
	"go.orx.me/apps/neo-box/internal/wasabi"
)

const tib = 1 << 40

type recordingNotifier struct {
	mu     sync.Mutex
	alerts []notify.Alert
}

func (n *recordingNotifier) Notify(_ context.Context, a notify.Alert) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.alerts = append(n.alerts, a)
}

func (n *recordingNotifier) byKind() map[string]notify.Alert {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := map[string]notify.Alert{}
	for _, a := range n.alerts {
		out[a.Kind] = a
	}
	return out
}

func budgetConn(budget float64, estimate bool) *connrepo.Connection {
	c := wasabiConn("w1")
	c.Name = "main"
	cfg, _ := json.Marshal(wasabi.ConnectionConfig{
		AccessKeyID: "AK", CostEstimateEnabled: estimate, BudgetUSD: budget, BillingCycleAnchor: "2026-09-15",
	})
	c.Config = cfg
	return c
}

// seed stores 15 days (09-15..09-29) of 3 TB active storage downloading
// 0.5 TB a day, with a large delete on 09-29 and an older one on 09-20.
func seed(t *testing.T, h *harness) {
	t.Helper()
	var rows []wasabi.Usage
	for d := day("2026-09-15"); d.Before(day("2026-09-30")); d = d.AddDate(0, 0, 1) {
		u := wasabi.Usage{Day: d, PaddedStorageSizeBytes: 3 * tib, DownloadBytes: tib / 2, DeletedStorageSizeBytes: tib / 10}
		switch d {
		case day("2026-09-29"):
			u.DeleteBytes = tib
		case day("2026-09-20"):
			u.DeleteBytes = 2 * tib
		}
		rows = append(rows, u)
	}
	if err := h.repo.UpsertUsage(context.Background(), "w1", rows); err != nil {
		t.Fatal(err)
	}
}

func TestAlertRules(t *testing.T) {
	// 3.1 TB billed a day at 7.99: 15 days so far = 12.38, projected 24.77.
	for _, tc := range []struct {
		name     string
		budget   float64
		estimate bool
		want     []string
	}{
		{"projected over budget", 20, true, []string{"budget_projected", "egress_over_storage", "large_deletes"}},
		{"already over budget", 10, true, []string{"budget_exceeded", "egress_over_storage", "large_deletes"}},
		{"within budget", 100, true, []string{"egress_over_storage", "large_deletes"}},
		{"no cost estimate", 10, false, []string{"large_deletes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := budgetConn(tc.budget, tc.estimate)
			h := newHarness(t, conn)
			seed(t, h)
			n := &recordingNotifier{}
			h.m.SetNotifier(n)
			h.m.checkAlerts(context.Background(), conn)

			var kinds []string
			for k := range n.byKind() {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			if strings.Join(kinds, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("alerts = %v, want %v", kinds, tc.want)
			}
			for _, a := range n.alerts {
				if a.UserID != "u1" || a.Source != "wasabi" || a.ConnectionID != "w1" || a.Link != "/connections/w1" {
					t.Fatalf("alert = %+v", a)
				}
			}
		})
	}
}

func TestAlertContentAndKeys(t *testing.T) {
	conn := budgetConn(20, true)
	h := newHarness(t, conn)
	seed(t, h)
	n := &recordingNotifier{}
	h.m.SetNotifier(n)
	h.m.checkAlerts(context.Background(), conn)
	h.m.checkAlerts(context.Background(), conn) // a later sync raises the same keys

	got := n.byKind()
	if a := got["budget_projected"]; a.Key != "wasabi:w1:budget_projected:2026-09-15" || a.Severity != notify.Warning ||
		a.Title != "main is on track to go over its $20.00 budget" || !strings.Contains(a.Body, "Projected $24.77 this cycle (2026-09-15 – 2026-10-14)") {
		t.Fatalf("budget alert = %+v", a)
	}
	// Only 09-29's delete is recent enough; 09-20's is from the backfill.
	del := got["large_deletes"]
	if del.Key != "wasabi:w1:large_deletes:2026-09-29" || del.Title != "main: 1.0 TB deleted on 2026-09-29" ||
		!strings.Contains(del.Body, "33% of the 3.0 TB stored the day before") {
		t.Fatalf("delete alert = %+v", del)
	}
	if e := got["egress_over_storage"]; e.Key != "wasabi:w1:egress_over_storage:2026-09-15" || !strings.Contains(e.Body, "7.5 TB downloaded") {
		t.Fatalf("egress alert = %+v", e)
	}
	keys := map[string]int{}
	for _, a := range n.alerts {
		keys[a.Key]++
	}
	for k, c := range keys {
		if c != 2 {
			t.Fatalf("key %s raised %d times over two checks, want the same key twice", k, c)
		}
	}
}

func TestSyncChecksAlerts(t *testing.T) {
	// The fake account is tiny, so the 1 TB minimum (0.27/day) passes a
	// $1 budget within the backfilled cycle.
	conn := budgetConn(1, true)
	h := newHarness(t, conn)
	n := &recordingNotifier{}
	h.m.SetNotifier(n)
	h.start(t)
	h.m.Refresh("w1")
	h.waitIdle(t, "w1")
	if _, ok := n.byKind()["budget_exceeded"]; !ok {
		t.Fatalf("alerts after sync = %+v", n.alerts)
	}
}
