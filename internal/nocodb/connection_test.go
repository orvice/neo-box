package nocodb

import "testing"

func TestRateLimit(t *testing.T) {
	if got := (ConnectionConfig{}).RateLimit(); got != DefaultRequestsPerSecond {
		t.Fatalf("zero RateLimit = %v, want the default", got)
	}
	if got := (ConnectionConfig{RequestsPerSecond: 20}).RateLimit(); got != 20 {
		t.Fatalf("RateLimit = %v, want 20", got)
	}
}
