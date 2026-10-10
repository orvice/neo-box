package cloudflare

import (
	"fmt"
	"net/netip"
	"strings"
)

// EditableTypes are the record types Neo Box creates, changes and deletes.
// Records of other types are shown but not changed.
var EditableTypes = []string{"A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "CAA"}

// Editable reports whether Neo Box changes records of type t.
func Editable(t string) bool {
	for _, e := range EditableTypes {
		if e == t {
			return true
		}
	}
	return false
}

// ProxiableType reports whether records of type t can be proxied at all.
// Whether one record can is Cloudflare's call (DNSRecord.Proxiable).
func ProxiableType(t string) bool { return t == "A" || t == "AAAA" || t == "CNAME" }

// TTL bounds: 1 means automatic; Cloudflare allows 60..86400 seconds, or
// from 30 on Enterprise zones, and says which applies when it refuses one.
const (
	TTLAuto = 1
	minTTL  = 30
	maxTTL  = 86400
)

// SRVData is an SRV record's structured content.
type SRVData struct {
	Priority, Weight, Port int
	Target                 string
}

// CAAData is a CAA record's structured content.
type CAAData struct {
	Flags      int
	Tag, Value string
}

// RecordInput is a DNS record as entered in the form. Fields its type does
// not use are ignored.
type RecordInput struct {
	Type    string
	Name    string
	Content string
	TTL     int
	Proxied bool
	// Priority is an MX record's preference.
	Priority int
	SRV      SRVData
	CAA      CAAData
}

// FieldError is invalid input in one field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + " " + e.Message }

func invalid(field, format string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Normalize trims the input and turns TTL 0 into automatic.
func (in RecordInput) Normalize() RecordInput {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.Name = strings.TrimSpace(in.Name)
	in.Content = strings.TrimSpace(in.Content)
	in.SRV.Target = strings.TrimSpace(in.SRV.Target)
	in.CAA.Tag = strings.ToLower(strings.TrimSpace(in.CAA.Tag))
	in.CAA.Value = strings.TrimSpace(in.CAA.Value)
	if in.TTL == 0 {
		in.TTL = TTLAuto
	}
	if !ProxiableType(in.Type) {
		in.Proxied = false
	}
	return in
}

// Validate checks a normalized input. Cloudflare checks more (e.g. names
// that clash) and its refusal is shown as is.
func (in RecordInput) Validate() error {
	if !Editable(in.Type) {
		return invalid("type", "must be one of %s", strings.Join(EditableTypes, ", "))
	}
	if in.Name == "" {
		return invalid("name", "is required")
	}
	if len(in.Name) > 255 || strings.ContainsAny(in.Name, " \t\n") {
		return invalid("name", "must be a DNS name of at most 255 characters without spaces")
	}
	if in.TTL != TTLAuto && (in.TTL < minTTL || in.TTL > maxTTL) {
		return invalid("ttl", "must be 1 (automatic) or between %d and %d seconds", minTTL, maxTTL)
	}
	switch in.Type {
	case "A":
		if a, err := netip.ParseAddr(in.Content); err != nil || !a.Is4() {
			return invalid("content", "must be an IPv4 address")
		}
	case "AAAA":
		if a, err := netip.ParseAddr(in.Content); err != nil || !a.Is6() || a.Is4In6() {
			return invalid("content", "must be an IPv6 address")
		}
	case "CNAME", "NS":
		if !hostname(in.Content) {
			return invalid("content", "must be a host name")
		}
	case "TXT":
		if in.Content == "" {
			return invalid("content", "is required")
		}
	case "MX":
		if !hostname(in.Content) {
			return invalid("content", "must be a mail server host name")
		}
		if !uint16Range(in.Priority) {
			return invalid("priority", "must be between 0 and 65535")
		}
	case "SRV":
		labels := strings.Split(in.Name, ".")
		if len(labels) < 2 || !strings.HasPrefix(labels[0], "_") || !strings.HasPrefix(labels[1], "_") {
			return invalid("name", "must start with _service._protocol, e.g. _sip._tcp")
		}
		if !uint16Range(in.SRV.Priority) || !uint16Range(in.SRV.Weight) || !uint16Range(in.SRV.Port) {
			return invalid("srv", "priority, weight and port must be between 0 and 65535")
		}
		if in.SRV.Target != "." && !hostname(in.SRV.Target) {
			return invalid("srv.target", "must be a host name, or . for no service")
		}
	case "CAA":
		if in.CAA.Flags < 0 || in.CAA.Flags > 255 {
			return invalid("caa.flags", "must be between 0 and 255")
		}
		switch in.CAA.Tag {
		case "issue", "issuewild", "iodef":
		default:
			return invalid("caa.tag", "must be issue, issuewild or iodef")
		}
		if in.CAA.Value == "" {
			return invalid("caa.value", "is required")
		}
	}
	return nil
}

func uint16Range(n int) bool { return n >= 0 && n <= 65535 }

// hostname is a light check: Cloudflare validates the details.
func hostname(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t\n/:@") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

// valueFields are the fields the form shows for in's type, besides name and
// TTL.
func (in RecordInput) valueFields(body map[string]any) {
	switch in.Type {
	case "SRV":
		body["data"] = map[string]any{
			"priority": in.SRV.Priority, "weight": in.SRV.Weight, "port": in.SRV.Port, "target": in.SRV.Target,
		}
	case "CAA":
		body["data"] = map[string]any{"flags": in.CAA.Flags, "tag": in.CAA.Tag, "value": in.CAA.Value}
	case "MX":
		body["content"] = in.Content
		body["priority"] = in.Priority
	default:
		body["content"] = in.Content
	}
}

// CreateBody is the request body that creates in.
func (in RecordInput) CreateBody() map[string]any {
	body := map[string]any{"type": in.Type, "name": in.Name, "ttl": in.TTL}
	in.valueFields(body)
	if ProxiableType(in.Type) {
		body["proxied"] = in.Proxied
	}
	return body
}

// EditBody is the partial-update body for in. It holds only what the form
// shows, so the record keeps its comment, tags, settings and anything else;
// the proxy status is included only when the record is proxiable.
func (in RecordInput) EditBody(proxiable bool) map[string]any {
	body := map[string]any{"name": in.Name, "ttl": in.TTL}
	in.valueFields(body)
	if proxiable {
		body["proxied"] = in.Proxied
	}
	return body
}

// ProxiedBody is the partial-update body that changes only the proxy
// status.
func ProxiedBody(proxied bool) map[string]any { return map[string]any{"proxied": proxied} }
