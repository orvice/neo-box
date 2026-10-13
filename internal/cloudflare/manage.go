package cloudflare

import (
	"regexp"
	"strings"
)

// Input checks for the Worker and Pages changes. Cloudflare checks more and
// its refusal is shown as is.

var (
	bindingName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	cronField    = regexp.MustCompile(`^[0-9A-Za-z*/,\-?#]+$`)
	projectName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	branchChars  = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)
	maxSecretLen = 5 * 1024
)

// ValidateDomainName checks a custom domain: a DNS name of at least two
// labels, without wildcards, scheme, port or path.
func ValidateDomainName(h string) error {
	if strings.Contains(h, "*") {
		return invalid("hostname", "must not contain a wildcard")
	}
	return ValidateTunnelHostname(h)
}

// RouteHost returns the host part of a Worker route pattern ("*.a.com/x"
// gives "*.a.com"), and the name of the Zone it needs: the host without its
// leading wildcard.
func RouteHost(pattern string) (host, zoneHost string) {
	host, _, _ = strings.Cut(pattern, "/")
	return host, strings.TrimLeft(host, "*.")
}

// ValidateRoutePattern checks a Worker route pattern such as
// "example.com/*" or "*.example.com/api/*": a host, a slash and a path.
func ValidateRoutePattern(p string) error {
	if p == "" {
		return invalid("pattern", "is required")
	}
	if len(p) > 1000 || strings.ContainsAny(p, " \t\n") {
		return invalid("pattern", "must be a route pattern such as example.com/* without spaces")
	}
	if strings.Contains(p, "://") {
		return invalid("pattern", "must not include a scheme (use example.com/*, not https://example.com/*)")
	}
	host, zoneHost := RouteHost(p)
	if !strings.Contains(p, "/") {
		return invalid("pattern", "must include a path, e.g. %s/*", host)
	}
	if strings.ContainsAny(host, ":") {
		return invalid("pattern", "must not include a port")
	}
	if zoneHost == "" || !strings.Contains(zoneHost, ".") || strings.Contains(zoneHost, "*") {
		return invalid("pattern", "must start with a domain name of a zone, optionally with a leading wildcard, e.g. *.example.com/*")
	}
	return nil
}

// ValidateCron checks a cron trigger: five fields.
func ValidateCron(c string) error {
	fields := strings.Fields(c)
	if len(fields) != 5 {
		return invalid("cron", "must have five fields, e.g. */15 * * * *")
	}
	for _, f := range fields {
		if !cronField.MatchString(f) {
			return invalid("cron", "has an invalid field %q", f)
		}
	}
	return nil
}

// ValidateBindingName checks a secret or variable name.
func ValidateBindingName(n string) error {
	if n == "" {
		return invalid("name", "is required")
	}
	if len(n) > 255 || !bindingName.MatchString(n) {
		return invalid("name", "must contain only letters, digits and underscores, and not start with a digit")
	}
	return nil
}

// ValidateSecretValue checks a secret's value without keeping it.
func ValidateSecretValue(v string) error {
	if v == "" {
		return invalid("value", "is required")
	}
	if len(v) > maxSecretLen {
		return invalid("value", "must be at most %d bytes", maxSecretLen)
	}
	return nil
}

// ValidatePagesProjectName checks a new project's name.
func ValidatePagesProjectName(n string) error {
	if n == "" {
		return invalid("name", "is required")
	}
	if len(n) > 58 || !projectName.MatchString(n) {
		return invalid("name", "must be at most 58 characters of lowercase letters, digits and hyphens, starting with a letter or digit")
	}
	return nil
}

// ValidateBranch checks a production branch name.
func ValidateBranch(b string) error {
	if b == "" {
		return invalid("production_branch", "is required")
	}
	if len(b) > 255 || !branchChars.MatchString(b) {
		return invalid("production_branch", "must be a Git branch name")
	}
	return nil
}
