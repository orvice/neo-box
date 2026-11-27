package porkbun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Domain is one domain in a Porkbun account.
type Domain struct {
	Domain       string        `json:"domain"`
	Status       string        `json:"status"`
	TLD          string        `json:"tld"`
	CreatedAt    time.Time     `json:"created_at"`
	ExpiresAt    time.Time     `json:"expires_at"`
	SecurityLock bool          `json:"security_lock"`
	WhoisPrivacy bool          `json:"whois_privacy"`
	AutoRenew    bool          `json:"auto_renew"`
	NotLocal     bool          `json:"not_local"`
	APIAccess    bool          `json:"api_access"`
	Labels       []DomainLabel `json:"labels"`
}

// DomainLabel is a tag assigned to a domain in Porkbun.
type DomainLabel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color"`
}

// DNSRecord is one DNS record read from Porkbun.
type DNSRecord struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority string `json:"priority"`
	Notes    string `json:"notes"`
}

// URLForward is one URL forwarding rule configured on a domain.
type URLForward struct {
	ID          string `json:"id"`
	Subdomain   string `json:"subdomain"`
	Location    string `json:"location"`
	Type        string `json:"type"`
	IncludePath bool   `json:"include_path"`
	Wildcard    bool   `json:"wildcard"`
}

// DNSSECRecord is one DS record configured for a domain.
type DNSSECRecord struct {
	KeyTag     string `json:"key_tag"`
	Algorithm  string `json:"algorithm"`
	DigestType string `json:"digest_type"`
	Digest     string `json:"digest"`
}

// GlueHost is one custom nameserver glue record registered for a domain.
type GlueHost struct {
	Host string   `json:"host"`
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
}

// PingResult is the response of POST /ping.
type PingResult struct {
	YourIP  string
	Sandbox bool
}

// NormalizeDomain trims whitespace and trailing dots, lowercases the domain,
// and rejects malformed path segments.
func NormalizeDomain(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if d == "" {
		return "", fmt.Errorf("porkbun: domain is required")
	}
	if strings.ContainsAny(d, " \t\r\n/\\?#") || strings.Contains(d, "..") || !strings.Contains(d, ".") {
		return "", fmt.Errorf("porkbun: invalid domain name %q", raw)
	}
	return d, nil
}

// UsesPorkbunNameservers reports whether all reported nameservers belong to
// Porkbun (*.porkbun.com). An empty slice returns false.
func UsesPorkbunNameservers(ns []string) bool {
	if len(ns) == 0 {
		return false
	}
	for _, raw := range ns {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if host == "" {
			return false
		}
		if host != "porkbun.com" && !strings.HasSuffix(host, ".porkbun.com") {
			return false
		}
	}
	return true
}

// parsePorkbunTime parses Porkbun's UTC timestamps ("2006-01-02 15:04:05" or
// RFC3339). Empty or zero timestamps return the zero time.
func parsePorkbunTime(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" || s == "0000-00-00 00:00:00" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// --- defensive JSON decoders ---
// Porkbun v3 historically mixed string ("1", "600") and numeric (1, 600) JSON
// values for flags and integers before standardizing in v3.31. These types
// accept both shapes without failing.

type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*b = false
		return nil
	}
	var rawBool bool
	if err := json.Unmarshal(trimmed, &rawBool); err == nil {
		*b = flexBool(rawBool)
		return nil
	}
	var rawNum int64
	if err := json.Unmarshal(trimmed, &rawNum); err == nil {
		*b = flexBool(rawNum != 0)
		return nil
	}
	var rawStr string
	if err := json.Unmarshal(trimmed, &rawStr); err == nil {
		switch strings.ToLower(strings.TrimSpace(rawStr)) {
		case "1", "true", "yes", "on":
			*b = true
		default:
			*b = false
		}
		return nil
	}
	return fmt.Errorf("cannot decode %s as bool", string(trimmed))
}

type flexInt int

func (n *flexInt) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*n = 0
		return nil
	}
	var rawNum int
	if err := json.Unmarshal(trimmed, &rawNum); err == nil {
		*n = flexInt(rawNum)
		return nil
	}
	var rawStr string
	if err := json.Unmarshal(trimmed, &rawStr); err == nil {
		s := strings.TrimSpace(rawStr)
		if s == "" {
			*n = 0
			return nil
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("cannot decode %q as int: %w", rawStr, err)
		}
		*n = flexInt(v)
		return nil
	}
	return fmt.Errorf("cannot decode %s as int", string(trimmed))
}

type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}
	var rawStr string
	if err := json.Unmarshal(trimmed, &rawStr); err == nil {
		*s = flexString(rawStr)
		return nil
	}
	var rawNum json.Number
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&rawNum); err == nil {
		*s = flexString(rawNum.String())
		return nil
	}
	return fmt.Errorf("cannot decode %s as string", string(trimmed))
}

// splitIPs separates a mixed slice of IPv4 and IPv6 addresses.
func splitIPs(ips []string) (v4, v6 []string) {
	for _, raw := range ips {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		addr, err := netip.ParseAddr(ip)
		if err == nil && addr.Is6() {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}
	return v4, v6
}
