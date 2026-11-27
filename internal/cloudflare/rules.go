package cloudflare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ZoneRuleCategory is one of the five Cloudflare Zone Ruleset phases managed
// by Neo Box. Each phase is evaluated and configured independently.
type ZoneRuleCategory string

const (
	ZoneRuleSingleRedirect ZoneRuleCategory = "http_request_dynamic_redirect"
	ZoneRuleURLRewrite     ZoneRuleCategory = "http_request_transform"
	ZoneRuleRequestHeader  ZoneRuleCategory = "http_request_late_transform"
	ZoneRuleResponseHeader ZoneRuleCategory = "http_response_headers_transform"
	ZoneRuleOrigin         ZoneRuleCategory = "http_request_origin"
)

// AbsentRulesetSnapshot is the concurrency snapshot token representing a phase
// that currently has no entrypoint ruleset at Cloudflare.
const AbsentRulesetSnapshot = "absent"

var zoneRuleCategories = []ZoneRuleCategory{
	ZoneRuleSingleRedirect,
	ZoneRuleURLRewrite,
	ZoneRuleRequestHeader,
	ZoneRuleResponseHeader,
	ZoneRuleOrigin,
}

// AllZoneRuleCategories returns the five supported Zone Rules categories in
// presentation order.
func AllZoneRuleCategories() []ZoneRuleCategory {
	out := make([]ZoneRuleCategory, len(zoneRuleCategories))
	copy(out, zoneRuleCategories)
	return out
}

func (c ZoneRuleCategory) Valid() bool {
	for _, v := range zoneRuleCategories {
		if c == v {
			return true
		}
	}
	return false
}

// Label is the Cloudflare product name for the category.
func (c ZoneRuleCategory) Label() string {
	switch c {
	case ZoneRuleSingleRedirect:
		return "Single Redirects"
	case ZoneRuleURLRewrite:
		return "URL Rewrite Rules"
	case ZoneRuleRequestHeader:
		return "Request Header Transform Rules"
	case ZoneRuleResponseHeader:
		return "Response Header Transform Rules"
	case ZoneRuleOrigin:
		return "Origin Rules"
	}
	return string(c)
}

// ExpectedAction is the Cloudflare Ruleset action name used by this phase.
func (c ZoneRuleCategory) ExpectedAction() string {
	switch c {
	case ZoneRuleSingleRedirect:
		return "redirect"
	case ZoneRuleURLRewrite, ZoneRuleRequestHeader, ZoneRuleResponseHeader:
		return "rewrite"
	case ZoneRuleOrigin:
		return "route"
	}
	return ""
}

// ReadPermission names the Cloudflare API token permission needed to read
// this category's ruleset.
func (c ZoneRuleCategory) ReadPermission() string {
	switch c {
	case ZoneRuleSingleRedirect:
		return "Dynamic URL Redirects Read"
	case ZoneRuleURLRewrite, ZoneRuleRequestHeader, ZoneRuleResponseHeader:
		return "Zone Transform Rules Read"
	case ZoneRuleOrigin:
		return "Origin Read"
	}
	return "Zone Rules Read"
}

// WritePermission names the Cloudflare API token permission needed to modify
// this category's ruleset.
func (c ZoneRuleCategory) WritePermission() string {
	switch c {
	case ZoneRuleSingleRedirect:
		return "Dynamic URL Redirects Write"
	case ZoneRuleURLRewrite, ZoneRuleRequestHeader, ZoneRuleResponseHeader:
		return "Zone Transform Rules Write"
	case ZoneRuleOrigin:
		return "Origin Write"
	}
	return "Zone Rules Write"
}

// --- raw ruleset and rule models ---

// ZoneRule is a Cloudflare ruleset rule. ActionParameters and Unknown preserve
// unfamiliar configuration across read-modify-write operations so Neo Box never
// discards fields it does not understand.
type ZoneRule struct {
	ID               string          `json:"id,omitempty"`
	Version          string          `json:"version,omitempty"`
	Ref              string          `json:"ref,omitempty"`
	Description      string          `json:"description,omitempty"`
	Expression       string          `json:"expression"`
	Action           string          `json:"action"`
	ActionParameters json.RawMessage `json:"action_parameters,omitempty"`
	Enabled          *bool           `json:"enabled,omitempty"`
	LastUpdated      string          `json:"last_updated,omitempty"`
	// Unknown preserves extra fields returned by Cloudflare. Harmless response
	// metadata is distinguished from unsupported writable fields when checking
	// read-only status, and all keys are merged back on MarshalJSON.
	Unknown map[string]json.RawMessage `json:"-"`
}

// IsEnabled reports whether the rule is enabled (Cloudflare defaults omitted
// enabled to true).
func (r ZoneRule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// RawConfigJSON returns pretty-printed JSON of the rule for display-only
// inspection when the rule is read-only.
func (r ZoneRule) RawConfigJSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return string(b)
	}
	return buf.String()
}

