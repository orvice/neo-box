package wasabi

import (
	"math"
	"sort"
	"time"
)

// CycleDays is the length of a Wasabi billing cycle. Invoices run every 30
// days from the day the account became paid, not by calendar month.
const CycleDays = 30

const gib = 1 << 30

// Estimate is an estimated storage charge for one period, in USD. It is
// computed from account-level daily usage with Wasabi's published formulas
// (https://docs.wasabi.com/apidocs/billing-and-utilization-metrics,
// "Converting Metrics to Charges"), per day:
//
//	active  = MAX((PaddedStorageSizeBytes + MetadataStorageSizeBytes) / 1024^3, 1024) GB
//	deleted = DeletedStorageSizeBytes / 1024^3 GB
//	charge  = (active + deleted) * price-per-TB-month / 30 / 1024
//
// The MAX is the 1 TB minimum, applied to active storage only. Egress is not
// priced: Wasabi does not charge for it while monthly egress stays within
// the stored volume, so the estimate only flags when it does not.
type Estimate struct {
	// PeriodStart and PeriodEnd are the period's first and last days.
	PeriodStart time.Time
	PeriodEnd   time.Time
	// Rolling means no billing-cycle anchor is set, so the period is the
	// last CycleDays days.
	Rolling bool
	// DataThrough is the newest day with data in the period, or zero.
	DataThrough  time.Time
	DaysWithData int
	// CostToDate sums the daily charges of the days with data.
	CostToDate float64
	// ProjectedCost is the whole period's charge, filling days without
	// data yet with the newest day's charge. For a rolling period it is
	// the newest day's charge over CycleDays: a monthly run rate.
	ProjectedCost float64
	// EgressBytes is the period's downloaded bytes.
	EgressBytes int64
	// EgressExceedsStorage reports egress above the average active
	// storage: outside Wasabi's free-egress allowance.
	EgressExceedsStorage bool
}

// DailyCharge is the storage charge of one day of account usage.
func DailyCharge(u Usage, pricePerTBMonth float64) float64 {
	active := math.Max(float64(u.ActiveStorageBytes())/gib, 1024)
	deleted := float64(u.DeletedStorageSizeBytes) / gib
	return (active + deleted) * pricePerTBMonth / CycleDays / 1024
}

// EstimateCost estimates the charge for the billing cycle containing today,
// or for the last CycleDays days when anchor is zero. days holds
// account-level usage (Bucket empty) in any order.
func EstimateCost(days []Usage, pricePerTBMonth float64, anchor, today time.Time) Estimate {
	e := Estimate{}
	e.PeriodStart, e.PeriodEnd, e.Rolling = Period(anchor, today)

	inPeriod := daysIn(days, e.PeriodStart, e.PeriodEnd)
	if len(inPeriod) == 0 {
		return e
	}

	var activeSum float64
	for _, u := range inPeriod {
		e.CostToDate += DailyCharge(u, pricePerTBMonth)
		e.EgressBytes += u.DownloadBytes
		activeSum += float64(u.ActiveStorageBytes())
	}
	newest := inPeriod[len(inPeriod)-1]
	e.DataThrough = newest.Day
	e.DaysWithData = len(inPeriod)
	e.EgressExceedsStorage = float64(e.EgressBytes) > activeSum/float64(len(inPeriod))

	newestCharge := DailyCharge(newest, pricePerTBMonth)
	if e.Rolling {
		e.ProjectedCost = newestCharge * CycleDays
	} else {
		e.ProjectedCost = e.CostToDate + newestCharge*float64(CycleDays-e.DaysWithData)
	}
	return e
}

// Period is what an estimate covers: the 30-day billing cycle containing
// today, or the last CycleDays days (rolling) when anchor is zero.
func Period(anchor, today time.Time) (start, end time.Time, rolling bool) {
	today = DayOf(today)
	if anchor.IsZero() {
		return today.AddDate(0, 0, -(CycleDays - 1)), today, true
	}
	start = CycleStart(anchor, today)
	return start, start.AddDate(0, 0, CycleDays-1), false
}

// daysIn returns the rows with from <= Day <= to, oldest first.
func daysIn(days []Usage, from, to time.Time) []Usage {
	var out []Usage
	for _, u := range days {
		if !u.Day.Before(from) && !u.Day.After(to) {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// CycleStart returns the first day of the 30-day billing cycle, counted
// from anchor, that contains day. An anchor after day counts backwards.
func CycleStart(anchor, day time.Time) time.Time {
	anchor, day = DayOf(anchor), DayOf(day)
	offset := int(day.Sub(anchor).Hours() / 24)
	cycles := offset / CycleDays
	if offset < 0 && offset%CycleDays != 0 {
		cycles--
	}
	return anchor.AddDate(0, 0, cycles*CycleDays)
}
