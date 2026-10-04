package wasabi

import (
	"testing"
	"time"
)

func TestCostBreakdown(t *testing.T) {
	price := 7.99
	perTBDay := price / 30
	anchor := day("2026-08-31") // cycle 08-31..09-29
	today := day("2026-09-03")

	// The account holds 3 TB active and 1 TB deleted. Bucket "a" holds
	// 2 TB active and the deleted TB; "b" 0.5 TB; 0.5 TB is not in any
	// bucket row.
	account := days(day("2026-08-30"), 4, Usage{PaddedStorageSizeBytes: 3 * tib, DeletedStorageSizeBytes: tib})
	buckets := append(
		days(day("2026-08-30"), 4, Usage{Bucket: "a", Region: "us-east-1", PaddedStorageSizeBytes: 2 * tib, DeletedStorageSizeBytes: tib, DeleteBytes: gib}),
		// "b" is gone on the newest day.
		days(day("2026-08-31"), 2, Usage{Bucket: "b", Region: "eu-central-1", PaddedStorageSizeBytes: tib / 2})...,
	)

	b := CostBreakdown(account, buckets, price, anchor, today)
	// 08-31..09-02 are in the cycle: 3 days.
	if b.DaysWithData != 3 || !b.DataThrough.Equal(day("2026-09-02")) || b.Rolling {
		t.Fatalf("period = %d days through %s rolling=%v", b.DaysWithData, b.DataThrough, b.Rolling)
	}
	approx(t, "active", b.ActiveCost, 3*3*perTBDay)
	approx(t, "deleted", b.DeletedCost, 3*1*perTBDay)
	approx(t, "minimum", b.MinimumCost, 0)
	est := EstimateCost(account, price, anchor, today)
	approx(t, "total = estimate cost to date", b.Total(), est.CostToDate)

	if len(b.Buckets) != 2 || b.Buckets[0].Bucket != "a" || b.Buckets[1].Bucket != "b" {
		t.Fatalf("buckets = %+v", b.Buckets)
	}
	a, bb := b.Buckets[0], b.Buckets[1]
	approx(t, "a active", a.ActiveCost, 3*2*perTBDay)
	approx(t, "a deleted", a.DeletedCost, 3*1*perTBDay)
	if a.Gone || a.ActiveBytes != 2*tib || a.DeletedBytes != tib || a.DeletedInPeriodBytes != 3*gib || a.Region != "us-east-1" {
		t.Fatalf("a = %+v", a)
	}
	approx(t, "b active", bb.ActiveCost, 2*0.5*perTBDay)
	if !bb.Gone {
		t.Fatalf("b should be gone: %+v", bb)
	}
	// 3 days x 3 TB active + 1 TB deleted, minus what the buckets explain.
	approx(t, "unattributed", b.Unattributed, 3*4*perTBDay-(a.Total()+bb.Total()))
}

func TestCostBreakdownMinimum(t *testing.T) {
	price := 7.99
	perTBDay := price / 30
	today := day("2026-09-10")
	// 256 GiB active: the 1 TB minimum tops up the remaining 768 GiB.
	account := days(day("2026-09-01"), 10, Usage{PaddedStorageSizeBytes: 256 * gib, DeletedStorageSizeBytes: 64 * gib})
	buckets := days(day("2026-09-01"), 10, Usage{Bucket: "x", PaddedStorageSizeBytes: 256 * gib, DeletedStorageSizeBytes: 64 * gib})

	b := CostBreakdown(account, buckets, price, time.Time{}, today)
	if !b.Rolling || b.DaysWithData != 10 {
		t.Fatalf("rolling=%v days=%d", b.Rolling, b.DaysWithData)
	}
	approx(t, "active", b.ActiveCost, 10*0.25*perTBDay)
	approx(t, "minimum", b.MinimumCost, 10*0.75*perTBDay)
	approx(t, "deleted", b.DeletedCost, 10*(64.0/1024)*perTBDay)
	approx(t, "total = estimate", b.Total(), EstimateCost(account, price, time.Time{}, today).CostToDate)
	approx(t, "unattributed", b.Unattributed, 0)
}

func TestCostBreakdownEmpty(t *testing.T) {
	b := CostBreakdown(nil, nil, 7.99, time.Time{}, day("2026-09-10"))
	if b.DaysWithData != 0 || b.Total() != 0 || len(b.Buckets) != 0 {
		t.Fatalf("empty = %+v", b)
	}
}
