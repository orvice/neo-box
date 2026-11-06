package cloudflare

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestZoneRulePreservesUnknownConfiguration(t *testing.T) {
	var r ZoneRule
	if err := json.Unmarshal([]byte(`{"id":"r1","expression":"true","action":"rewrite","action_parameters":{"uri":{"path":{"value":"/new"}}},"enabled":true,"logging":{"enabled":true}}`), &r); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"logging":{"enabled":true}`) || !strings.Contains(got, `"action_parameters"`) {
		t.Fatalf("unknown rule configuration was lost: %s", got)
	}
}

func TestReorderZoneRulesRejectsPartialAndDuplicateLists(t *testing.T) {
	rs := ZoneRuleset{ID: "rs", Phase: ZoneRuleURLRewrite, Rules: []ZoneRule{{ID: "a"}, {ID: "b"}}}
	if rs.Rule("a") == nil {
		t.Fatal("rule lookup failed")
	}
	if len(SortRules(rs.Rules)) != 2 {
		t.Fatal("sort lost rules")
	}
	for _, in := range [][]ZoneRule{{{ID: "a"}}, {{ID: "a"}, {ID: "a"}}} {
		if _, err := (&Client{}).ReorderZoneRules(nil, "z", rs, in); err == nil {
			t.Fatalf("accepted unsafe ordering: %#v", in)
		}
	}
}
