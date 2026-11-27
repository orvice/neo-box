package porkbun

import (
	"fmt"
	"strings"
)

// ProviderType is the provider key of Porkbun connections.
const ProviderType = "porkbun"

// ConnectionConfig is a Porkbun connection's non-secret settings.
type ConnectionConfig struct {
	APIKey    string `json:"api_key"`
	IsSandbox bool   `json:"is_sandbox"`
}

// ConnectionSecret is a Porkbun connection's secret settings.
type ConnectionSecret struct {
	SecretAPIKey string `json:"secret_api_key"`
}

// IsSandboxKey reports whether apiKey is a Porkbun sandbox key (pk1_sb_...).
func IsSandboxKey(apiKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(apiKey), "pk1_sb_")
}

// NormalizeAPIKey trims whitespace and checks that an API key is non-empty and
// contains no embedded whitespace.
func NormalizeAPIKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("porkbun: api key is required")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return "", fmt.Errorf("porkbun: api key must not contain whitespace")
	}
	return key, nil
}
