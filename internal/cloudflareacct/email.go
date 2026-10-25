package cloudflareacct

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
)

// Email Routing: a Zone's rules and catch-all, and the Account's destination
// addresses. Every write is logged through operate as an email_routing
// operation whose Resource is the Zone's name, or emailAddressesResource for
// the Account's addresses. Neo Box never disables Email Routing.

const (
	emailRulesReadPermission = "Email Routing Rules Read"
	emailRulesEditPermission = "Email Routing Rules Write"
	emailAddrReadPermission  = "Email Routing Addresses Read"
	emailAddrEditPermission  = "Email Routing Addresses Write"

	// emailAddressesResource is the Resource of destination address
	// operations, which belong to the Account and not to a Zone.
	emailAddressesResource = "destination addresses"
)

var (
	// ErrEmailRuleNotFound: no such routing rule in the Zone.
	ErrEmailRuleNotFound = errors.New("routing rule not found in this zone")
	// ErrEmailAddressNotFound: no such destination address in the Account.
	ErrEmailAddressNotFound = errors.New("destination address not found in this account")
	// ErrAddressUnverified: a forward needs a verified destination address
	// of the Account.
	ErrAddressUnverified = errors.New("the destination address is not verified yet, or is not one of this account's")
	// ErrAddressInUse: a rule or the catch-all still forwards to the
	// address.
	ErrAddressInUse = errors.New("the destination address is still used")
	// ErrRuleReadOnly: the rule sends to a Worker, is managed by a Worker's
	// wrangler configuration, or is not one Neo Box edits.
	ErrRuleReadOnly = errors.New("this routing rule cannot be changed here")
)

// EmailRoutingView is what Neo Box shows of a Zone's Email Routing. A part
// that could not be read is empty and named in Issues.
type EmailRoutingView struct {
	Settings cloudflare.EmailRoutingSettings
	Rules    []cloudflare.EmailRule
	// CatchAll is nil when it could not be read.
	CatchAll *cloudflare.EmailRule
	// RequiredDNS is read only while Email Routing is not enabled.
	RequiredDNS []cloudflare.EmailDNSRecord
	Issues      []AddressIssue
}

func (a *Account) emailLock(key string) func() {
	return a.m.lockResource("email/" + a.ID + "/" + key)
}

// emailRuleState is a rule as the log keeps it.
func emailRuleState(zone string, r cloudflare.EmailRule) map[string]any {
	m := map[string]any{"zone": zone, "enabled": r.Enabled, "action": r.ActionType()}
	if addr := r.Address(); addr != "" {
		m["address"] = addr
	}
	if t := r.Target(); t != "" {
		m["target"] = t
	}
	if r.Name != "" {
		m["name"] = r.Name
	}
	return m
}

// EmailRouting reads a Zone's Email Routing live. Without the settings
// there is nothing to show, so a failure to read them fails the call; the
// other parts fail one by one.
func (a *Account) EmailRouting(ctx context.Context, zoneID string) (*EmailRoutingView, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	st, err := a.api.GetEmailRouting(ctx, zone.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's Email Routing settings", emailRulesReadPermission)
	}
	v := &EmailRoutingView{Settings: *st}
	var (
		issues issueSet
		wg     sync.WaitGroup
	)
	read := func(source string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				issues.add(source, err)
			}
		}()
	}
	read("routing rules", func() (err error) {
		v.Rules, err = a.api.ListEmailRules(ctx, zone.ID)
		return err
	})
	read("catch-all rule", func() (err error) {
		v.CatchAll, err = a.api.GetEmailCatchAll(ctx, zone.ID)
		return err
	})
	if !st.Enabled {
		read("required DNS records", func() (err error) {
			v.RequiredDNS, err = a.api.EmailRoutingDNS(ctx, zone.ID)
			return err
		})
	}
	wg.Wait()
	sort.Slice(v.Rules, func(i, j int) bool {
		x, y := strings.ToLower(v.Rules[i].Address()), strings.ToLower(v.Rules[j].Address())
		if x != y {
			return x < y
		}
		return v.Rules[i].ID < v.Rules[j].ID
	})
	v.Issues = issues.list
	return v, nil
}

// EnableEmailRouting enables Email Routing; Cloudflare adds and locks the
// MX and SPF records. Nothing is done when it is already enabled.
func (a *Account) EnableEmailRouting(ctx context.Context, actor Actor, zoneID string) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.emailLock(zone.ID)()
	cur, err := a.api.GetEmailRouting(ctx, zone.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's Email Routing settings", emailRulesReadPermission)
	}
	if cur.Enabled {
		return noChange(), nil
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: zone.Name, action: "enable_routing",
		before:    map[string]any{"enabled": false, "status": cur.Status},
		requested: map[string]any{"enabled": true},
		what:      "enable Email Routing", permission: emailRulesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			s, err := a.api.EnableEmailRouting(ctx, zone.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"enabled": s.Enabled, "status": s.Status}, nil
		},
	})
}

func (a *Account) zoneEmailRules(ctx context.Context, zoneID string) ([]cloudflare.EmailRule, error) {
	rules, err := a.api.ListEmailRules(ctx, zoneID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's routing rules", emailRulesReadPermission)
	}
	return rules, nil
}

