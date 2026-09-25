package nocodb

// ProviderType is the provider key of NocoDB connections.
const ProviderType = "nocodb"

// ConnectionConfig is a NocoDB connection's non-secret settings.
type ConnectionConfig struct {
	BaseURL string `json:"base_url"`
}

// ConnectionSecret is a NocoDB connection's secret settings.
type ConnectionSecret struct {
	APIToken string `json:"api_token"`
}
