package config

import "time"

// AppConfig is the YAML startup config, loaded by Butterfly from the file
// pointed to by BUTTERFLY_CONFIG_FILE_PATH.
type AppConfig struct {
	Auth AuthConfig `yaml:"auth"`

	// DBStore is a key under butterfly's `store.db`. That entry must use
	// driver postgres.
	DBStore string `yaml:"db_store"`

	Crypto  CryptoConfig  `yaml:"crypto"`
	Storage StorageConfig `yaml:"storage"`
	NocoDB  NocoDBConfig  `yaml:"nocodb"`
}

func (c *AppConfig) EffectiveDBStore() string {
	if c.DBStore == "" {
		return "main"
	}
	return c.DBStore
}

// CryptoConfig holds the key protecting stored third-party credentials.
type CryptoConfig struct {
	// EncryptionKey is a 16/24/32-byte AES key, raw, hex, or base64. Without
	// it, credentials (e.g. NocoDB API tokens) cannot be stored.
	EncryptionKey string `yaml:"encryption_key"`
}

// StorageConfig selects where large blobs (snapshot content) are stored.
type StorageConfig struct {
	// S3Store is a key under butterfly's `store.s3`. When empty, blobs go
	// to LocalDir instead (development only).
	S3Store   string `yaml:"s3_store"`
	KeyPrefix string `yaml:"key_prefix"`
	LocalDir  string `yaml:"local_dir"`
}

func (s StorageConfig) EffectiveLocalDir() string {
	if s.LocalDir == "" {
		return "./data/blobs"
	}
	return s.LocalDir
}

// NocoDBConfig tunes NocoDB snapshot runs.
type NocoDBConfig struct {
	// RequestsPerSecond caps calls per NocoDB connection client. NocoDB
	// Cloud allows 5/s; self-hosted instances can usually go higher.
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	// Workers is how many snapshots run concurrently.
	Workers int `yaml:"workers"`
	// PageSize is the record page size when reading tables.
	PageSize int `yaml:"page_size"`
	// SnapshotTimeout bounds one snapshot run.
	SnapshotTimeout time.Duration `yaml:"snapshot_timeout"`
}

type AuthConfig struct {
	InitialAdminUsername string        `yaml:"initial_admin_username"`
	InitialAdminPassword string        `yaml:"initial_admin_password"`
	SessionTTL           time.Duration `yaml:"session_ttl"`
	// AllowUnauthenticated lets the AuthMiddleware grant admin to every
	// request when no auth repo is wired. Intended for local development
	// only — production deployments must leave this false so a misconfigured
	// bootstrap fails closed instead of silently exposing all data.
	AllowUnauthenticated bool                           `yaml:"allow_unauthenticated"`
	OAuthProviders       map[string]OAuthProviderConfig `yaml:"oauth_providers"`
}

// OAuthProviderConfig holds the client credentials and OAuth endpoints for a
// single third-party login provider. Only providers whose ClientID and
// ClientSecret are set are exposed via ListOAuthProviders; the rest are
// effectively disabled.
type OAuthProviderConfig struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	RedirectURL  string   `yaml:"redirect_url"`
	Scopes       []string `yaml:"scopes"`
	// DisplayName is shown on the login page (e.g. "GitHub"). Defaults to a
	// titlecase version of the provider key when empty.
	DisplayName string `yaml:"display_name"`
}

// Enabled reports whether the provider has the minimum credentials needed
// to participate in the OAuth flow.
func (c OAuthProviderConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

func (c AuthConfig) EffectiveSessionTTL() time.Duration {
	if c.SessionTTL <= 0 {
		return 7 * 24 * time.Hour
	}
	return c.SessionTTL
}

func (c *AppConfig) Print() {}