func findEmailRule(rules []cloudflare.EmailRule, id string) *cloudflare.EmailRule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

// requireVerified refuses an enabled forward to an address that is not a
// verified destination address of the Account.
func (a *Account) requireVerified(ctx context.Context, r cloudflare.EmailRule) error {
	if !r.Enabled || r.ActionType() != cloudflare.EmailActionForward {
		return nil
	}
	addrs, err := a.api.ListEmailAddresses(ctx, a.ID)
	if err != nil {
		return a.classify(ctx, err, "read destination addresses", emailAddrReadPermission)
	}
	for _, ad := range addrs {
		if strings.EqualFold(ad.Email, r.Target()) && ad.IsVerified() {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrAddressUnverified, r.Target())
}

// CreateEmailRule adds a rule for one address of the Zone.
func (a *Account) CreateEmailRule(ctx context.Context, actor Actor, zoneID string, in cloudflare.EmailRuleInput) (*OperationResult, error) {
	in = in.Normalized()
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateEmailRuleInput(zone.Name, in); err != nil {
		return nil, err
	}
	defer a.emailLock(zone.ID)()
	rules, err := a.zoneEmailRules(ctx, zone.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if strings.EqualFold(r.Address(), in.Address) {
			return nil, fmt.Errorf("%w: %s already has a routing rule", ErrInUse, in.Address)
		}
	}
	rule := in.Rule(nil)
	if err := a.requireVerified(ctx, rule); err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: zone.Name, action: "create_rule", target: in.Address,
		requested: emailRuleState(zone.Name, rule),
		what:      "create routing rules", permission: emailRulesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			got, err := a.api.CreateEmailRule(ctx, zone.ID, rule)
			if err != nil {
				return nil, err
			}
			return emailRuleState(zone.Name, *got), nil
		},
	})
}

// UpdateEmailRule replaces a rule's fields; that includes enabling and
// disabling it, which the log records as such.
func (a *Account) UpdateEmailRule(ctx context.Context, actor Actor, zoneID, ruleID string, in cloudflare.EmailRuleInput) (*OperationResult, error) {
	in = in.Normalized()
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateEmailRuleInput(zone.Name, in); err != nil {
		return nil, err
	}
	defer a.emailLock(zone.ID)()
	rules, err := a.zoneEmailRules(ctx, zone.ID)
	if err != nil {
		return nil, err
	}
	cur := findEmailRule(rules, ruleID)
	if cur == nil {
		return nil, ErrEmailRuleNotFound
	}
	if why := cur.ReadOnlyReason(false); why != "" {
		return nil, fmt.Errorf("%w: %s", ErrRuleReadOnly, why)
	}
	for _, r := range rules {
		if r.ID != cur.ID && strings.EqualFold(r.Address(), in.Address) {
			return nil, fmt.Errorf("%w: %s already has a routing rule", ErrInUse, in.Address)
		}
	}
	next := in.Rule(cur)
	if cur.Same(next) {
		return noChange(), nil
	}
	if err := a.requireVerified(ctx, next); err != nil {
		return nil, err
	}
	// A change of only the enabled flag is logged as enabling or disabling.
	action := "update_rule"
	toggled := *cur
	toggled.Enabled = next.Enabled
	if toggled.Same(next) {
		action = "disable_rule"
		if next.Enabled {
			action = "enable_rule"
		}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: zone.Name, action: action, target: cur.Address(),
		before:    emailRuleState(zone.Name, *cur),
		requested: emailRuleState(zone.Name, next),
		what:      "change routing rules", permission: emailRulesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			got, err := a.api.UpdateEmailRule(ctx, zone.ID, cur.ID, next)
			if err != nil {
				return nil, err
			}
			return emailRuleState(zone.Name, *got), nil
		},
	})
}

// DeleteEmailRule deletes a rule.
func (a *Account) DeleteEmailRule(ctx context.Context, actor Actor, zoneID, ruleID string) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer a.emailLock(zone.ID)()
	rules, err := a.zoneEmailRules(ctx, zone.ID)
	if err != nil {
		return nil, err
	}
	cur := findEmailRule(rules, ruleID)
	if cur == nil {
		return nil, ErrEmailRuleNotFound
	}
	if why := cur.ReadOnlyReason(false); why != "" {
		return nil, fmt.Errorf("%w: %s", ErrRuleReadOnly, why)
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: zone.Name, action: "delete_rule", target: cur.Address(),
		before: emailRuleState(zone.Name, *cur),
		what:   "delete routing rules", permission: emailRulesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return nil, a.api.DeleteEmailRule(ctx, zone.ID, cur.ID)
		},
	})
}

