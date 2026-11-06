package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

// ZoneRuleCategory is one of the Zone Rules phases supported by Neo Box.
// The values are Cloudflare product/API names and are intentionally kept
// separate: Cloudflare evaluates each phase independently.
type ZoneRuleCategory string

const (
	ZoneRuleSingleRedirect ZoneRuleCategory = "http_request_redirect"
	ZoneRuleURLRewrite     ZoneRuleCategory = "http_request_transform"
	ZoneRuleRequestHeader  ZoneRuleCategory = "http_request_late_transform"
	ZoneRuleResponseHeader ZoneRuleCategory = "http_response_headers_transform"
	ZoneRuleOrigin         ZoneRuleCategory = "http_request_origin"
)

var zoneRuleCategories = []ZoneRuleCategory{
	ZoneRuleSingleRedirect, ZoneRuleURLRewrite, ZoneRuleRequestHeader,
	ZoneRuleResponseHeader, ZoneRuleOrigin,
}

func (c ZoneRuleCategory) Valid() bool {
	for _, v := range zoneRuleCategories {
		if c == v {
			return true
		}
	}
	return false
}

// ZoneRule is a Cloudflare ruleset rule. Action and ActionParameters are raw
// JSON on purpose: Cloudflare adds fields over time and Neo Box must preserve
// unfamiliar configuration when it performs a safe read-modify-write.
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
	// Unknown preserves fields returned by Cloudflare that are not represented
	// above. It is display-only and is merged back by PreserveRule.
	Unknown map[string]json.RawMessage `json:"-"`
}

// ZoneRuleset is the complete live state of one phase. Rules are ordered as
// Cloudflare evaluates them. ETag is retained when supplied by the API.
type ZoneRuleset struct {
	ID          string           `json:"id,omitempty"`
	Name        string           `json:"name,omitempty"`
	Description string           `json:"description,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Phase       ZoneRuleCategory `json:"phase"`
	Version     string           `json:"version,omitempty"`
	ETag        string           `json:"etag,omitempty"`
	Rules       []ZoneRule       `json:"rules"`
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
	r.Unknown = raw
	return nil
}

// MarshalJSON merges Unknown into the normal representation, so a rule can
// be sent back without dropping fields Neo Box does not understand.
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

// ListZoneRulesets reads all rulesets for a phase. A missing phase is returned
// as an empty slice; callers can distinguish an absent ruleset from a failed
// request by the error value.
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

// GetZoneRuleset reads one complete ruleset, including every rule and its
// unknown fields. It is used immediately before writes for conflict checks.
func (c *Client) GetZoneRuleset(ctx context.Context, zoneID, rulesetID string) (*ZoneRuleset, error) {
	var out ZoneRuleset
	if err := c.getOne(ctx, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rulesetID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateZoneRuleset initializes a missing phase. Callers must pass the exact
// phase and should log the initialization intent before invoking this method.
func (c *Client) CreateZoneRuleset(ctx context.Context, zoneID string, rs ZoneRuleset) (*ZoneRuleset, error) {
	if !rs.Phase.Valid() {
		return nil, fmt.Errorf("cloudflare: unsupported zone rules phase %q", rs.Phase)
	}
	if rs.Kind == "" {
		rs.Kind = "zone"
	}
	var out ZoneRuleset
	if err := c.send(ctx, http.MethodPost, "/zones/"+esc(zoneID)+"/rulesets", rs, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReplaceZoneRuleset replaces the ordered ruleset while preserving the caller
// supplied complete rule objects. Cloudflare's response is the authoritative
// resulting state.
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

// UpdateZoneRule updates a single rule and leaves all other rules untouched.
func (c *Client) UpdateZoneRule(ctx context.Context, zoneID, rulesetID string, rule ZoneRule) (*ZoneRule, error) {
	if rulesetID == "" || rule.ID == "" {
		return nil, errors.New("cloudflare: ruleset and rule ids are required")
	}
	var out ZoneRule
	if err := c.send(ctx, http.MethodPut, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rulesetID)+"/rules/"+esc(rule.ID), rule, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteZoneRule(ctx context.Context, zoneID, rulesetID, ruleID string) error {
	if rulesetID == "" || ruleID == "" {
		return errors.New("cloudflare: ruleset and rule ids are required")
	}
	return c.send(ctx, http.MethodDelete, "/zones/"+esc(zoneID)+"/rulesets/"+esc(rulesetID)+"/rules/"+esc(ruleID), nil, nil)
}

// ReorderZoneRules verifies all IDs belong to the supplied ruleset and then
// replaces the full ordered list. It refuses a partial list because that would
// risk deleting an unfamiliar rule.
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

// getOne uses the regular client decoder without exposing its envelope.
func (c *Client) getOne(ctx context.Context, path string, out any) error {
	_, err := c.get(ctx, path, nil, out)
	return err
}

// SortRules returns a copy ordered by Cloudflare's explicit order, useful for
// stable comparisons and UI presentation.
func SortRules(rules []ZoneRule) []ZoneRule {
	out := append([]ZoneRule(nil), rules...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
