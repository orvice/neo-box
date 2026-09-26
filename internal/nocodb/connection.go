package nocodb

// ProviderType is the provider key of NocoDB connections.
const ProviderType = "nocodb"

// DefaultRequestsPerSecond matches NocoDB Cloud's rate limit.
const DefaultRequestsPerSecond = 5

// ConnectionConfig is a NocoDB connection's non-secret settings.
type ConnectionConfig struct {
	BaseURL string `json:"base_url"`
	// RequestsPerSecond caps calls to this instance; zero means
	// DefaultRequestsPerSecond. Self-hosted instances can usually go
	// higher, which speeds up link-heavy Bases.
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"`
}

// RateLimit is the effective requests-per-second cap.
func (c ConnectionConfig) RateLimit() float64 {
	if c.RequestsPerSecond <= 0 {
		return DefaultRequestsPerSecond
	}
	return c.RequestsPerSecond
}

// ConnectionSecret is a NocoDB connection's secret settings.
type ConnectionSecret struct {
	APIToken string `json:"api_token"`
}
