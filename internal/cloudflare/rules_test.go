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
	// Unsupported writable field "logging" must make the rule read-only.
	if _, reason := ParseZoneRuleConfig(ZoneRuleURLRewrite, r); reason == "" || !strings.Contains(reason, "logging") {
		t.Fatalf("expected logging to make rule read-only, got reason=%q", reason)
	}
}

func TestZoneRuleDistinguishesMetadataFromUnsupportedFields(t *testing.T) {
	var r ZoneRule
	raw := `{"id":"r1","version":"2","ref":"ref1","last_updated":"2026-11-06T00:00:00Z","categories":["custom"],"expression":"http.host eq \"a.com\"","action":"redirect","action_parameters":{"from_value":{"target_url":{"value":"https://b.com"},"status_code":301,"preserve_query_string":true}},"enabled":true}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	cfg, reason := ParseZoneRuleConfig(ZoneRuleSingleRedirect, r)
	if reason != "" || cfg == nil || cfg.SingleRedirect == nil {
		t.Fatalf("metadata made rule read-only: cfg=%+v reason=%q", cfg, reason)
	}
	if cfg.SingleRedirect.StatusCode != 301 || !cfg.SingleRedirect.PreserveQueryString || cfg.SingleRedirect.TargetValue != "https://b.com" {
		t.Fatalf("parsed redirect = %+v", cfg.SingleRedirect)
	}
}

func TestZoneRuleAllCategoriesRoundTrip(t *testing.T) {
	port := 8443
	host := "origin.internal"
	sni := "sni.internal"
	dns := "backend.internal"

	cases := []struct {
		name string
		cat  ZoneRuleCategory
		in   ZoneRuleInput
	}{
		{
			name: "single redirect static",
			cat:  ZoneRuleSingleRedirect,
			in: ZoneRuleInput{
				Description: "Redirect old",
				Expression:  `http.host eq "old.example.com"`,
				Enabled:     true,
				Config: ZoneRuleConfig{
					SingleRedirect: &SingleRedirectConfig{
						TargetKind: RedirectTargetStatic, TargetValue: "https://new.example.com/home",
						StatusCode: 308, PreserveQueryString: true,
					},
				},
			},
		},
		{
			name: "single redirect expression",
			cat:  ZoneRuleSingleRedirect,
			in: ZoneRuleInput{
				Description: "Dynamic redirect",
				Expression:  "true",
				Enabled:     false,
				Config: ZoneRuleConfig{
					SingleRedirect: &SingleRedirectConfig{
						TargetKind: RedirectTargetExpression, TargetValue: `concat("https://new.example.com", http.request.uri.path)`,
						StatusCode: 302, PreserveQueryString: false,
					},
				},
			},
		},
		{
			name: "url rewrite path and query",
			cat:  ZoneRuleURLRewrite,
			in: ZoneRuleInput{
				Description: "Rewrite API",
				Expression:  `starts_with(http.request.uri.path, "/v1/")`,
				Enabled:     true,
				Config: ZoneRuleConfig{
					URLRewrite: &URLRewriteConfig{
						PathMode: RewritePartExpression, PathValue: `concat("/api", http.request.uri.path)`,
						QueryMode: RewritePartStatic, QueryValue: "source=edge",
					},
				},
			},
		},
		{
			name: "request header transform",
			cat:  ZoneRuleRequestHeader,
			in: ZoneRuleInput{
				Description: "Set request headers",
				Expression:  `http.host eq "api.example.com"`,
				Enabled:     true,
				Config: ZoneRuleConfig{
					RequestHeader: &HeaderTransformConfig{
						Headers: []HeaderModification{
							{Name: "X-Country", Operation: HeaderOpSetExpression, Value: "ip.src.country"},
							{Name: "X-Remove", Operation: HeaderOpRemove},
							{Name: "X-Token", Operation: HeaderOpSetStatic, Value: "super-secret-value"},
						},
					},
				},
			},
		},
		{
			name: "response header transform",
			cat:  ZoneRuleResponseHeader,
			in: ZoneRuleInput{
				Description: "Set response headers",
				Expression:  "true",
				Enabled:     true,
				Config: ZoneRuleConfig{
					ResponseHeader: &HeaderTransformConfig{
						Headers: []HeaderModification{
							{Name: "Cache-Control", Operation: HeaderOpSetStatic, Value: "no-store"},
							{Name: "Link", Operation: HeaderOpAddStatic, Value: "</app.css>; rel=preload"},
							{Name: "Server", Operation: HeaderOpRemove},
							{Name: "X-Ray", Operation: HeaderOpAddExpression, Value: "http.request.id"},
						},
					},
				},
			},
		},
		{
			name: "origin rule",
			cat:  ZoneRuleOrigin,
			in: ZoneRuleInput{
				Description: "Route to internal",
				Expression:  `http.host eq "app.example.com"`,
				Enabled:     true,
				Config: ZoneRuleConfig{
					Origin: &OriginRuleConfig{
						DestinationPort: &port, HostHeader: &host, SNI: &sni, DNSRecord: &dns,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built, err := BuildZoneRule(tc.cat, tc.in, nil)
			if err != nil {
				t.Fatalf("BuildZoneRule: %v", err)
			}
			raw, err := json.Marshal(built)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var decoded ZoneRule
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			cfg, reason := ParseZoneRuleConfig(tc.cat, decoded)
			if reason != "" || cfg == nil {
				t.Fatalf("ParseZoneRuleConfig failed: reason=%q", reason)
			}
			rebuilt, err := BuildZoneRule(tc.cat, ZoneRuleInput{
				Description: decoded.Description,
				Expression:  decoded.Expression,
				Enabled:     decoded.IsEnabled(),
				Config:      *cfg,
			}, &decoded)
			if err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			if !ZoneRulesEqual(tc.cat, decoded, rebuilt) {
				t.Fatalf("round-trip mismatch:\n got=%s\nwant=%s", rebuilt.ActionParameters, decoded.ActionParameters)
			}
		})
	}
}

func TestZoneRuleValidationAndUnsupportedDetection(t *testing.T) {
	// Request header cannot use add.
	err := ValidateZoneRuleInput(ZoneRuleRequestHeader, ZoneRuleInput{
		Expression: "true",
		Config: ZoneRuleConfig{
			RequestHeader: &HeaderTransformConfig{
				Headers: []HeaderModification{{Name: "X-Test", Operation: HeaderOpAddStatic, Value: "secret-val"}},
			},
		},
	})
	if err == nil {
		t.Fatal("expected request header add to be rejected")
	}
	if strings.Contains(err.Error(), "secret-val") {
		t.Fatalf("validation error leaked header value: %v", err)
	}

	// Single Redirect with from_list must be read-only.
	fromList := ZoneRule{
		ID:               "r1",
		Expression:       "true",
		Action:           "redirect",
		ActionParameters: json.RawMessage(`{"from_list":{"name":"bulk"}}`),
	}
	if _, reason := ParseZoneRuleConfig(ZoneRuleSingleRedirect, fromList); reason == "" {
		t.Fatal("expected from_list redirect to be read-only")
	}

	// Origin rule with unknown extra action parameter must be read-only.
	extraOrigin := ZoneRule{
		ID:               "r2",
		Expression:       "true",
		Action:           "route",
		ActionParameters: json.RawMessage(`{"host_header":"a.com","edge_ttl":{"mode":"respect_origin"}}`),
	}
	if _, reason := ParseZoneRuleConfig(ZoneRuleOrigin, extraOrigin); reason == "" {
		t.Fatal("expected origin rule with extra field to be read-only")
	}
}

func TestReorderZoneRulesRejectsPartialAndDuplicateLists(t *testing.T) {
	rs := ZoneRuleset{ID: "rs", Phase: ZoneRuleURLRewrite, Rules: []ZoneRule{{ID: "a"}, {ID: "b"}}}
	if rs.Rule("a") == nil {
		t.Fatal("rule lookup failed")
	}
	for _, in := range [][]ZoneRule{{{ID: "a"}}, {{ID: "a"}, {ID: "a"}}} {
		if _, err := (&Client{}).ReorderZoneRules(nil, "z", rs, in); err == nil {
			t.Fatalf("accepted unsafe ordering: %#v", in)
		}
	}
}
