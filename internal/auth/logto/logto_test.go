package logto

import (
	"go.orx.me/apps/neo-box/internal/config"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	base := config.LogtoConfig{Endpoint: "https://identity.test", AppID: "web", AppSecret: "secret", PublicURL: "https://dashboard.test", OwnerSubject: "owner"}
	if err := ValidateConfig(base); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"", "http://identity.test", "https://user:secret@identity.test", "https://identity.test?x=y"} {
		cfg := base
		cfg.Endpoint = endpoint
		if ValidateConfig(cfg) == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	for _, origin := range []string{"https://dashboard.test/path", "https://dashboard.test?x=y", "http://dashboard.test", "//dashboard.test"} {
		cfg := base
		cfg.PublicURL = origin
		if ValidateConfig(cfg) == nil {
			t.Fatalf("accepted public origin %q", origin)
		}
	}
	for _, owner := range []string{"..", "../owner", " bad "} {
		cfg := base
		cfg.DataOwnerID = owner
		if ValidateConfig(cfg) == nil {
			t.Fatalf("accepted owner %q", owner)
		}
	}
	base.AppSecret = ""
	if ValidateConfig(base) == nil {
		t.Fatal("accepted a public SPA client without a secret")
	}
}

func TestReturnPath(t *testing.T) {
	for _, raw := range []string{"", "https://evil.test", "//evil.test", "/\\evil.test", "/\n/evil.test", "/sign-in", "/api/auth/logout", "/foo/../sign-in", "/auth/callback?code=old"} {
		if got := ReturnPath(raw); got != "/" {
			t.Fatalf("%q => %q", raw, got)
		}
	}
	if got := ReturnPath("/connections?provider=nocodb#list"); got != "/connections?provider=nocodb#list" {
		t.Fatal(got)
	}
}
