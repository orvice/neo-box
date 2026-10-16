package cftest

import (
	"fmt"
	"net/http"
	"strings"
)

// Email Routing permissions a Token can hold.
const (
	PermEmailRulesRead = "email_rules:read" // Email Routing Rules Read (zone)
	PermEmailRulesEdit = "email_rules:edit" // Email Routing Rules Write (zone)
	PermEmailAddrRead  = "email_addr:read"  // Email Routing Addresses Read (account)
	PermEmailAddrEdit  = "email_addr:edit"  // Email Routing Addresses Write (account)
)

// EmailRule is a routing rule of a Zone. Address is empty for the catch-all.
type EmailRule struct {
	ID, Name, Source string
	Enabled          bool
	Priority         int
	Address          string
	// Action is forward, drop or worker; Target is the forward address or
	// the Worker's name.
	Action, Target string
}

// EmailAddress is a destination address of an Account.
type EmailAddress struct {
	ID, Email string
	Verified  bool
}

type emailZone struct {
	enabled  bool
	rules    []*EmailRule
	catchAll *EmailRule
}

// EmailState is a copy of a Zone's Email Routing state.
type EmailState struct {
	Enabled  bool
	Rules    []EmailRule
	CatchAll EmailRule
}

func (s *Server) emailOf(zoneID string) *emailZone {
	ez := s.emailZones[zoneID]
	if ez == nil {
		ez = &emailZone{catchAll: &EmailRule{ID: "catchall-" + zoneID, Name: "Catch-all rule", Source: "api", Action: "drop"}}
		s.emailZones[zoneID] = ez
	}
	return ez
}

// AddEmailAddress adds a destination address to an Account.
func (s *Server) AddEmailAddress(account string, a *EmailAddress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		s.nextID++
		a.ID = fmt.Sprintf("ea%d", s.nextID)
	}
	s.emailAddrs[account] = append(s.emailAddrs[account], a)
}

// EmailAddresses returns copies of an Account's destination addresses.
func (s *Server) EmailAddresses(account string) []EmailAddress {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []EmailAddress
	for _, a := range s.emailAddrs[account] {
		out = append(out, *a)
	}
	return out
}

// VerifyEmailAddress marks an address verified, as clicking Cloudflare's link does.
func (s *Server) VerifyEmailAddress(account, email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.emailAddrs[account] {
		if strings.EqualFold(a.Email, email) {
			a.Verified = true
		}
	}
}

// SetEmailEnabled turns Email Routing on or off for a Zone.
func (s *Server) SetEmailEnabled(zoneID string, enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emailOf(zoneID).enabled = enabled
}

// AddEmailRule adds a routing rule to a Zone.
func (s *Server) AddEmailRule(zoneID string, r *EmailRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == "" {
		s.nextID++
		r.ID = fmt.Sprintf("er%d", s.nextID)
	}
	if r.Source == "" {
		r.Source = "api"
	}
	ez := s.emailOf(zoneID)
	ez.rules = append(ez.rules, r)
}

// EmailRouting returns a copy of a Zone's Email Routing state.
func (s *Server) EmailRouting(zoneID string) EmailState {
	s.mu.Lock()
	defer s.mu.Unlock()
	ez := s.emailOf(zoneID)
	st := EmailState{Enabled: ez.enabled, CatchAll: *ez.catchAll}
	for _, r := range ez.rules {
		st.Rules = append(st.Rules, *r)
	}
	return st
}

func (s *Server) emailVerified(account, email string) bool {
	for _, a := range s.emailAddrs[account] {
		if strings.EqualFold(a.Email, email) {
			return a.Verified
		}
	}
	return false
}

func emailRuleJSON(r *EmailRule) map[string]any {
	matchers := []map[string]any{{"type": "all"}}
	if r.Address != "" {
		matchers = []map[string]any{{"type": "literal", "field": "to", "value": r.Address}}
	}
	action := map[string]any{"type": r.Action}
	if r.Target != "" {
		action["value"] = []string{r.Target}
	}
	return map[string]any{
		"id": r.ID, "tag": r.ID, "name": r.Name, "enabled": r.Enabled, "priority": r.Priority,
		"source": r.Source, "matchers": matchers, "actions": []map[string]any{action},
	}
}

