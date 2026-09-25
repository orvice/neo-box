package wasabi

import (
	"fmt"
	"time"
)

// ProviderType is the provider key of Wasabi connections.
const ProviderType = "wasabi"

// DefaultPricePerTBMonth is Wasabi's Pay-Go storage price in USD
// (from 1 July 2026).
const DefaultPricePerTBMonth = 7.99

// DateLayout is how days are written in settings and in the API.
const DateLayout = "2006-01-02"

// ConnectionConfig is a Wasabi connection's non-secret settings.
type ConnectionConfig struct {
	AccessKeyID string `json:"access_key_id"`
	// PricePerTBMonth is the storage price used for the cost estimate. Zero
	// means DefaultPricePerTBMonth.
	PricePerTBMonth float64 `json:"price_per_tb_month,omitempty"`
	// BillingCycleAnchor is a day (DateLayout) on which a 30-day billing
	// cycle started; empty estimates over the last 30 days instead.
	BillingCycleAnchor  string `json:"billing_cycle_anchor,omitempty"`
	CostEstimateEnabled bool   `json:"cost_estimate_enabled"`
}

// ConnectionSecret is a Wasabi connection's secret settings.
type ConnectionSecret struct {
	SecretKey string `json:"secret_key"`
}

// Price returns the storage price in USD per TB-month.
func (c ConnectionConfig) Price() float64 {
	if c.PricePerTBMonth <= 0 {
		return DefaultPricePerTBMonth
	}
	return c.PricePerTBMonth
}

// Anchor returns the billing-cycle anchor day, or the zero time.
func (c ConnectionConfig) Anchor() (time.Time, error) {
	if c.BillingCycleAnchor == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(DateLayout, c.BillingCycleAnchor)
	if err != nil {
		return time.Time{}, fmt.Errorf("billing_cycle_anchor must be YYYY-MM-DD: %w", err)
	}
	return t, nil
}
