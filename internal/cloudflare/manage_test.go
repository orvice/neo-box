package cloudflare

import (
	"strings"
	"testing"
)

func TestValidateRoutePattern(t *testing.T) {
	for _, ok := range []string{"example.com/*", "*.example.com/*", "*example.com/api/*", "a.b.example.com/x"} {
		if err := ValidateRoutePattern(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "example.com", "https://example.com/*", "example.com:80/*", "*/*", "localhost/*", "exa mple.com/*", "a.*.com/*"} {
		if err := ValidateRoutePattern(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, zone := RouteHost("*.example.com/api/*"); zone != "example.com" {
		t.Errorf("zone host = %q", zone)
	}
}

func TestValidateInputs(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		ok   bool
	}{
		{"cron", ValidateCron("*/15 * * * *"), true},
		{"cron ranges", ValidateCron("0 3 1-5 * MON,WED"), true},
		{"cron short", ValidateCron("* * * *"), false},
		{"cron junk", ValidateCron("a b c d $"), false},
		{"binding", ValidateBindingName("API_KEY2"), true},
		{"binding digit", ValidateBindingName("2KEY"), false},
		{"binding dash", ValidateBindingName("a-b"), false},
		{"secret", ValidateSecretValue("x"), true},
		{"secret empty", ValidateSecretValue(""), false},
		{"secret huge", ValidateSecretValue(strings.Repeat("x", maxSecretLen+1)), false},
		{"project", ValidatePagesProjectName("my-site-2"), true},
		{"project upper", ValidatePagesProjectName("My-Site"), false},
		{"project dash", ValidatePagesProjectName("-site"), false},
		{"project long", ValidatePagesProjectName(strings.Repeat("a", 59)), false},
		{"branch", ValidateBranch("release/1.2"), true},
		{"branch space", ValidateBranch("a b"), false},
		{"domain", ValidateDomainName("app.example.com"), true},
		{"domain wildcard", ValidateDomainName("*.example.com"), false},
		{"domain scheme", ValidateDomainName("https://app.example.com"), false},
	} {
		if (c.err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, c.err, c.ok)
		}
	}
}
