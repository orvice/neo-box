package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Email Routing: a Zone's settings, rules and catch-all, and the Account's
// destination addresses. Neo Box never disables Email Routing.

const (
	EmailActionForward = "forward"
	EmailActionDrop    = "drop"
	EmailActionWorker  = "worker"

	EmailMatcherLiteral = "literal"
	EmailMatcherAll     = "all"

	// EmailSourceAPI marks a rule managed through the dashboard or API; the
	// other source is "wrangler", a rule owned by a Worker's configuration.
	EmailSourceAPI = "api"
)

func emailPath(zoneID string) string { return "/zones/" + esc(zoneID) + "/email/routing" }

// EmailRoutingSettings is a Zone's Email Routing state.
type EmailRoutingSettings struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Status is ready, unconfigured, misconfigured, misconfigured/locked or
	// unlocked.
	Status            string `json:"status"`
	SupportSubaddress bool   `json:"support_subaddress"`
}

// EmailDNSRecord is a DNS record Email Routing needs.
type EmailDNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority *int   `json:"priority"`
	TTL      int    `json:"ttl"`
}

type EmailAction struct {
	Type  string   `json:"type"`
	Value []string `json:"value,omitempty"`
}

type EmailMatcher struct {
	Type  string `json:"type"`
	Field string `json:"field,omitempty"`
	Value string `json:"value,omitempty"`
}

// EmailRule is a routing rule, or the catch-all.
type EmailRule struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Enabled  bool           `json:"enabled"`
	Priority int            `json:"priority,omitempty"`
	Source   string         `json:"source,omitempty"`
	Matchers []EmailMatcher `json:"matchers"`
	Actions  []EmailAction  `json:"actions"`
}

// Address is the one address the rule matches; "" for the catch-all and for
// a rule that matches anything else.
func (r EmailRule) Address() string {
	if len(r.Matchers) == 1 && r.Matchers[0].Type == EmailMatcherLiteral {
		return r.Matchers[0].Value
	}
	return ""
}

// ActionType is the rule's action; "" unless it has exactly one.
func (r EmailRule) ActionType() string {
	if len(r.Actions) == 1 {
		return r.Actions[0].Type
	}
	return ""
}

// Target is the forward address (or Worker name) of the rule's one action.
func (r EmailRule) Target() string {
	if len(r.Actions) == 1 && len(r.Actions[0].Value) > 0 {
		return r.Actions[0].Value[0]
	}
	return ""
}

func (r EmailRule) isCatchAll() bool {
	for _, m := range r.Matchers {
		if m.Type == EmailMatcherAll {
			return true
		}
	}
	return false
}

// ReadOnlyReason says why Neo Box does not change the rule; "" when it does.
func (r EmailRule) ReadOnlyReason(catchAll bool) string {
	switch {
	case r.Source != "" && r.Source != EmailSourceAPI:
		return "It is managed by a Worker's wrangler configuration, not by the dashboard."
	case r.ActionType() == EmailActionWorker:
		return "It sends mail to a Worker, which Neo Box does not edit."
	case r.ActionType() != EmailActionForward && r.ActionType() != EmailActionDrop:
		return "Its action is not one Neo Box edits."
	case !catchAll && r.Address() == "":
		return "It does not match a single address."
	}
	return ""
}

// Same reports whether o would leave the rule as it is.
func (r EmailRule) Same(o EmailRule) bool {
	return r.Enabled == o.Enabled && r.Name == o.Name &&
		strings.EqualFold(r.Address(), o.Address()) &&
		r.ActionType() == o.ActionType() && strings.EqualFold(r.Target(), o.Target())
}

// EmailAddress is a destination address of the Account.
type EmailAddress struct {
	ID       string     `json:"id"`
	Email    string     `json:"email"`
	Created  *time.Time `json:"created"`
	Verified *time.Time `json:"verified"`
}

func (a EmailAddress) IsVerified() bool { return a.Verified != nil && !a.Verified.IsZero() }

