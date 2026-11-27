package cftest

import (
	"fmt"
	"net/http"
	"strconv"
)

func phasePerms(phase string) (readPerm, writePerm string) {
	switch phase {
	case "http_request_dynamic_redirect":
		return PermRedirectRead, PermRedirectEdit
	case "http_request_transform", "http_request_late_transform", "http_response_headers_transform":
		return PermTransformRead, PermTransformEdit
	case "http_request_origin":
		return PermOriginRead, PermOriginEdit
	default:
		return PermZoneRulesRead, PermZoneRulesEdit
	}
}

func (tok *Token) canRulesPhase(account, phase string, write bool) bool {
	if write {
		_, wp := phasePerms(phase)
		return tok.has(PermZoneRulesEdit, account) || tok.has(wp, account)
	}
	rp, wp := phasePerms(phase)
	return tok.has(PermZoneRulesRead, account) || tok.has(PermZoneRulesEdit, account) ||
		tok.has(rp, account) || tok.has(wp, account)
}

// SetZoneRuleset installs or replaces the entrypoint ruleset for a phase in
// zoneID (for seeding test fixtures or simulating external edits).
func (s *Server) SetZoneRuleset(zoneID string, rs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone := copyMap(rs)
	if clone["id"] == nil || clone["id"] == "" {
		s.nextID++
		clone["id"] = fmt.Sprintf("rs-%d", s.nextID)
	}
	if clone["kind"] == nil || clone["kind"] == "" {
		clone["kind"] = "zone"
	}
	if clone["version"] == nil || clone["version"] == "" {
		clone["version"] = "1"
	}
	if clone["rules"] == nil {
		clone["rules"] = []any{}
	}
	phase, _ := clone["phase"].(string)
	list := s.rulesets[zoneID]
	for i, existing := range list {
		if existing["phase"] == phase {
			list[i] = clone
			s.rulesets[zoneID] = list
			return
		}
	}
	s.rulesets[zoneID] = append(list, clone)
}

// ZoneRuleset returns a deep copy of the zone's entrypoint ruleset for phase,
// or nil if absent.
func (s *Server) ZoneRuleset(zoneID, phase string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.rulesets[zoneID] {
		if x["phase"] == phase {
			return copyMap(x)
		}
	}
	return nil
}

func bumpVersion(rs map[string]any) {
	v, _ := strconv.Atoi(fmt.Sprint(rs["version"]))
	rs["version"] = strconv.Itoa(v + 1)
}

