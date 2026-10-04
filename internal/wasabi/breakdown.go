package wasabi

import (
	"sort"
	"time"
)

// Breakdown splits a period's estimated storage charge, in USD, by what it
// pays for. The account lines (active, deleted, minimum) add up to the
// estimate's cost to date. Bucket lines split active and deleted storage by
// bucket; Wasabi doesn't say whether bucket figures add up to the account's,
// so the difference is kept as Unattributed rather than spread.
type Breakdown struct {
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Rolling      bool
	DataThrough  time.Time
	DaysWithData int

	// ActiveCost pays for active storage (padded bytes plus metadata).
	ActiveCost float64
	// DeletedCost pays for deleted data still billed under the minimum
	// storage duration.
	DeletedCost float64
	// MinimumCost tops active storage up to the 1 TB minimum.
	MinimumCost float64
	// Unattributed is the account's active and deleted cost that the
	// bucket figures don't account for (negative when buckets add up to
	// more).
	Unattributed float64

	// Buckets are sorted by total cost, highest first.
	Buckets []BucketCost
}

// Total is the period's cost to date.
func (b Breakdown) Total() float64 { return b.ActiveCost + b.DeletedCost + b.MinimumCost }

// BucketCost is one bucket's share of a period's charge.
type BucketCost struct {
	Bucket string
	Region string
	// Gone means the bucket has no row on the period's newest day.
	Gone        bool
	ActiveCost  float64
	DeletedCost float64
	// ActiveBytes and DeletedBytes are from the bucket's newest day in the
	// period.
	ActiveBytes  int64
	DeletedBytes int64
	// DeletedInPeriodBytes sums the bytes deleted during the period.
	DeletedInPeriodBytes int64
}

// Total is the bucket's charge for the period.
func (c BucketCost) Total() float64 { return c.ActiveCost + c.DeletedCost }

// CostBreakdown builds the breakdown of the period EstimateCost would use.
// account holds account-level rows and buckets bucket-level rows, in any
// order; only days with account data count, so the account lines match the
// estimate.
func CostBreakdown(account, buckets []Usage, pricePerTBMonth float64, anchor, today time.Time) Breakdown {
	b := Breakdown{}
	b.PeriodStart, b.PeriodEnd, b.Rolling = Period(anchor, today)
	days := daysIn(account, b.PeriodStart, b.PeriodEnd)
	if len(days) == 0 {
		return b
	}
	perGBDay := pricePerTBMonth / CycleDays / 1024
	counted := make(map[time.Time]bool, len(days))
	for _, u := range days {
		counted[u.Day] = true
		active := float64(u.ActiveStorageBytes()) / gib
		b.ActiveCost += active * perGBDay
		b.DeletedCost += float64(u.DeletedStorageSizeBytes) / gib * perGBDay
		if active < 1024 {
			b.MinimumCost += (1024 - active) * perGBDay
		}
	}
	b.DataThrough = days[len(days)-1].Day
	b.DaysWithData = len(days)

	byName := map[string]*BucketCost{}
	newest := map[string]time.Time{}
	for _, u := range daysIn(buckets, b.PeriodStart, b.PeriodEnd) {
		if !counted[u.Day] {
			continue
		}
		c := byName[u.Bucket]
		if c == nil {
			c = &BucketCost{Bucket: u.Bucket}
			byName[u.Bucket] = c
		}
		c.ActiveCost += float64(u.ActiveStorageBytes()) / gib * perGBDay
		c.DeletedCost += float64(u.DeletedStorageSizeBytes) / gib * perGBDay
		c.DeletedInPeriodBytes += u.DeleteBytes
		// Rows come oldest first, so the last one wins.
		c.Region, c.ActiveBytes, c.DeletedBytes = u.Region, u.ActiveStorageBytes(), u.DeletedStorageSizeBytes
		newest[u.Bucket] = u.Day
	}
	attributed := 0.0
	for name, c := range byName {
		c.Gone = newest[name].Before(b.DataThrough)
		attributed += c.Total()
		b.Buckets = append(b.Buckets, *c)
	}
	b.Unattributed = b.ActiveCost + b.DeletedCost - attributed
	sort.Slice(b.Buckets, func(i, j int) bool {
		if ti, tj := b.Buckets[i].Total(), b.Buckets[j].Total(); ti != tj {
			return ti > tj
		}
		return b.Buckets[i].Bucket < b.Buckets[j].Bucket
	})
	return b
}