// SetEmailCatchAll sets the Zone's catch-all rule.
func (a *Account) SetEmailCatchAll(ctx context.Context, actor Actor, zoneID string, in cloudflare.EmailCatchAllInput) (*OperationResult, error) {
	in = in.Normalized()
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if err := cloudflare.ValidateEmailCatchAllInput(in); err != nil {
		return nil, err
	}
	defer a.emailLock(zone.ID)()
	cur, err := a.api.GetEmailCatchAll(ctx, zone.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's catch-all rule", emailRulesReadPermission)
	}
	if why := cur.ReadOnlyReason(true); why != "" {
		return nil, fmt.Errorf("%w: %s", ErrRuleReadOnly, why)
	}
	next := in.Rule(*cur)
	if cur.Same(next) {
		return noChange(), nil
	}
	if err := a.requireVerified(ctx, next); err != nil {
		return nil, err
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: zone.Name, action: "set_catch_all", target: "catch_all",
		before:    emailRuleState(zone.Name, *cur),
		requested: emailRuleState(zone.Name, next),
		what:      "change the catch-all rule", permission: emailRulesEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			got, err := a.api.UpdateEmailCatchAll(ctx, zone.ID, next)
			if err != nil {
				return nil, err
			}
			return emailRuleState(zone.Name, *got), nil
		},
	})
}

// EmailAddresses returns the Account's destination addresses, by address.
func (a *Account) EmailAddresses(ctx context.Context) ([]cloudflare.EmailAddress, error) {
	list, err := a.api.ListEmailAddresses(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read destination addresses", emailAddrReadPermission)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Email) < strings.ToLower(list[j].Email) })
	return list, nil
}

// CreateEmailAddress adds a destination address; Cloudflare mails it a link
// that verifies it. An address the Account already has is left alone.
func (a *Account) CreateEmailAddress(ctx context.Context, actor Actor, email string) (*OperationResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := cloudflare.ValidateEmailAddress("email", email); err != nil {
		return nil, err
	}
	defer a.emailLock("addresses")()
	list, err := a.api.ListEmailAddresses(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read destination addresses", emailAddrReadPermission)
	}
	for _, ad := range list {
		if strings.EqualFold(ad.Email, email) {
			return noChange(), nil
		}
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: emailAddressesResource, action: "create_address", target: email,
		requested: map[string]any{"email": email},
		what:      "add destination addresses", permission: emailAddrEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			got, err := a.api.CreateEmailAddress(ctx, a.ID, email)
			if err != nil {
				return nil, err
			}
			return map[string]any{"email": got.Email, "verified": got.IsVerified()}, nil
		},
	})
}

// DeleteEmailAddress deletes a destination address, unless a rule or the
// catch-all of the Zone still forwards to it. Rules of the Account's other
// Zones are not checked.
func (a *Account) DeleteEmailAddress(ctx context.Context, actor Actor, zoneID, addressID string) (*OperationResult, error) {
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	list, err := a.api.ListEmailAddresses(ctx, a.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read destination addresses", emailAddrReadPermission)
	}
	var addr *cloudflare.EmailAddress
	for i := range list {
		if list[i].ID == addressID {
			addr = &list[i]
		}
	}
	if addr == nil {
		return nil, ErrEmailAddressNotFound
	}
	defer a.emailLock(zone.ID)()
	rules, err := a.zoneEmailRules(ctx, zone.ID)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.ActionType() == cloudflare.EmailActionForward && strings.EqualFold(r.Target(), addr.Email) {
			return nil, fmt.Errorf("%w: the rule for %s in %s forwards to %s", ErrAddressInUse, r.Address(), zone.Name, addr.Email)
		}
	}
	catchAll, err := a.api.GetEmailCatchAll(ctx, zone.ID)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's catch-all rule", emailRulesReadPermission)
	}
	if catchAll.ActionType() == cloudflare.EmailActionForward && strings.EqualFold(catchAll.Target(), addr.Email) {
		return nil, fmt.Errorf("%w: the catch-all rule of %s forwards to %s", ErrAddressInUse, zone.Name, addr.Email)
	}
	return a.operate(ctx, actor, resourceChange{
		kind: cfrepo.KindEmailRouting, resource: emailAddressesResource, action: "delete_address", target: addr.Email,
		before: map[string]any{"email": addr.Email, "verified": addr.IsVerified()},
		what:   "delete destination addresses", permission: emailAddrEditPermission,
		send: func(ctx context.Context) (map[string]any, error) {
			return nil, a.api.DeleteEmailAddress(ctx, a.ID, addr.ID)
		},
	})
}

// emailLogsPermission is what Cloudflare's GraphQL Analytics API needs to
// read a Zone's Email Routing events.
const emailLogsPermission = "Analytics Read"

// EmailLogs reads a Zone's Email Routing events, newest first, from
// Cloudflare's GraphQL Analytics API. Nothing is stored and, being a read,
// nothing is logged. The Zone must belong to the connection's Account.
func (a *Account) EmailLogs(ctx context.Context, zoneID string, q cloudflare.EmailLogQuery) ([]cloudflare.EmailLogEvent, error) {
	q = q.Normalized()
	if err := q.Validate(a.m.now()); err != nil {
		return nil, err
	}
	zone, err := a.Zone(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	events, err := a.api.ListEmailRoutingEvents(ctx, zone.ID, q)
	if err != nil {
		return nil, a.classify(ctx, err, "read this Zone's Email Routing logs", emailLogsPermission)
	}
	return events, nil
}