func (s *Server) emailSettings(z *Zone, ez *emailZone) map[string]any {
	status := "unconfigured"
	if ez.enabled {
		status = "ready"
	}
	return map[string]any{
		"id": "set-" + z.ID, "name": z.Name, "enabled": ez.enabled, "status": status,
		"support_subaddress": false, "skip_wizard": false,
	}
}

func emailDNSRecords(zone string) []map[string]any {
	return []map[string]any{
		{"type": "MX", "name": zone, "content": "route1.mx.cloudflare.net", "priority": 20, "ttl": 1},
		{"type": "MX", "name": zone, "content": "route2.mx.cloudflare.net", "priority": 83, "ttl": 1},
		{"type": "MX", "name": zone, "content": "route3.mx.cloudflare.net", "priority": 91, "ttl": 1},
		{"type": "TXT", "name": zone, "content": "v=spf1 include:_spf.mx.cloudflare.net ~all", "ttl": 1},
	}
}

// parseEmailRule reads a rule body; the second result is a refusal message.
// A forward action needs a verified address, as at Cloudflare.
func (s *Server) parseEmailRule(z *Zone, body map[string]any, catchAll bool) (*EmailRule, string) {
	r := &EmailRule{Source: "api", Enabled: true}
	if v, ok := body["enabled"].(bool); ok {
		r.Enabled = v
	}
	r.Name, _ = body["name"].(string)
	if v, ok := body["priority"].(float64); ok {
		r.Priority = int(v)
	}
	if v, ok := body["source"].(string); ok && v != "" {
		r.Source = v
	}
	if !catchAll {
		ms, _ := body["matchers"].([]any)
		if len(ms) != 1 {
			return nil, "exactly one matcher is required"
		}
		m, _ := ms[0].(map[string]any)
		if m["type"] != "literal" {
			return nil, "the matcher must be literal"
		}
		r.Address, _ = m["value"].(string)
		if r.Address == "" {
			return nil, "the matcher value is required"
		}
	}
	as, _ := body["actions"].([]any)
	if len(as) != 1 {
		return nil, "exactly one action is required"
	}
	a, _ := as[0].(map[string]any)
	r.Action, _ = a["type"].(string)
	if vals, _ := a["value"].([]any); len(vals) > 0 {
		r.Target, _ = vals[0].(string)
	}
	switch r.Action {
	case "drop", "worker":
	case "forward":
		if !s.emailVerified(z.Account, r.Target) {
			return nil, "destination address " + r.Target + " is not verified"
		}
	default:
		return nil, "unknown action"
	}
	return r, ""
}

// emailRoutes answers /zones/{id}/email/routing/{rest...}.
func (s *Server) emailRoutes(w http.ResponseWriter, r *http.Request, tok *Token, z *Zone, rest []string, body map[string]any) {
	write := r.Method != http.MethodGet
	if (write && !tok.has(PermEmailRulesEdit, z.Account)) ||
		(!write && !tok.has(PermEmailRulesRead, z.Account) && !tok.has(PermEmailRulesEdit, z.Account)) {
		denied(w)
		return
	}
	ez := s.emailOf(z.ID)
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		ok(w, s.emailSettings(z, ez), nil)
	case len(rest) == 1 && rest[0] == "dns" && r.Method == http.MethodGet:
		ok(w, emailDNSRecords(z.Name), nil)
	case len(rest) == 1 && rest[0] == "dns" && r.Method == http.MethodPost:
		ez.enabled = true
		ok(w, s.emailSettings(z, ez), nil)
	case len(rest) >= 1 && rest[0] == "rules":
		s.emailRules(w, r, z, ez, rest[1:], body)
	default:
		notFound(w, r.URL.Path)
	}
}