func (r *ZoneRule) UnmarshalJSON(data []byte) error {
	type alias ZoneRule
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = ZoneRule(a)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, k := range []string{"id", "version", "ref", "description", "expression", "action", "action_parameters", "enabled", "last_updated"} {
		delete(raw, k)
	}
	if len(raw) > 0 {
		r.Unknown = raw
	} else {
		r.Unknown = nil
	}
	return nil
}

func (r ZoneRule) MarshalJSON() ([]byte, error) {
	type alias ZoneRule
	b, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	for k, v := range r.Unknown {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return json.Marshal(out)
}

// ZoneRuleset is the complete live state of one phase entrypoint ruleset.
// Rules are ordered as Cloudflare evaluates them.
type ZoneRuleset struct {
	ID          string                     `json:"id,omitempty"`
	Name        string                     `json:"name,omitempty"`
	Description string                     `json:"description,omitempty"`
	Kind        string                     `json:"kind,omitempty"`
	Phase       ZoneRuleCategory           `json:"phase"`
	Version     string                     `json:"version,omitempty"`
	LastUpdated string                     `json:"last_updated,omitempty"`
	ETag        string                     `json:"etag,omitempty"`
	Rules       []ZoneRule                 `json:"rules"`
	Unknown     map[string]json.RawMessage `json:"-"`
}

func (r *ZoneRuleset) UnmarshalJSON(data []byte) error {
	type alias ZoneRuleset
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*r = ZoneRuleset(a)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, k := range []string{"id", "name", "description", "kind", "phase", "version", "last_updated", "etag", "rules"} {
		delete(raw, k)
	}
	if len(raw) > 0 {
		r.Unknown = raw
	} else {
		r.Unknown = nil
	}
	return nil
}

func (r ZoneRuleset) MarshalJSON() ([]byte, error) {
	type alias ZoneRuleset
	b, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	for k, v := range r.Unknown {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return json.Marshal(out)
}

// Snapshot hashes the live ruleset state (ID, version and complete ordered
// rules) for optimistic concurrency checks before mutations.
func (r ZoneRuleset) Snapshot() string {
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (r ZoneRuleset) Rule(id string) *ZoneRule {
	for i := range r.Rules {
		if r.Rules[i].ID == id {
			return &r.Rules[i]
		}
	}
	return nil
}

func (r ZoneRuleset) Empty() bool { return len(r.Rules) == 0 }

// --- typed v1 action configurations ---

const (
	RedirectTargetStatic     = "static"
	RedirectTargetExpression = "expression"

	RewritePartKeep       = "keep"
	RewritePartStatic     = "static"
	RewritePartExpression = "expression"

	HeaderOpSetStatic     = "set_static"
	HeaderOpSetExpression = "set_expression"
	HeaderOpAddStatic     = "add_static"
	HeaderOpAddExpression = "add_expression"
	HeaderOpRemove        = "remove"
)

type SingleRedirectConfig struct {
	TargetKind          string
	TargetValue         string
	StatusCode          int
	PreserveQueryString bool
}

type URLRewriteConfig struct {
	PathMode   string
	PathValue  string
	QueryMode  string
	QueryValue string
}

type HeaderModification struct {
	Name      string
	Operation string
	Value     string
}

type HeaderTransformConfig struct {
	Headers []HeaderModification
}

type OriginRuleConfig struct {
	DestinationPort *int
	HostHeader      *string
	SNI             *string
	DNSRecord       *string
}

// ZoneRuleConfig holds the typed v1 action configuration for one category.
type ZoneRuleConfig struct {
	SingleRedirect *SingleRedirectConfig
	URLRewrite     *URLRewriteConfig
	RequestHeader  *HeaderTransformConfig
	ResponseHeader *HeaderTransformConfig
	Origin         *OriginRuleConfig
}

// ZoneRuleInput is the validated typed input for creating or updating a rule.
type ZoneRuleInput struct {
	Description string
	Expression  string
	Enabled     bool
	Config      ZoneRuleConfig
}

// Response metadata keys Cloudflare may attach to rule objects that do not
// affect rule behavior and must not make ordinary rules read-only.
var ruleResponseMetadataKeys = map[string]bool{
	"categories":  true,
	"created_on":  true,
	"modified_on": true,
}

func unknownKeys(m map[string]json.RawMessage, allowed map[string]bool) []string {
	var extra []string
	for k := range m {
		if !allowed[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return extra
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty JSON object")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("expected JSON object")
	}
	return m, nil
}

func decodeString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

func decodeBool(raw json.RawMessage) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, err
	}
	return b, nil
}

func decodeInt(raw json.RawMessage) (int, error) {
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return n, nil
}

// ParseZoneRuleConfig parses a Cloudflare rule's action and action_parameters
// into the typed v1 model for category. If the rule contains any unsupported
// writable fields, action, or parameters, it returns a non-empty read-only
// reason and nil config.
func ParseZoneRuleConfig(category ZoneRuleCategory, r ZoneRule) (*ZoneRuleConfig, string) {
	if extra := unknownKeys(r.Unknown, ruleResponseMetadataKeys); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported rule field(s): %s", strings.Join(extra, ", "))
	}
	expectedAction := category.ExpectedAction()
	if expectedAction == "" {
		return nil, fmt.Sprintf("Unsupported Zone Rules category %q", category)
	}
	if r.Action != expectedAction {
		return nil, fmt.Sprintf("Uses action %q, which is not editable in %s", r.Action, category.Label())
	}
	params, err := decodeObject(r.ActionParameters)
	if err != nil {
		return nil, "Action parameters are missing or not a JSON object"
	}

	switch category {
	case ZoneRuleSingleRedirect:
		cfg, reason := parseSingleRedirect(params)
		if reason != "" {
			return nil, reason
		}
		return &ZoneRuleConfig{SingleRedirect: cfg}, ""
	case ZoneRuleURLRewrite:
		cfg, reason := parseURLRewrite(params)
		if reason != "" {
			return nil, reason
		}
		return &ZoneRuleConfig{URLRewrite: cfg}, ""
	case ZoneRuleRequestHeader:
		cfg, reason := parseHeaderTransform(params, false)
		if reason != "" {
			return nil, reason
		}
		return &ZoneRuleConfig{RequestHeader: cfg}, ""
	case ZoneRuleResponseHeader:
		cfg, reason := parseHeaderTransform(params, true)
		if reason != "" {
			return nil, reason
		}
		return &ZoneRuleConfig{ResponseHeader: cfg}, ""
	case ZoneRuleOrigin:
		cfg, reason := parseOriginRule(params)
		if reason != "" {
			return nil, reason
		}
		return &ZoneRuleConfig{Origin: cfg}, ""
	}
	return nil, "Unsupported Zone Rules category"
}

func parseSingleRedirect(params map[string]json.RawMessage) (*SingleRedirectConfig, string) {
	if extra := unknownKeys(params, map[string]bool{"from_value": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported Single Redirect parameter(s): %s", strings.Join(extra, ", "))
	}
	fvRaw, ok := params["from_value"]
	if !ok {
		return nil, "Missing from_value redirect configuration"
	}
	fv, err := decodeObject(fvRaw)
	if err != nil {
		return nil, "Invalid from_value redirect configuration"
	}
	if extra := unknownKeys(fv, map[string]bool{"target_url": true, "status_code": true, "preserve_query_string": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported redirect field(s): %s", strings.Join(extra, ", "))
	}
	tuRaw, ok := fv["target_url"]
	if !ok {
		return nil, "Missing target_url configuration"
	}
	tu, err := decodeObject(tuRaw)
	if err != nil {
		return nil, "Invalid target_url configuration"
	}
	if extra := unknownKeys(tu, map[string]bool{"value": true, "expression": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported target_url field(s): %s", strings.Join(extra, ", "))
	}
	valRaw, hasVal := tu["value"]
	exprRaw, hasExpr := tu["expression"]
	if hasVal == hasExpr {
		return nil, "Redirect target_url must specify either a static value or an expression"
	}
	cfg := &SingleRedirectConfig{StatusCode: 301}
	if hasVal {
		s, err := decodeString(valRaw)
		if err != nil || s == "" {
			return nil, "Invalid static redirect target_url value"
		}
		cfg.TargetKind, cfg.TargetValue = RedirectTargetStatic, s
	} else {
		s, err := decodeString(exprRaw)
		if err != nil || s == "" {
			return nil, "Invalid dynamic redirect target_url expression"
		}
		cfg.TargetKind, cfg.TargetValue = RedirectTargetExpression, s
	}
	if scRaw, ok := fv["status_code"]; ok {
		sc, err := decodeInt(scRaw)
		if err != nil || !validRedirectStatus(sc) {
			return nil, fmt.Sprintf("Unsupported redirect status code (%s)", strings.TrimSpace(string(scRaw)))
		}
		cfg.StatusCode = sc
	}
	if pqsRaw, ok := fv["preserve_query_string"]; ok {
		pqs, err := decodeBool(pqsRaw)
		if err != nil {
			return nil, "Invalid preserve_query_string value"
		}
		cfg.PreserveQueryString = pqs
	}
	return cfg, ""
}

func parseURLRewrite(params map[string]json.RawMessage) (*URLRewriteConfig, string) {
	if extra := unknownKeys(params, map[string]bool{"uri": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported URL Rewrite parameter(s): %s", strings.Join(extra, ", "))
	}
	uriRaw, ok := params["uri"]
	if !ok {
		return nil, "Missing uri rewrite configuration"
	}
	uriObj, err := decodeObject(uriRaw)
	if err != nil {
		return nil, "Invalid uri rewrite configuration"
	}
	if extra := unknownKeys(uriObj, map[string]bool{"path": true, "query": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported uri field(s): %s", strings.Join(extra, ", "))
	}
	cfg := &URLRewriteConfig{PathMode: RewritePartKeep, QueryMode: RewritePartKeep}
	if pRaw, ok := uriObj["path"]; ok {
		mode, val, reason := parseRewritePart("path", pRaw, false)
		if reason != "" {
			return nil, reason
		}
		cfg.PathMode, cfg.PathValue = mode, val
	}
	if qRaw, ok := uriObj["query"]; ok {
		mode, val, reason := parseRewritePart("query", qRaw, true)
		if reason != "" {
			return nil, reason
		}
		cfg.QueryMode, cfg.QueryValue = mode, val
	}
	if cfg.PathMode == RewritePartKeep && cfg.QueryMode == RewritePartKeep {
		return nil, "URL Rewrite rule modifies neither path nor query"
	}
	return cfg, ""
}

func parseRewritePart(name string, raw json.RawMessage, allowEmptyStatic bool) (string, string, string) {
	obj, err := decodeObject(raw)
	if err != nil {
		return "", "", fmt.Sprintf("Invalid uri.%s configuration", name)
	}
	if extra := unknownKeys(obj, map[string]bool{"value": true, "expression": true}); len(extra) > 0 {
		return "", "", fmt.Sprintf("Contains unsupported uri.%s field(s): %s", name, strings.Join(extra, ", "))
	}
	valRaw, hasVal := obj["value"]
	exprRaw, hasExpr := obj["expression"]
	if hasVal == hasExpr {
		return "", "", fmt.Sprintf("uri.%s must specify either value or expression", name)
	}
	if hasVal {
		s, err := decodeString(valRaw)
		if err != nil || (!allowEmptyStatic && s == "") {
			return "", "", fmt.Sprintf("Invalid static uri.%s value", name)
		}
		return RewritePartStatic, s, ""
	}
	s, err := decodeString(exprRaw)
	if err != nil || s == "" {
		return "", "", fmt.Sprintf("Invalid dynamic uri.%s expression", name)
	}
	return RewritePartExpression, s, ""
}

func parseHeaderTransform(params map[string]json.RawMessage, allowAdd bool) (*HeaderTransformConfig, string) {
	if extra := unknownKeys(params, map[string]bool{"headers": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported Header Transform parameter(s): %s", strings.Join(extra, ", "))
	}
	hdrsRaw, ok := params["headers"]
	if !ok {
		return nil, "Missing headers transform configuration"
	}
	hdrsObj, err := decodeObject(hdrsRaw)
	if err != nil || len(hdrsObj) == 0 {
		return nil, "Invalid or empty headers transform configuration"
	}
	names := make([]string, 0, len(hdrsObj))
	for name := range hdrsObj {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
	mods := make([]HeaderModification, 0, len(names))
	for _, name := range names {
		hObj, err := decodeObject(hdrsObj[name])
		if err != nil {
			return nil, fmt.Sprintf("Invalid configuration for header %q", name)
		}
		if extra := unknownKeys(hObj, map[string]bool{"operation": true, "value": true, "expression": true}); len(extra) > 0 {
			return nil, fmt.Sprintf("Header %q contains unsupported field(s): %s", name, strings.Join(extra, ", "))
		}
		opRaw, ok := hObj["operation"]
		if !ok {
			return nil, fmt.Sprintf("Header %q is missing operation", name)
		}
		op, err := decodeString(opRaw)
		if err != nil {
			return nil, fmt.Sprintf("Header %q has invalid operation", name)
		}
		valRaw, hasVal := hObj["value"]
		exprRaw, hasExpr := hObj["expression"]
		switch op {
		case "set":
			if hasVal == hasExpr {
				return nil, fmt.Sprintf("Header %q set operation must specify either value or expression", name)
			}
			if hasVal {
				s, err := decodeString(valRaw)
				if err != nil {
					return nil, fmt.Sprintf("Header %q has invalid static value", name)
				}
				mods = append(mods, HeaderModification{Name: name, Operation: HeaderOpSetStatic, Value: s})
			} else {
				s, err := decodeString(exprRaw)
				if err != nil || s == "" {
					return nil, fmt.Sprintf("Header %q has invalid expression", name)
				}
				mods = append(mods, HeaderModification{Name: name, Operation: HeaderOpSetExpression, Value: s})
			}
		case "add":
			if !allowAdd {
				return nil, fmt.Sprintf("Header %q uses operation %q, which is not supported for request headers", name, op)
			}
			if hasVal == hasExpr {
				return nil, fmt.Sprintf("Header %q add operation must specify either value or expression", name)
			}
			if hasVal {
				s, err := decodeString(valRaw)
				if err != nil {
					return nil, fmt.Sprintf("Header %q has invalid static value", name)
				}
				mods = append(mods, HeaderModification{Name: name, Operation: HeaderOpAddStatic, Value: s})
			} else {
				s, err := decodeString(exprRaw)
				if err != nil || s == "" {
					return nil, fmt.Sprintf("Header %q has invalid expression", name)
				}
				mods = append(mods, HeaderModification{Name: name, Operation: HeaderOpAddExpression, Value: s})
			}
		case "remove":
			if hasVal || hasExpr {
				return nil, fmt.Sprintf("Header %q remove operation contains unexpected value or expression", name)
			}
			mods = append(mods, HeaderModification{Name: name, Operation: HeaderOpRemove})
		default:
			return nil, fmt.Sprintf("Header %q uses unsupported operation %q", name, op)
		}
	}
	return &HeaderTransformConfig{Headers: mods}, ""
}

func parseOriginRule(params map[string]json.RawMessage) (*OriginRuleConfig, string) {
	if extra := unknownKeys(params, map[string]bool{"host_header": true, "sni": true, "origin": true}); len(extra) > 0 {
		return nil, fmt.Sprintf("Contains unsupported Origin Rule parameter(s): %s", strings.Join(extra, ", "))
	}
	cfg := &OriginRuleConfig{}
	if hhRaw, ok := params["host_header"]; ok {
		s, err := decodeString(hhRaw)
		if err != nil || strings.TrimSpace(s) == "" {
			return nil, "Invalid host_header override"
		}
		cfg.HostHeader = &s
	}
	if sniRaw, ok := params["sni"]; ok {
		sniObj, err := decodeObject(sniRaw)
		if err != nil {
			return nil, "Invalid sni override configuration"
		}
		if extra := unknownKeys(sniObj, map[string]bool{"value": true}); len(extra) > 0 {
			return nil, fmt.Sprintf("Contains unsupported sni field(s): %s", strings.Join(extra, ", "))
		}
		vRaw, ok := sniObj["value"]
		if !ok {
			return nil, "Missing sni.value"
		}
		s, err := decodeString(vRaw)
		if err != nil || strings.TrimSpace(s) == "" {
			return nil, "Invalid sni.value"
		}
		cfg.SNI = &s
	}
	if orgRaw, ok := params["origin"]; ok {
		orgObj, err := decodeObject(orgRaw)
		if err != nil {
			return nil, "Invalid origin override configuration"
		}
		if extra := unknownKeys(orgObj, map[string]bool{"host": true, "port": true}); len(extra) > 0 {
			return nil, fmt.Sprintf("Contains unsupported origin field(s): %s", strings.Join(extra, ", "))
		}
		if len(orgObj) == 0 {
			return nil, "Empty origin override object"
		}
		if hRaw, ok := orgObj["host"]; ok {
			s, err := decodeString(hRaw)
			if err != nil || strings.TrimSpace(s) == "" {
				return nil, "Invalid origin.host override"
			}
			cfg.DNSRecord = &s
		}
		if pRaw, ok := orgObj["port"]; ok {
			p, err := decodeInt(pRaw)
			if err != nil || p < 1 || p > 65535 {
				return nil, "Invalid origin.port override"
			}
			cfg.DestinationPort = &p
		}
	}
	if cfg.HostHeader == nil && cfg.SNI == nil && cfg.DNSRecord == nil && cfg.DestinationPort == nil {
		return nil, "Origin rule specifies no supported overrides"
	}
	return cfg, ""
}

// --- input validation and rule building ---

var headerNamePattern = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

func validRedirectStatus(code int) bool {
	switch code {
	case 301, 302, 307, 308:
		return true
	}
	return false
}

// ValidateZoneRuleInput checks structural constraints on a rule input before
// sending it to Cloudflare. Header values are never echoed in error messages.
func ValidateZoneRuleInput(category ZoneRuleCategory, in ZoneRuleInput) error {
	if !category.Valid() {
		return invalid("category", "must be a supported Zone Rules category")
	}
	if len(strings.TrimSpace(in.Description)) > 512 {
		return invalid("description", "must be at most 512 characters")
	}
	expr := strings.TrimSpace(in.Expression)
	if expr == "" {
		return invalid("expression", "is required; choose a match condition or explicitly match all requests")
	}
	if len(expr) > 4096 {
		return invalid("expression", "must be at most 4096 characters")
	}

	switch category {
	case ZoneRuleSingleRedirect:
		cfg := in.Config.SingleRedirect
		if cfg == nil {
			return invalid("single_redirect", "configuration is required for Single Redirects")
		}
		if cfg.TargetKind != RedirectTargetStatic && cfg.TargetKind != RedirectTargetExpression {
			return invalid("target_kind", "must be static or expression")
		}
		target := strings.TrimSpace(cfg.TargetValue)
		if target == "" {
			return invalid("target_value", "is required")
		}
		if cfg.TargetKind == RedirectTargetStatic && strings.ContainsAny(target, "\r\n \t") {
			return invalid("target_value", "must not contain whitespace")
		}
		if !validRedirectStatus(cfg.StatusCode) {
			return invalid("status_code", "must be 301, 302, 307 or 308")
		}
	case ZoneRuleURLRewrite:
		cfg := in.Config.URLRewrite
		if cfg == nil {
			return invalid("url_rewrite", "configuration is required for URL Rewrite Rules")
		}
		if err := validateRewritePart("path", cfg.PathMode, cfg.PathValue, false); err != nil {
			return err
		}
		if err := validateRewritePart("query", cfg.QueryMode, cfg.QueryValue, true); err != nil {
			return err
		}
		if cfg.PathMode == RewritePartKeep && cfg.QueryMode == RewritePartKeep {
			return invalid("url_rewrite", "rewrite at least the path or the query string")
		}
	case ZoneRuleRequestHeader:
		return validateHeaderTransform("request_header", in.Config.RequestHeader, false)
	case ZoneRuleResponseHeader:
		return validateHeaderTransform("response_header", in.Config.ResponseHeader, true)
	case ZoneRuleOrigin:
		cfg := in.Config.Origin
		if cfg == nil {
			return invalid("origin", "configuration is required for Origin Rules")
		}
		if cfg.DestinationPort == nil && cfg.HostHeader == nil && cfg.SNI == nil && cfg.DNSRecord == nil {
			return invalid("origin", "set at least one override (port, Host header, SNI or DNS target)")
		}
		if cfg.DestinationPort != nil && (*cfg.DestinationPort < 1 || *cfg.DestinationPort > 65535) {
			return invalid("destination_port", "must be between 1 and 65535")
		}
		if cfg.HostHeader != nil {
			if err := validateHostOverride("host_header", *cfg.HostHeader); err != nil {
				return err
			}
		}
		if cfg.SNI != nil {
			if err := validateHostOverride("sni", *cfg.SNI); err != nil {
				return err
			}
		}
		if cfg.DNSRecord != nil {
			if err := validateHostOverride("dns_record", *cfg.DNSRecord); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRewritePart(name, mode, val string, allowEmptyStatic bool) error {
	switch mode {
	case "", RewritePartKeep:
		return nil
	case RewritePartStatic:
		if !allowEmptyStatic && strings.TrimSpace(val) == "" {
			return invalid(name+"_value", "is required for static rewrite")
		}
		if strings.ContainsAny(val, "\r\n") {
			return invalid(name+"_value", "must not contain line breaks")
		}
		return nil
	case RewritePartExpression:
		if strings.TrimSpace(val) == "" {
			return invalid(name+"_value", "is required for dynamic rewrite")
		}
		return nil
	default:
		return invalid(name+"_mode", "must be keep, static or expression")
	}
}

func validateHeaderTransform(field string, cfg *HeaderTransformConfig, allowAdd bool) error {
	if cfg == nil || len(cfg.Headers) == 0 {
		return invalid(field, "at least one header modification is required")
	}
	seen := make(map[string]bool, len(cfg.Headers))
	for i, h := range cfg.Headers {
		name := strings.TrimSpace(h.Name)
		if name == "" || len(name) > 256 || !headerNamePattern.MatchString(name) {
			return invalid("headers", "header #%d name must be a valid HTTP header name", i+1)
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return invalid("headers", "duplicate header name %q", name)
		}
		seen[lower] = true
		switch h.Operation {
		case HeaderOpSetStatic:
			if h.Value == "" {
				return invalid("headers", "header %q requires a static value", name)
			}
			if strings.ContainsAny(h.Value, "\r\n") {
				return invalid("headers", "header %q value must not contain line breaks", name)
			}
		case HeaderOpSetExpression:
			if strings.TrimSpace(h.Value) == "" {
				return invalid("headers", "header %q requires an expression", name)
			}
		case HeaderOpAddStatic:
			if !allowAdd {
				return invalid("headers", "header %q cannot use add in Request Header Transform Rules", name)
			}
			if h.Value == "" {
				return invalid("headers", "header %q requires a static value", name)
			}
			if strings.ContainsAny(h.Value, "\r\n") {
				return invalid("headers", "header %q value must not contain line breaks", name)
			}
		case HeaderOpAddExpression:
			if !allowAdd {
				return invalid("headers", "header %q cannot use add in Request Header Transform Rules", name)
			}
			if strings.TrimSpace(h.Value) == "" {
				return invalid("headers", "header %q requires an expression", name)
			}
		case HeaderOpRemove:
			// No value needed.
		default:
			return invalid("headers", "header %q has an unsupported operation", name)
		}
	}
	return nil
}

func validateHostOverride(field, val string) error {
	v := strings.TrimSpace(val)
	if v == "" {
		return invalid(field, "must not be empty")
	}
	if len(v) > 255 || strings.ContainsAny(v, " \t\r\n/") || strings.Contains(v, "://") {
		return invalid(field, "must be a hostname without scheme, path or whitespace")
	}
	return nil
}

// BuildZoneRule turns a validated ZoneRuleInput into a Cloudflare ZoneRule.
// When base is non-nil (editing an existing rule), identity and response
// metadata are preserved.
func BuildZoneRule(category ZoneRuleCategory, in ZoneRuleInput, base *ZoneRule) (ZoneRule, error) {
	if err := ValidateZoneRuleInput(category, in); err != nil {
		return ZoneRule{}, err
	}
	var params any
	switch category {
	case ZoneRuleSingleRedirect:
		cfg := in.Config.SingleRedirect
		tu := map[string]string{}
		if cfg.TargetKind == RedirectTargetStatic {
			tu["value"] = strings.TrimSpace(cfg.TargetValue)
		} else {
			tu["expression"] = strings.TrimSpace(cfg.TargetValue)
		}
		fv := map[string]any{
			"target_url":            tu,
			"status_code":           cfg.StatusCode,
			"preserve_query_string": cfg.PreserveQueryString,
		}
		params = map[string]any{"from_value": fv}
	case ZoneRuleURLRewrite:
		cfg := in.Config.URLRewrite
		uri := map[string]any{}
		if cfg.PathMode == RewritePartStatic {
			uri["path"] = map[string]string{"value": cfg.PathValue}
		} else if cfg.PathMode == RewritePartExpression {
			uri["path"] = map[string]string{"expression": strings.TrimSpace(cfg.PathValue)}
		}
		if cfg.QueryMode == RewritePartStatic {
			uri["query"] = map[string]string{"value": cfg.QueryValue}
		} else if cfg.QueryMode == RewritePartExpression {
			uri["query"] = map[string]string{"expression": strings.TrimSpace(cfg.QueryValue)}
		}
		params = map[string]any{"uri": uri}
	case ZoneRuleRequestHeader:
		params = map[string]any{"headers": buildHeadersMap(in.Config.RequestHeader.Headers)}
	case ZoneRuleResponseHeader:
		params = map[string]any{"headers": buildHeadersMap(in.Config.ResponseHeader.Headers)}
	case ZoneRuleOrigin:
		cfg := in.Config.Origin
		p := map[string]any{}
		if cfg.HostHeader != nil {
			p["host_header"] = strings.TrimSpace(*cfg.HostHeader)
		}
		if cfg.SNI != nil {
			p["sni"] = map[string]string{"value": strings.TrimSpace(*cfg.SNI)}
		}
		if cfg.DNSRecord != nil || cfg.DestinationPort != nil {
			o := map[string]any{}
			if cfg.DNSRecord != nil {
				o["host"] = strings.TrimSpace(*cfg.DNSRecord)
			}
			if cfg.DestinationPort != nil {
				o["port"] = *cfg.DestinationPort
			}
			p["origin"] = o
		}
		params = p
	}

	rawParams, err := json.Marshal(params)
	if err != nil {
		return ZoneRule{}, fmt.Errorf("cloudflare: encode action_parameters: %w", err)
	}
	enabled := in.Enabled
	rule := ZoneRule{
		Description:      strings.TrimSpace(in.Description),
		Expression:       strings.TrimSpace(in.Expression),
		Action:           category.ExpectedAction(),
		ActionParameters: rawParams,
		Enabled:          &enabled,
	}
	if base != nil {
		rule.ID = base.ID
		rule.Ref = base.Ref
		rule.Version = base.Version
		if len(base.Unknown) > 0 {
			rule.Unknown = make(map[string]json.RawMessage, len(base.Unknown))
			for k, v := range base.Unknown {
				rule.Unknown[k] = v
			}
		}
	}
	return rule, nil
}

func buildHeadersMap(mods []HeaderModification) map[string]map[string]string {
	out := make(map[string]map[string]string, len(mods))
	for _, h := range mods {
		name := strings.TrimSpace(h.Name)
		switch h.Operation {
		case HeaderOpSetStatic:
			out[name] = map[string]string{"operation": "set", "value": h.Value}
		case HeaderOpSetExpression:
			out[name] = map[string]string{"operation": "set", "expression": strings.TrimSpace(h.Value)}
		case HeaderOpAddStatic:
			out[name] = map[string]string{"operation": "add", "value": h.Value}
		case HeaderOpAddExpression:
			out[name] = map[string]string{"operation": "add", "expression": strings.TrimSpace(h.Value)}
		case HeaderOpRemove:
			out[name] = map[string]string{"operation": "remove"}
		}
	}
	return out
}

// ZoneRulesEqual reports whether two supported rules in the same category have
// identical effective configuration (used to skip no-op updates).
func ZoneRulesEqual(category ZoneRuleCategory, a, b ZoneRule) bool {
	if a.Description != b.Description || a.Expression != b.Expression || a.Action != b.Action || a.IsEnabled() != b.IsEnabled() {
		return false
	}
	ca, ra := ParseZoneRuleConfig(category, a)
	cb, rb := ParseZoneRuleConfig(category, b)
	if ra != "" || rb != "" || ca == nil || cb == nil {
		return false
	}
	ba, err1 := json.Marshal(ca)
	bb, err2 := json.Marshal(cb)
	return err1 == nil && err2 == nil && bytes.Equal(ba, bb)
}

// --- Cloudflare Rulesets API calls ---

// GetZonePhaseRuleset reads the live entrypoint ruleset for one Zone phase.
// It returns (nil, nil) ONLY when Cloudflare responds 404 (the phase has never
// been initialized); permission, token and API failures are returned as errors.
func (c *Client) GetZonePhaseRuleset(ctx context.Context, zoneID string, phase ZoneRuleCategory) (*ZoneRuleset, error) {
	if !phase.Valid() {
		return nil, fmt.Errorf("cloudflare: unsupported zone rules phase %q", phase)
	}
	var out ZoneRuleset
	path := "/zones/" + esc(zoneID) + "/rulesets/phases/" + esc(string(phase)) + "/entrypoint"
	if err := c.getOne(ctx, path, &out); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// ListZoneRulesets lists rulesets for a phase (kept for compatibility).
func (c *Client) ListZoneRulesets(ctx context.Context, zoneID string, phase ZoneRuleCategory) ([]ZoneRuleset, error) {
	if !phase.Valid() {
		return nil, fmt.Errorf("cloudflare: unsupported zone rules phase %q", phase)
	}
	var out []ZoneRuleset
	q := mapValues("phase", string(phase))
	_, err := c.get(ctx, "/zones/"+esc(zoneID)+"/rulesets", q, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetZoneRuleset reads one complete ruleset by ID.
func (c *Client) GetZoneRuleset(ctx context.Context, zoneID, rulesetID string) (*ZoneRuleset, error) {
	var out ZoneRuleset
	if err := c.getOne(ctx, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rulesetID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateZoneRuleset initializes a missing phase entrypoint ruleset with its
// initial rule(s).
func (c *Client) CreateZoneRuleset(ctx context.Context, zoneID string, rs ZoneRuleset) (*ZoneRuleset, error) {
	if !rs.Phase.Valid() {
		return nil, fmt.Errorf("cloudflare: unsupported zone rules phase %q", rs.Phase)
	}
	if rs.Kind == "" {
		rs.Kind = "zone"
	}
	if rs.Name == "" {
		rs.Name = "default"
	}
	var out ZoneRuleset
	if err := c.send(ctx, http.MethodPost, "/zones/"+esc(zoneID)+"/rulesets", rs, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddZoneRulesetRule appends a single rule to an existing ruleset without
// modifying or replacing surrounding rules.
func (c *Client) AddZoneRulesetRule(ctx context.Context, zoneID, rulesetID string, rule ZoneRule) (*ZoneRuleset, error) {
	if rulesetID == "" {
		return nil, errors.New("cloudflare: ruleset id is required")
	}
	var out ZoneRuleset
	path := "/zones/" + esc(zoneID) + "/rulesets/" + esc(rulesetID) + "/rules"
	if err := c.send(ctx, http.MethodPost, path, rule, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateZoneRulesetRule updates a single rule via PATCH without touching any
// other rule in the ruleset. Cloudflare returns the updated ruleset.
func (c *Client) UpdateZoneRulesetRule(ctx context.Context, zoneID, rulesetID, ruleID string, rule ZoneRule) (*ZoneRuleset, error) {
	if rulesetID == "" || ruleID == "" {
		return nil, errors.New("cloudflare: ruleset and rule ids are required")
	}
	var out ZoneRuleset
	path := "/zones/" + esc(zoneID) + "/rulesets/" + esc(rulesetID) + "/rules/" + esc(ruleID)
	if err := c.send(ctx, http.MethodPatch, path, rule, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteZoneRule deletes a single rule without touching other rules.
func (c *Client) DeleteZoneRule(ctx context.Context, zoneID, rulesetID, ruleID string) error {
	if rulesetID == "" || ruleID == "" {
		return errors.New("cloudflare: ruleset and rule ids are required")
	}
	return c.send(ctx, http.MethodDelete, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rulesetID)+"/rules/"+esc(ruleID), nil, nil)
}

// ReplaceZoneRuleset replaces the complete ruleset while preserving unknown
// ruleset and rule fields.
func (c *Client) ReplaceZoneRuleset(ctx context.Context, zoneID string, rs ZoneRuleset) (*ZoneRuleset, error) {
	if rs.ID == "" {
		return nil, errors.New("cloudflare: ruleset id is required")
	}
	var out ZoneRuleset
	if err := c.send(ctx, http.MethodPut, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rs.ID), rs, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReorderZoneRules verifies that ordered is a complete permutation of rs.Rules
// (refusing partial, unknown, duplicate or unidentified lists) and updates the
// ruleset order.
func (c *Client) ReorderZoneRules(ctx context.Context, zoneID string, rs ZoneRuleset, ordered []ZoneRule) (*ZoneRuleset, error) {
	if len(ordered) != len(rs.Rules) {
		return nil, errors.New("cloudflare: reorder must include every rule")
	}
	want, got := map[string]bool{}, map[string]bool{}
	for _, r := range rs.Rules {
		if r.ID == "" {
			return nil, errors.New("cloudflare: cannot reorder an unidentified rule")
		}
		want[r.ID] = true
	}
	for _, r := range ordered {
		if r.ID == "" || got[r.ID] || !want[r.ID] {
			return nil, errors.New("cloudflare: reorder contains an unknown or duplicate rule")
		}
		got[r.ID] = true
	}
	next := rs
	next.Rules = append([]ZoneRule(nil), ordered...)
	return c.ReplaceZoneRuleset(ctx, zoneID, next)
}

func mapValues(key, value string) url.Values { return url.Values{key: []string{value}} }

func (c *Client) getOne(ctx context.Context, path string, out any) error {
	_, err := c.get(ctx, path, nil, out)
	return err
}
