package cftest

import (
	"fmt"
	"net/http"
)

func (s *Server) rulesetsRoute(w http.ResponseWriter, r *http.Request, tok *Token, z *Zone, parts []string, body map[string]any) {
	write := r.Method != http.MethodGet
	if !tok.has(PermZoneRulesRead, z.Account) && !(write && tok.has(PermZoneRulesEdit, z.Account)) {
		forbidden(w)
		return
	}
	phase := r.URL.Query().Get("phase")
	if len(parts) == 3 && r.Method == http.MethodGet {
		var out []map[string]any
		for _, x := range s.rulesets[z.ID] {
			if phase == "" || x["phase"] == phase {
				out = append(out, copyMap(x))
			}
		}
		ok(w, out, nil)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodPost {
		id := fmt.Sprintf("rs-%d", s.nextID)
		s.nextID++
		rs := copyMap(body)
		rs["id"] = id
		rs["kind"] = "zone"
		rs["rules"] = bodyRules(body)
		s.rulesets[z.ID] = append(s.rulesets[z.ID], rs)
		ok(w, rs, nil)
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
		}
	}
	if rs == nil {
		notFound(w, r.URL.Path)
		return
	}
	if len(parts) == 4 {
		if r.Method == http.MethodPut {
			for k, v := range body {
				rs[k] = v
			}
			rs["id"] = parts[3]
			ok(w, rs, nil)
			return
		}
		notFound(w, r.URL.Path)
		return
	}
	if len(parts) == 6 && parts[4] == "rules" {
		rules, _ := rs["rules"].([]any)
		for i, x := range rules {
			m, _ := x.(map[string]any)
			if m["id"] == parts[5] {
				if r.Method == http.MethodPut {
					for k, v := range body {
						m[k] = v
					}
					rules[i] = m
					ok(w, m, nil)
					return
				}
				if r.Method == http.MethodDelete {
					rules = append(rules[:i], rules[i+1:]...)
					rs["rules"] = rules
					ok(w, map[string]any{"id": parts[5]}, nil)
					return
				}
			}
		}
	}
	notFound(w, r.URL.Path)
}
func bodyRules(body map[string]any) []any {
	if x, ok := body["rules"].([]any); ok {
		return x
	}
	return []any{}
}