func (s *Server) emailRules(w http.ResponseWriter, r *http.Request, z *Zone, ez *emailZone, rest []string, body map[string]any) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		page, perPage := pageParams(r.URL.Query(), 20, 5, 50)
		idx, info := paginate(len(ez.rules), page, perPage)
		out := []map[string]any{}
		for _, i := range idx {
			out = append(out, emailRuleJSON(ez.rules[i]))
		}
		ok(w, out, info)
	case len(rest) == 0 && r.Method == http.MethodPost:
		rule, msg := s.parseEmailRule(z, body, false)
		if msg != "" {
			Fail(w, http.StatusBadRequest, 2020, msg)
			return
		}
		s.nextID++
		rule.ID = fmt.Sprintf("er%d", s.nextID)
		ez.rules = append(ez.rules, rule)
		ok(w, emailRuleJSON(rule), nil)
	case len(rest) == 1 && rest[0] == "catch_all":
		switch r.Method {
		case http.MethodGet:
			ok(w, emailRuleJSON(ez.catchAll), nil)
		case http.MethodPut:
			rule, msg := s.parseEmailRule(z, body, true)
			if msg != "" {
				Fail(w, http.StatusBadRequest, 2020, msg)
				return
			}
			ez.catchAll.Name, ez.catchAll.Enabled = rule.Name, rule.Enabled
			ez.catchAll.Action, ez.catchAll.Target = rule.Action, rule.Target
			ok(w, emailRuleJSON(ez.catchAll), nil)
		default:
			notFound(w, r.URL.Path)
		}
	case len(rest) == 1:
		idx := -1
		for i, rule := range ez.rules {
			if rule.ID == rest[0] {
				idx = i
			}
		}
		if idx < 0 {
			Fail(w, http.StatusNotFound, 2020, "Routing rule not found")
			return
		}
		cur := ez.rules[idx]
		switch r.Method {
		case http.MethodGet:
			ok(w, emailRuleJSON(cur), nil)
		case http.MethodPut:
			rule, msg := s.parseEmailRule(z, body, false)
			if msg != "" {
				Fail(w, http.StatusBadRequest, 2020, msg)
				return
			}
			cur.Name, cur.Enabled, cur.Priority = rule.Name, rule.Enabled, rule.Priority
			cur.Address, cur.Action, cur.Target = rule.Address, rule.Action, rule.Target
			ok(w, emailRuleJSON(cur), nil)
		case http.MethodDelete:
			ez.rules = append(ez.rules[:idx], ez.rules[idx+1:]...)
			ok(w, map[string]any{"id": cur.ID}, nil)
		default:
			notFound(w, r.URL.Path)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func emailAddressJSON(a *EmailAddress) map[string]any {
	m := map[string]any{"id": a.ID, "tag": a.ID, "email": a.Email, "created": "2026-01-02T03:04:05Z", "modified": "2026-01-02T03:04:05Z", "verified": nil}
	if a.Verified {
		m["verified"] = "2026-01-02T03:05:00Z"
	}
	return m
}

// emailAccountRoutes answers /accounts/{id}/email/routing/addresses[/{id}].
func (s *Server) emailAccountRoutes(w http.ResponseWriter, r *http.Request, tok *Token, parts []string, body map[string]any) {
	acc := parts[1]
	if len(parts) < 5 || parts[4] != "addresses" || !contains(tok.Accounts, acc) {
		notFound(w, r.URL.Path)
		return
	}
	write := r.Method != http.MethodGet
	if (write && !tok.has(PermEmailAddrEdit, acc)) ||
		(!write && !tok.has(PermEmailAddrRead, acc) && !tok.has(PermEmailAddrEdit, acc)) {
		denied(w)
		return
	}
	switch {
	case len(parts) == 5 && r.Method == http.MethodGet:
		list := s.emailAddrs[acc]
		page, perPage := pageParams(r.URL.Query(), 20, 5, 50)
		idx, info := paginate(len(list), page, perPage)
		out := []map[string]any{}
		for _, i := range idx {
			out = append(out, emailAddressJSON(list[i]))
		}
		ok(w, out, info)
	case len(parts) == 5 && r.Method == http.MethodPost:
		email, _ := body["email"].(string)
		if email == "" {
			Fail(w, http.StatusBadRequest, 2001, "email is required")
			return
		}
		for _, a := range s.emailAddrs[acc] {
			if strings.EqualFold(a.Email, email) {
				Fail(w, http.StatusConflict, 2005, "destination address already exists")
				return
			}
		}
		s.nextID++
		a := &EmailAddress{ID: fmt.Sprintf("ea%d", s.nextID), Email: email}
		s.emailAddrs[acc] = append(s.emailAddrs[acc], a)
		ok(w, emailAddressJSON(a), nil)
	case len(parts) == 6 && r.Method == http.MethodDelete:
		for i, a := range s.emailAddrs[acc] {
			if a.ID == parts[5] {
				s.emailAddrs[acc] = append(s.emailAddrs[acc][:i], s.emailAddrs[acc][i+1:]...)
				ok(w, emailAddressJSON(a), nil)
				return
			}
		}
		Fail(w, http.StatusNotFound, 2020, "Destination address not found")
	default:
		notFound(w, r.URL.Path)
	}
}