// --- reads and writes ---

// GetEmailRouting reads a Zone's settings. A Zone that never used Email
// Routing is reported as unconfigured.
func (c *Client) GetEmailRouting(ctx context.Context, zoneID string) (*EmailRoutingSettings, error) {
	var s EmailRoutingSettings
	if _, err := c.get(ctx, emailPath(zoneID), nil, &s); err != nil {
		if IsNotFound(err) {
			return &EmailRoutingSettings{Status: "unconfigured"}, nil
		}
		return nil, err
	}
	return &s, nil
}

// EmailRoutingDNS lists the DNS records Email Routing needs in the Zone.
func (c *Client) EmailRoutingDNS(ctx context.Context, zoneID string) ([]EmailDNSRecord, error) {
	var raw json.RawMessage
	if _, err := c.get(ctx, emailPath(zoneID)+"/dns", nil, &raw); err != nil {
		return nil, err
	}
	var list []EmailDNSRecord
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Record []EmailDNSRecord `json:"record"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("cloudflare: decode email routing dns: %w", err)
	}
	return wrapped.Record, nil
}

// EnableEmailRouting enables Email Routing; Cloudflare adds and locks the
// MX and SPF records.
func (c *Client) EnableEmailRouting(ctx context.Context, zoneID string) (*EmailRoutingSettings, error) {
	var s EmailRoutingSettings
	if err := c.send(ctx, http.MethodPost, emailPath(zoneID)+"/dns", struct{}{}, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListEmailRules returns every rule that matches one address; the catch-all
// is read with GetEmailCatchAll.
func (c *Client) ListEmailRules(ctx context.Context, zoneID string) ([]EmailRule, error) {
	var all []EmailRule
	err := c.eachPage(ctx, emailPath(zoneID)+"/rules", 50, func(raw json.RawMessage) error {
		var page []EmailRule
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, r := range page {
			if !r.isCatchAll() {
				all = append(all, r)
			}
		}
		return nil
	})
	return all, err
}

func (c *Client) CreateEmailRule(ctx context.Context, zoneID string, r EmailRule) (*EmailRule, error) {
	r.ID = ""
	var out EmailRule
	if err := c.send(ctx, http.MethodPost, emailPath(zoneID)+"/rules", r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateEmailRule replaces the rule; r must hold all of it.
func (c *Client) UpdateEmailRule(ctx context.Context, zoneID, id string, r EmailRule) (*EmailRule, error) {
	r.ID = ""
	var out EmailRule
	if err := c.send(ctx, http.MethodPut, emailPath(zoneID)+"/rules/"+esc(id), r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteEmailRule(ctx context.Context, zoneID, id string) error {
	return c.send(ctx, http.MethodDelete, emailPath(zoneID)+"/rules/"+esc(id), nil, nil)
}

func (c *Client) GetEmailCatchAll(ctx context.Context, zoneID string) (*EmailRule, error) {
	var r EmailRule
	if _, err := c.get(ctx, emailPath(zoneID)+"/rules/catch_all", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) UpdateEmailCatchAll(ctx context.Context, zoneID string, r EmailRule) (*EmailRule, error) {
	r.ID = ""
	var out EmailRule
	if err := c.send(ctx, http.MethodPut, emailPath(zoneID)+"/rules/catch_all", r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func emailAddressesPath(accountID string) string {
	return "/accounts/" + esc(accountID) + "/email/routing/addresses"
}

// ListEmailAddresses returns every destination address of the Account.
func (c *Client) ListEmailAddresses(ctx context.Context, accountID string) ([]EmailAddress, error) {
	var all []EmailAddress
	err := c.eachPage(ctx, emailAddressesPath(accountID), 50, func(raw json.RawMessage) error {
		var page []EmailAddress
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	})
	return all, err
}

// CreateEmailAddress adds a destination address; Cloudflare mails it a link
// that verifies it.
func (c *Client) CreateEmailAddress(ctx context.Context, accountID, email string) (*EmailAddress, error) {
	var out EmailAddress
	if err := c.send(ctx, http.MethodPost, emailAddressesPath(accountID), map[string]string{"email": email}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteEmailAddress(ctx context.Context, accountID, id string) error {
	return c.send(ctx, http.MethodDelete, emailAddressesPath(accountID)+"/"+esc(id), nil, nil)
}

// --- input ---

var emailAddressPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// ValidateEmailAddress checks an address; field names it in the error.
func ValidateEmailAddress(field, addr string) error {
	if len(addr) > 90 || !emailAddressPattern.MatchString(addr) {
		return invalid(field, "must be an email address such as name@example.com")
	}
	return nil
}

func validateEmailAction(action, target string) error {
	switch action {
	case EmailActionDrop:
		return nil
	case EmailActionForward:
		return ValidateEmailAddress("target", target)
	}
	return invalid("action", "must be forward or drop")
}

// EmailRuleInput is what the user sets on a rule that matches one address.
type EmailRuleInput struct {
	Name    string
	Address string
	// Action is forward or drop; Target is the forward address.
	Action, Target string
	Enabled        bool
}

// Normalized trims the input and lowercases addresses and the action.
func (in EmailRuleInput) Normalized() EmailRuleInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.ToLower(strings.TrimSpace(in.Address))
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	in.Target = strings.ToLower(strings.TrimSpace(in.Target))
	return in
}

// ValidateEmailRuleInput checks a normalized input for a rule of the Zone.
func ValidateEmailRuleInput(zoneName string, in EmailRuleInput) error {
	if len(in.Name) > 256 {
		return invalid("name", "must be at most 256 characters")
	}
	if err := ValidateEmailAddress("address", in.Address); err != nil {
		return err
	}
	if domain := in.Address[strings.LastIndex(in.Address, "@")+1:]; !strings.EqualFold(domain, zoneName) {
		return invalid("address", "must be an address at %s", zoneName)
	}
	return validateEmailAction(in.Action, in.Target)
}

func emailActions(action, target string) []EmailAction {
	if action == EmailActionForward {
		return []EmailAction{{Type: EmailActionForward, Value: []string{target}}}
	}
	return []EmailAction{{Type: EmailActionDrop}}
}

// Rule builds the rule the input describes. base is the rule being changed
// (nil for a new one); it supplies the ID, priority and source.
func (in EmailRuleInput) Rule(base *EmailRule) EmailRule {
	r := EmailRule{
		Name: in.Name, Enabled: in.Enabled,
		Matchers: []EmailMatcher{{Type: EmailMatcherLiteral, Field: "to", Value: in.Address}},
		Actions:  emailActions(in.Action, in.Target),
	}
	if base != nil {
		r.ID, r.Priority, r.Source = base.ID, base.Priority, base.Source
	}
	return r
}

// EmailCatchAllInput is what the user sets on the catch-all. Off
// (Enabled false) keeps whatever action the catch-all has.
type EmailCatchAllInput struct {
	Enabled        bool
	Action, Target string
}

func (in EmailCatchAllInput) Normalized() EmailCatchAllInput {
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))
	in.Target = strings.ToLower(strings.TrimSpace(in.Target))
	return in
}

func ValidateEmailCatchAllInput(in EmailCatchAllInput) error {
	if !in.Enabled {
		return nil
	}
	return validateEmailAction(in.Action, in.Target)
}

// Rule builds the catch-all the input describes, from the current one.
func (in EmailCatchAllInput) Rule(base EmailRule) EmailRule {
	r := EmailRule{
		ID: base.ID, Name: base.Name, Source: base.Source, Enabled: in.Enabled,
		Matchers: []EmailMatcher{{Type: EmailMatcherAll}},
	}
	switch {
	case in.Enabled:
		r.Actions = emailActions(in.Action, in.Target)
	case len(base.Actions) > 0:
		r.Actions = base.Actions
	default:
		r.Actions = emailActions(EmailActionDrop, "")
	}
	return r
}