func (s *Server) rulesetsRoute(w http.ResponseWriter, r *http.Request, tok *Token, z *Zone, parts []string, body map[string]any) {
	// GET /zones/{id}/rulesets/phases/{phase}/entrypoint
	if len(parts) == 6 && parts[3] == "phases" && parts[5] == "entrypoint" && r.Method == http.MethodGet {
		phase := parts[4]
		if !tok.canRulesPhase(z.Account, phase, false) {
			forbidden(w)
			return
		}
		for _, x := range s.rulesets[z.ID] {
			if x["phase"] == phase {
				ok(w, copyMap(x), nil)
				return
			}
		}
		notFound(w, r.URL.Path)
		return
	}

	// GET /zones/{id}/rulesets
	if len(parts) == 3 && r.Method == http.MethodGet {
		phase := r.URL.Query().Get("phase")
		if phase != "" {
			if !tok.canRulesPhase(z.Account, phase, false) {
				forbidden(w)
				return
			}
		} else if !tok.has(PermZoneRulesRead, z.Account) && !tok.has(PermZoneRulesEdit, z.Account) {
			forbidden(w)
			return
		}
		var out []map[string]any
		for _, x := range s.rulesets[z.ID] {
			if phase == "" || x["phase"] == phase {
				out = append(out, copyMap(x))
			}
		}
		ok(w, out, nil)
		return
	}

	// POST /zones/{id}/rulesets (initialize phase entrypoint ruleset)
	if len(parts) == 3 && r.Method == http.MethodPost {
		phase, _ := body["phase"].(string)
		if !tok.canRulesPhase(z.Account, phase, true) {
			forbidden(w)
			return
		}
		for _, existing := range s.rulesets[z.ID] {
			if existing["phase"] == phase {
				Fail(w, http.StatusConflict, 20010, "a zone entrypoint ruleset for this phase already exists")
				return
			}
		}
		s.nextID++
		id := fmt.Sprintf("rs-%d", s.nextID)
		rs := copyMap(body)
		rs["id"] = id
		if rs["kind"] == nil || rs["kind"] == "" {
			rs["kind"] = "zone"
		}
		if rs["name"] == nil || rs["name"] == "" {
			rs["name"] = "default"
		}
		rs["version"] = "1"
		rs["rules"] = s.normalizeRules(bodyRules(body))
		s.rulesets[z.ID] = append(s.rulesets[z.ID], rs)
		ok(w, copyMap(rs), nil)
		return
	}

	if len(parts) < 4 {
		notFound(w, r.URL.Path)
		return
	}

	var rs map[string]any
	for _, x := range s.rulesets[z.ID] {
		if x["id"] == parts[3] {
			rs = x
			break
		}
	}
	if rs == nil {
		notFound(w, r.URL.Path)
		return
	}
	phase, _ := rs["phase"].(string)
	write := r.Method != http.MethodGet
	if !tok.canRulesPhase(z.Account, phase, write) {
		forbidden(w)
		return
	}

	// GET / PUT /zones/{id}/rulesets/{ruleset_id}
	if len(parts) == 4 {
		if r.Method == http.MethodGet {
			ok(w, copyMap(rs), nil)
			return
		}
		if r.Method == http.MethodPut {
			for k, v := range body {
				rs[k] = v
			}
			rs["id"] = parts[3]
			rs["phase"] = phase
			rs["rules"] = s.normalizeRules(bodyRules(body))
			bumpVersion(rs)
			ok(w, copyMap(rs), nil)
			return
		}
		notFound(w, r.URL.Path)
		return
	}

	// POST /zones/{id}/rulesets/{ruleset_id}/rules (append a rule)
	if len(parts) == 5 && parts[4] == "rules" && r.Method == http.MethodPost {
		rule := copyMap(body)
		if rule["id"] == nil || rule["id"] == "" {
			s.nextID++
			rule["id"] = fmt.Sprintf("rule-%d", s.nextID)
		}
		if rule["ref"] == nil || rule["ref"] == "" {
			rule["ref"] = rule["id"]
		}
		rule["version"] = "1"
		rules, _ := rs["rules"].([]any)
		rules = append(rules, rule)
		rs["rules"] = rules
		bumpVersion(rs)
		ok(w, copyMap(rs), nil)
		return
	}

	// PATCH / PUT / DELETE /zones/{id}/rulesets/{ruleset_id}/rules/{rule_id}
	if len(parts) == 6 && parts[4] == "rules" {
		rules, _ := rs["rules"].([]any)
		for i, x := range rules {
			m, _ := x.(map[string]any)
			if m["id"] == parts[5] {
				if r.Method == http.MethodPatch || r.Method == http.MethodPut {
					updated := copyMap(body)
					updated["id"] = parts[5]
					if updated["ref"] == nil || updated["ref"] == "" {
						if prevRef, ok := m["ref"]; ok {
							updated["ref"] = prevRef
						}
					}
					v, _ := strconv.Atoi(fmt.Sprint(m["version"]))
					updated["version"] = strconv.Itoa(v + 1)
					rules[i] = updated
					rs["rules"] = rules
					bumpVersion(rs)
					ok(w, copyMap(rs), nil)
					return
				}
				if r.Method == http.MethodDelete {
					rules = append(rules[:i], rules[i+1:]...)
					rs["rules"] = rules
					bumpVersion(rs)
					ok(w, copyMap(rs), nil)
					return
				}
			}
		}
		notFound(w, r.URL.Path)
		return
	}
	notFound(w, r.URL.Path)
}

func (s *Server) normalizeRules(rules []any) []any {
	out := make([]any, 0, len(rules))
	for _, x := range rules {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		rule := copyMap(m)
		if rule["id"] == nil || rule["id"] == "" {
			s.nextID++
			rule["id"] = fmt.Sprintf("rule-%d", s.nextID)
		}
		if rule["ref"] == nil || rule["ref"] == "" {
			rule["ref"] = rule["id"]
		}
		if rule["version"] == nil || rule["version"] == "" {
			rule["version"] = "1"
		}
		out = append(out, rule)
	}
	return out
}

func bodyRules(body map[string]any) []any {
	if x, ok := body["rules"].([]any); ok {
		return x
	}
	return []any{}
}
