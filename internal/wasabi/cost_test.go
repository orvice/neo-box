package wasabi

import (
	"math"
	"testing"
	"time"
)

const tib = 1 << 40

func approx(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.12f, want %.12f", what, got, want)
	}
}

func TestDailyCharge(t *testing.T) {
	price := 7.99
	perTBDay := price / 30

	// Below 1 TB active is billed as 1 TB.
	approx(t, "tiny account", DailyCharge(Usage{PaddedStorageSizeBytes: 10 * gib}, price), perTBDay)
	// 2 TB active.
	approx(t, "2 TB", DailyCharge(Usage{PaddedStorageSizeBytes: 2 * tib}, price), 2*perTBDay)
	// Metadata counts as active storage.
	approx(t, "metadata", DailyCharge(Usage{PaddedStorageSizeBytes: 2*tib - gib, MetadataStorageSizeBytes: gib}, price), 2*perTBDay)
	// Deleted storage is billed on top, with no minimum of its own.
	approx(t, "deleted", DailyCharge(Usage{PaddedStorageSizeBytes: 10 * gib, DeletedStorageSizeBytes: tib / 2}, price), 1.5*perTBDay)
}

func days(from time.Time, n int, u Usage) []Usage {
	var out []Usage
	for i := 0; i < n; i++ {
		d := u
		d.Day = from.AddDate(0, 0, i)
		out = append(out, d)
	}
	return out
}

func TestEstimateWithAnchor(t *testing.T) {
	price := 7.99
	perTBDay := price / 30
	anchor := day("2026-08-01")
	today := day("2026-09-10") // cycles: 08-01..08-30, 08-31..09-29

	history := days(day("2026-08-20"), 21, Usage{PaddedStorageSizeBytes: 2 * tib, DownloadBytes: gib})
	e := EstimateCost(history, price, anchor, today.Add(15*time.Hour))
	if e.Rolling || !e.PeriodStart.Equal(day("2026-08-31")) || !e.PeriodEnd.Equal(day("2026-09-29")) {
		t.Fatalf("period = %s..%s rolling=%v", e.PeriodStart, e.PeriodEnd, e.Rolling)
	}
	// 08-31..09-09 are in the cycle: 10 days.
	if e.DaysWithData != 10 || !e.DataThrough.Equal(day("2026-09-09")) {
		t.Fatalf("days with data = %d through %s", e.DaysWithData, e.DataThrough)
	}
	approx(t, "cost to date", e.CostToDate, 10*2*perTBDay)
	approx(t, "projected", e.ProjectedCost, 30*2*perTBDay)
	if e.EgressBytes != 10*gib || e.EgressExceedsStorage {
		t.Fatalf("egress = %d exceeds=%v", e.EgressBytes, e.EgressExceedsStorage)
	}
}

func TestEstimateRollingAndEgress(t *testing.T) {
	price := 6.0
	today := day("2026-09-30")
	// 40 days of history; only the last 30 count. Egress: 30 GB/day for
	// 30 days = 900 GB, above 100 GB of storage.
	history := days(day("2026-08-22"), 40, Usage{PaddedStorageSizeBytes: 100 * gib, DownloadBytes: 30 * gib})
	e := EstimateCost(history, price, time.Time{}, today)
	if !e.Rolling || !e.PeriodStart.Equal(day("2026-09-01")) || !e.PeriodEnd.Equal(today) {
		t.Fatalf("period = %s..%s rolling=%v", e.PeriodStart, e.PeriodEnd, e.Rolling)
	}
	if e.DaysWithData != 30 || !e.EgressExceedsStorage || e.EgressBytes != 900*gib {
		t.Fatalf("days=%d egress=%d exceeds=%v", e.DaysWithData, e.EgressBytes, e.EgressExceedsStorage)
	}
	// 100 GB is below the 1 TB minimum.
	approx(t, "cost", e.CostToDate, 30*price/30)
	approx(t, "run rate", e.ProjectedCost, price)
}

func TestEstimateWithoutData(t *testing.T) {
	e := EstimateCost(nil, 7.99, time.Time{}, day("2026-09-30"))
	if e.DaysWithData != 0 || e.CostToDate != 0 || e.ProjectedCost != 0 || !e.DataThrough.IsZero() {
		t.Fatalf("empty estimate = %+v", e)
	}
}

func TestCycleStart(t *testing.T) {
	anchor := day("2026-08-01")
	for _, tc := range []struct{ day, want string }{
		{"2026-08-01", "2026-08-01"},
		{"2026-08-30", "2026-08-01"},
		{"2026-08-31", "2026-08-31"},
		{"2026-07-31", "2026-07-02"}, // an anchor in the future counts back
		{"2026-07-02", "2026-07-02"},
	} {
		if got := CycleStart(anchor, day(tc.day)); !got.Equal(day(tc.want)) {
			t.Errorf("CycleStart(%s) = %s, want %s", tc.day, got.Format(DateLayout), tc.want)
		}
	}
}

func TestConnectionConfig(t *testing.T) {
	if (ConnectionConfig{}).Price() != DefaultPricePerTBMonth {
		t.Fatal("zero price should fall back to the default")
	}
	if a, err := (ConnectionConfig{BillingCycleAnchor: "2026-08-01"}).Anchor(); err != nil || !a.Equal(day("2026-08-01")) {
		t.Fatalf("Anchor = %v, %v", a, err)
	}
	if _, err := (ConnectionConfig{BillingCycleAnchor: "08/01/2026"}).Anchor(); err == nil {
		t.Fatal("a malformed anchor should fail")
	}
}
