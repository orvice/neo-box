package cloudflare

import (
	"fmt"
	"regexp"
	"strings"
)

// ProviderType is the provider key of Cloudflare connections.
const ProviderType = "cloudflare"

// ConnectionConfig is a Cloudflare connection's non-secret settings: the
// one Account it reads and changes.
type ConnectionConfig struct {
	AccountID string `json:"account_id"`
}

// ConnectionSecret is a Cloudflare connection's secret settings.
type ConnectionSecret struct {
	APIToken string `json:"api_token"`
}

var accountIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NormalizeAccountID trims and lowercases an Account ID and checks that it
// looks like one (32 hex digits).
func NormalizeAccountID(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if !accountIDPattern.MatchString(id) {
		return "", fmt.Errorf("cloudflare: account ID must be 32 hexadecimal characters")
	}
	return id, nil
}
