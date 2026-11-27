package application

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/structpb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	catRedirect   = neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_SINGLE_REDIRECT
	catURLRewrite = neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_URL_REWRITE
	catReqHeader  = neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_REQUEST_HEADER
	catRespHeader = neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_RESPONSE_HEADER
	catOrigin     = neoboxv1.CloudflareZoneRuleCategory_CLOUDFLARE_ZONE_RULE_CATEGORY_ORIGIN
)

type zoneRulesAPI struct {
	t    *testing.T
	f    *cfFixture
	conn string
	zone string
}

func (z zoneRulesAPI) getAll() []*neoboxv1.CloudflareZoneRules {
	z.t.Helper()
	resp, err := z.f.srv.GetCloudflareZoneRules(asUser("u1"), &neoboxv1.GetCloudflareZoneRulesRequest{
		ConnectionId: z.conn, ZoneId: z.zone,
	})
	if err != nil {
		z.t.Fatalf("GetCloudflareZoneRules: %v", err)
	}
	return resp.GetCategories()
}

func (z zoneRulesAPI) getCategory(cat neoboxv1.CloudflareZoneRuleCategory) *neoboxv1.CloudflareZoneRules {
	z.t.Helper()
	resp, err := z.f.srv.GetCloudflareZoneRules(asUser("u1"), &neoboxv1.GetCloudflareZoneRulesRequest{
		ConnectionId: z.conn, ZoneId: z.zone, Category: cat,
	})
	if err != nil {
		z.t.Fatalf("GetCloudflareZoneRules(%v): %v", cat, err)
	}
	if len(resp.GetCategories()) != 1 {
		z.t.Fatalf("expected 1 category, got %d", len(resp.GetCategories()))
	}
	return resp.GetCategories()[0]
}

func (z zoneRulesAPI) create(cat neoboxv1.CloudflareZoneRuleCategory, snapshot string, rule *neoboxv1.CloudflareZoneRule) (*neoboxv1.CreateCloudflareZoneRuleResponse, error) {
	return z.f.srv.CreateCloudflareZoneRule(asUser("u1"), &neoboxv1.CreateCloudflareZoneRuleRequest{
		ConnectionId: z.conn, ZoneId: z.zone, Category: cat, Snapshot: snapshot, Rule: rule,
	})
}

func (z zoneRulesAPI) update(cat neoboxv1.CloudflareZoneRuleCategory, ruleID, snapshot string, rule *neoboxv1.CloudflareZoneRule) (*neoboxv1.UpdateCloudflareZoneRuleResponse, error) {
	return z.f.srv.UpdateCloudflareZoneRule(asUser("u1"), &neoboxv1.UpdateCloudflareZoneRuleRequest{
		ConnectionId: z.conn, ZoneId: z.zone, Category: cat, RuleId: ruleID, Snapshot: snapshot, Rule: rule,
	})
}

func (z zoneRulesAPI) remove(cat neoboxv1.CloudflareZoneRuleCategory, ruleID, snapshot string) (*neoboxv1.DeleteCloudflareZoneRuleResponse, error) {
	return z.f.srv.DeleteCloudflareZoneRule(asUser("u1"), &neoboxv1.DeleteCloudflareZoneRuleRequest{
		ConnectionId: z.conn, ZoneId: z.zone, Category: cat, RuleId: ruleID, Snapshot: snapshot,
	})
}

func (z zoneRulesAPI) reorder(cat neoboxv1.CloudflareZoneRuleCategory, snapshot string, ids []string) (*neoboxv1.ReorderCloudflareZoneRulesResponse, error) {
	return z.f.srv.ReorderCloudflareZoneRules(asUser("u1"), &neoboxv1.ReorderCloudflareZoneRulesRequest{
		ConnectionId: z.conn, ZoneId: z.zone, Category: cat, Snapshot: snapshot, RuleIds: ids,
	})
}

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(n int32) *int32 { return &n }
func strPtr(s string) *string { return &s }

func redirectRuleProto(desc, expr, target string, status int32, preserve bool, enabled bool) *neoboxv1.CloudflareZoneRule {
	return &neoboxv1.CloudflareZoneRule{
		Description: desc,
		Expression:  expr,
		Enabled:     boolPtr(enabled),
		Config: &neoboxv1.CloudflareZoneRule_SingleRedirect{
			SingleRedirect: &neoboxv1.CloudflareSingleRedirectConfig{
				TargetKind:          neoboxv1.CloudflareRedirectTargetKind_CLOUDFLARE_REDIRECT_TARGET_KIND_STATIC,
				TargetValue:         target,
				StatusCode:          status,
				PreserveQueryString: preserve,
			},
		},
	}
}

func TestCloudflareZoneRulesIndependentCategoriesAndAbsentRuleset(t *testing.T) {
	f := newCFFixture(t)
	// Token with only Origin Read + Zone Read: Origin should load as empty/absent
	// while the other 4 categories report permission_denied independently.
	f.cf.AddToken("origin-only", &cftest.Token{
		Accounts: []string{cfAccA},
		Perms:    []string{cftest.PermZoneRead, cftest.PermOriginRead},
	})
	conn := f.connect(t, "u1", cfAccA, "origin-only")
	api := zoneRulesAPI{t, f, conn, "za1"}

	cats := api.getAll()
	if len(cats) != 5 {
		t.Fatalf("got %d categories, want 5", len(cats))
	}
	byCat := map[neoboxv1.CloudflareZoneRuleCategory]*neoboxv1.CloudflareZoneRules{}
	for _, c := range cats {
		byCat[c.GetCategory()] = c
	}
	origin := byCat[catOrigin]
	if origin.GetIssue() != "" || !origin.GetRulesetMissing() || origin.GetSnapshot() != cloudflare.AbsentRulesetSnapshot || len(origin.GetRules()) != 0 {
		t.Fatalf("origin category = %+v, want confirmed absent ruleset", origin)
	}
	for _, deniedCat := range []neoboxv1.CloudflareZoneRuleCategory{catRedirect, catURLRewrite, catReqHeader, catRespHeader} {
		c := byCat[deniedCat]
		if c.GetIssue() == "" || c.GetIssueKind() != neoboxv1.CloudflareZoneRulesErrorKind_CLOUDFLARE_ZONE_RULES_ERROR_KIND_PERMISSION_DENIED || c.GetRulesetMissing() {
			t.Fatalf("category %v = %+v, want permission_denied issue (not empty)", deniedCat, c)
		}
	}

	// Ownership: another account's Zone and another user's Connection are unreachable.
	_, err := f.srv.GetCloudflareZoneRules(asUser("u1"), &neoboxv1.GetCloudflareZoneRulesRequest{ConnectionId: conn, ZoneId: "zb1"})
	wantCode(t, "other account zone", err, connect.CodeNotFound)
	_, err = f.srv.GetCloudflareZoneRules(asUser("u2"), &neoboxv1.GetCloudflareZoneRulesRequest{ConnectionId: conn, ZoneId: "za1"})
	wantCode(t, "other user connection", err, connect.CodeNotFound)
}

func TestCloudflareZoneRulesFullLifecycleAllFiveCategories(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	api := zoneRulesAPI{t, f, conn, "za1"}

	// 1. Single Redirects: create first rule when ruleset is absent (initializes ruleset),
	// then append a disabled rule, toggle enable/disable, no-op update, edit, delete.
	initial := api.getCategory(catRedirect)
	if !initial.GetRulesetMissing() || initial.GetSnapshot() != cloudflare.AbsentRulesetSnapshot {
		t.Fatalf("initial redirect = %+v", initial)
	}
	r1, err := api.create(catRedirect, initial.GetSnapshot(), redirectRuleProto(
		"Redirect www", `http.host eq "www.alpha.com"`, "https://alpha.com", 301, true, true,
	))
	if err != nil || !r1.GetResult().GetChanged() || r1.GetRule().GetId() == "" ||
		r1.GetResult().GetOperation().GetAction() != "create_http_rule" ||
		r1.GetResult().GetOperation().GetKind() != neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_ZONE_RULES {
		t.Fatalf("create first redirect = %+v, %v", r1, err)
	}

	// Append a second rule submitted disabled.
	after1 := api.getCategory(catRedirect)
	if after1.GetRulesetMissing() || len(after1.GetRules()) != 1 {
		t.Fatalf("after first create = %+v", after1)
	}
	r2, err := api.create(catRedirect, after1.GetSnapshot(), redirectRuleProto(
		"Disabled redirect", `http.host eq "old.alpha.com"`, "https://alpha.com/new", 308, false, false,
	))
	if err != nil || r2.GetRule().GetEnabled() {
		t.Fatalf("create disabled redirect = %+v, %v", r2, err)
	}

	// Enable rule 2 -> logged as enable_http_rule.
	after2 := api.getCategory(catRedirect)
	upd, err := api.update(catRedirect, r2.GetRule().GetId(), after2.GetSnapshot(), redirectRuleProto(
		"Disabled redirect", `http.host eq "old.alpha.com"`, "https://alpha.com/new", 308, false, true,
	))
	if err != nil || !upd.GetResult().GetChanged() || upd.GetResult().GetOperation().GetAction() != "enable_http_rule" {
		t.Fatalf("enable rule = %+v, %v", upd, err)
	}

	// No-op update -> no write, no operation logged.
	after3 := api.getCategory(catRedirect)
	writesBefore := len(f.cf.Writes())
	opsBefore := len(resourceOps(t, f, conn))
	noop, err := api.update(catRedirect, r2.GetRule().GetId(), after3.GetSnapshot(), redirectRuleProto(
		"Disabled redirect", `http.host eq "old.alpha.com"`, "https://alpha.com/new", 308, false, true,
	))
	if err != nil || noop.GetResult().GetChanged() || len(f.cf.Writes()) != writesBefore || len(resourceOps(t, f, conn)) != opsBefore {
		t.Fatalf("no-op update wrote or logged: res=%+v err=%v", noop, err)
	}

	// 2. URL Rewrite Rules
	ur, err := api.create(catURLRewrite, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Rewrite docs",
		Expression:  `starts_with(http.request.uri.path, "/docs/")`,
		Enabled:     boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_UrlRewrite{
			UrlRewrite: &neoboxv1.CloudflareURLRewriteConfig{
				PathMode:   neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_EXPRESSION,
				PathValue:  `concat("/v2", http.request.uri.path)`,
				QueryMode:  neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_STATIC,
				QueryValue: "src=web",
			},
		},
	})
	if err != nil || ur.GetRule().GetUrlRewrite().GetQueryValue() != "src=web" {
		t.Fatalf("create url rewrite = %+v, %v", ur, err)
	}

	// 3. Request Header Transform + sensitive value redaction check
	const secretHeaderVal = "Bearer super-secret-api-token-999"
	rqh, err := api.create(catReqHeader, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Inject origin auth",
		Expression:  `http.host eq "api.alpha.com"`,
		Enabled:     boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_RequestHeader{
			RequestHeader: &neoboxv1.CloudflareHeaderTransformConfig{
				Headers: []*neoboxv1.CloudflareHeaderModification{
					{Name: "Authorization", Operation: neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_SET_STATIC, Value: secretHeaderVal},
					{Name: "X-Debug", Operation: neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_REMOVE},
				},
			},
		},
	})
	if err != nil || len(rqh.GetRule().GetRequestHeader().GetHeaders()) != 2 {
		t.Fatalf("create request header = %+v, %v", rqh, err)
	}

	// 4. Response Header Transform (supports add)
	rsh, err := api.create(catRespHeader, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Add security header",
		Expression:  "true",
		Enabled:     boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_ResponseHeader{
			ResponseHeader: &neoboxv1.CloudflareHeaderTransformConfig{
				Headers: []*neoboxv1.CloudflareHeaderModification{
					{Name: "X-Frame-Options", Operation: neoboxv1.CloudflareHeaderOperation_CLOUDFLARE_HEADER_OPERATION_ADD_STATIC, Value: "DENY"},
				},
			},
		},
	})
	if err != nil || len(rsh.GetRule().GetResponseHeader().GetHeaders()) != 1 {
		t.Fatalf("create response header = %+v, %v", rsh, err)
	}

	// 5. Origin Rules: saving origin DNS override NEVER mutates DNS records.
	orig, err := api.create(catOrigin, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Route to internal",
		Expression:  `http.host eq "app.alpha.com"`,
		Enabled:     boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_Origin{
			Origin: &neoboxv1.CloudflareOriginRuleConfig{
				DestinationPort: int32Ptr(8443),
				HostHeader:      strPtr("internal.alpha.com"),
				Sni:             strPtr("sni.alpha.com"),
				DnsRecord:       strPtr("origin.alpha.com"),
			},
		},
	})
	if err != nil || orig.GetRule().GetOrigin().GetDestinationPort() != 8443 || orig.GetRule().GetOrigin().GetDnsRecord() != "origin.alpha.com" {
		t.Fatalf("create origin rule = %+v, %v", orig, err)
	}
	for _, w := range f.cf.Writes() {
		if strings.Contains(w.Path, "/dns_records") {
			t.Fatalf("origin rule mutated DNS records: %+v", w)
		}
	}

	// Delete rule 1 in Single Redirects.
	delSnap := api.getCategory(catRedirect).GetSnapshot()
	delRes, err := api.remove(catRedirect, r1.GetRule().GetId(), delSnap)
	if err != nil || !delRes.GetResult().GetChanged() || delRes.GetResult().GetOperation().GetAction() != "delete_http_rule" {
		t.Fatalf("delete = %+v, %v", delRes, err)
	}

	// Verify operation log filtering by KIND_ZONE_RULES works AND never contains the secret header value!
	logResp, err := f.srv.ListCloudflareResourceOperations(asUser("u1"), &neoboxv1.ListCloudflareResourceOperationsRequest{
		ConnectionId: conn,
		Kind:         neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_ZONE_RULES,
		Resource:     "alpha.com",
	})
	if err != nil || len(logResp.GetOperations()) < 7 {
		t.Fatalf("ListCloudflareResourceOperations(ZONE_RULES) = %d, %v", len(logResp.GetOperations()), err)
	}
	rawOps, _ := json.Marshal(resourceOps(t, f, conn))
	if strings.Contains(string(rawOps), secretHeaderVal) {
		t.Fatalf("operation log leaked sensitive header value: %s", rawOps)
	}
}

func TestCloudflareZoneRulesReadOnlyProtectionAndBoundedReorder(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	api := zoneRulesAPI{t, f, conn, "za1"}

	// Seed a Single Redirects ruleset created externally with:
	// [r1 (supported), r2 (supported), r3 (READ-ONLY: from_list + logging), r4 (supported), r5 (supported)]
	f.cf.SetZoneRuleset("za1", map[string]any{
		"id":      "rs-redir",
		"phase":   string(cloudflare.ZoneRuleSingleRedirect),
		"kind":    "zone",
		"version": "5",
		"rules": []any{
			map[string]any{
				"id": "r1", "description": "First", "expression": `http.host eq "a.alpha.com"`, "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_value": map[string]any{"target_url": map[string]any{"value": "https://alpha.com/1"}, "status_code": 301}},
			},
			map[string]any{
				"id": "r2", "description": "Second", "expression": `http.host eq "b.alpha.com"`, "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_value": map[string]any{"target_url": map[string]any{"value": "https://alpha.com/2"}, "status_code": 302}},
			},
			map[string]any{
				"id": "r3", "description": "Unsupported bulk", "expression": "true", "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_list": map[string]any{"name": "legacy_list"}},
				"logging":           map[string]any{"enabled": true},
			},
			map[string]any{
				"id": "r4", "description": "Fourth", "expression": `http.host eq "d.alpha.com"`, "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_value": map[string]any{"target_url": map[string]any{"value": "https://alpha.com/4"}, "status_code": 307}},
			},
			map[string]any{
				"id": "r5", "description": "Fifth", "expression": `http.host eq "e.alpha.com"`, "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_value": map[string]any{"target_url": map[string]any{"value": "https://alpha.com/5"}, "status_code": 308}},
			},
		},
	})

	cat := api.getCategory(catRedirect)
	if len(cat.GetRules()) != 5 {
		t.Fatalf("rules = %d, want 5", len(cat.GetRules()))
	}
	roRule := cat.GetRules()[2]
	if !roRule.GetReadOnly() || roRule.GetReadOnlyReason() == "" || !strings.Contains(roRule.GetRawConfigJson(), "legacy_list") {
		t.Fatalf("r3 read-only metadata = %+v", roRule)
	}
	if cat.GetRules()[0].GetReadOnly() || cat.GetRules()[3].GetReadOnly() {
		t.Fatalf("supported rules marked read-only")
	}

	writesBefore := len(f.cf.Writes())
	// Refuses update, delete, moving r3, or moving r4 across r3, or swapping r1 & r4 across r3.
	_, err := api.update(catRedirect, "r3", cat.GetSnapshot(), redirectRuleProto("try", "true", "https://x.com", 301, false, false))
	wantCode(t, "edit read-only", err, connect.CodeFailedPrecondition)
	_, err = api.remove(catRedirect, "r3", cat.GetSnapshot())
	wantCode(t, "delete read-only", err, connect.CodeFailedPrecondition)
	_, err = api.reorder(catRedirect, cat.GetSnapshot(), []string{"r1", "r3", "r2", "r4", "r5"})
	wantCode(t, "move read-only directly", err, connect.CodeFailedPrecondition)
	_, err = api.reorder(catRedirect, cat.GetSnapshot(), []string{"r1", "r2", "r4", "r3", "r5"})
	wantCode(t, "move supported across read-only", err, connect.CodeFailedPrecondition)
	_, err = api.reorder(catRedirect, cat.GetSnapshot(), []string{"r4", "r2", "r3", "r1", "r5"})
	wantCode(t, "swap supported across read-only", err, connect.CodeFailedPrecondition)
	if len(f.cf.Writes()) != writesBefore {
		t.Fatalf("disallowed read-only operations wrote to Cloudflare: %+v", f.cf.Writes()[writesBefore:])
	}

	// Reordering within intervening groups ([r2, r1] before r3, [r5, r4] after r3) IS allowed
	// and preserves r3's unknown fields intact!
	res, err := api.reorder(catRedirect, cat.GetSnapshot(), []string{"r2", "r1", "r3", "r5", "r4"})
	if err != nil || !res.GetResult().GetChanged() || res.GetResult().GetOperation().GetAction() != "reorder_http_rules" {
		t.Fatalf("valid bounded reorder = %+v, %v", res, err)
	}
	live := f.cf.ZoneRuleset("za1", string(cloudflare.ZoneRuleSingleRedirect))
	liveRaw, _ := json.Marshal(live)
	if !strings.Contains(string(liveRaw), `"legacy_list"`) || !strings.Contains(string(liveRaw), `"logging":{"enabled":true}`) {
		t.Fatalf("read-only rule unknown config lost after reorder: %s", liveRaw)
	}
	after := api.getCategory(catRedirect)
	gotIDs := make([]string, 0, 5)
	for _, r := range after.GetRules() {
		gotIDs = append(gotIDs, r.GetId())
	}
	if strings.Join(gotIDs, ",") != "r2,r1,r3,r5,r4" {
		t.Fatalf("reordered ids = %v", gotIDs)
	}

	// No-op reorder sends nothing.
	writesBefore = len(f.cf.Writes())
	noop, err := api.reorder(catRedirect, after.GetSnapshot(), []string{"r2", "r1", "r3", "r5", "r4"})
	if err != nil || noop.GetResult().GetChanged() || len(f.cf.Writes()) != writesBefore {
		t.Fatalf("no-op reorder wrote: %+v, %v", noop, err)
	}
}

func TestCloudflareZoneRulesConflictLoggingAndErrors(t *testing.T) {
	f := newCFFixture(t)
	conn := f.connect(t, "u1", cfAccA, "full")
	api := zoneRulesAPI{t, f, conn, "za1"}

	// Create one rule so we have a snapshot.
	created, err := api.create(catRedirect, cloudflare.AbsentRulesetSnapshot, redirectRuleProto(
		"Initial", `http.host eq "a.alpha.com"`, "https://alpha.com", 301, false, true,
	))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	staleSnap := api.getCategory(catRedirect).GetSnapshot()

	// External edit in Cloudflare dashboard changes the ruleset.
	f.cf.SetZoneRuleset("za1", map[string]any{
		"id":      api.getCategory(catRedirect).GetRulesetId(),
		"phase":   string(cloudflare.ZoneRuleSingleRedirect),
		"kind":    "zone",
		"version": "99",
		"rules": []any{
			map[string]any{
				"id": created.GetRule().GetId(), "description": "Edited externally",
				"expression": `http.host eq "a.alpha.com"`, "action": "redirect", "enabled": true,
				"action_parameters": map[string]any{"from_value": map[string]any{"target_url": map[string]any{"value": "https://alpha.com"}, "status_code": 301}},
			},
		},
	})

	// Stale update, delete, create, and reorder all fail with CodeAborted and send NO write.
	writesBefore := len(f.cf.Writes())
	_, err = api.update(catRedirect, created.GetRule().GetId(), staleSnap, redirectRuleProto("Mine", "true", "https://alpha.com", 302, false, true))
	wantCode(t, "stale update", err, connect.CodeAborted)
	_, err = api.remove(catRedirect, created.GetRule().GetId(), staleSnap)
	wantCode(t, "stale delete", err, connect.CodeAborted)
	_, err = api.create(catRedirect, staleSnap, redirectRuleProto("New", "true", "https://alpha.com", 302, false, true))
	wantCode(t, "stale create", err, connect.CodeAborted)
	_, err = api.reorder(catRedirect, staleSnap, []string{created.GetRule().GetId()})
	wantCode(t, "stale reorder", err, connect.CodeAborted)
	if len(f.cf.Writes()) != writesBefore {
		t.Fatalf("stale conflict still sent writes: %+v", f.cf.Writes()[writesBefore:])
	}

	// Reject raw action_parameters without typed config.
	rawParams, _ := structpb.NewStruct(map[string]any{"from_value": map[string]any{}})
	_, err = api.create(catRedirect, "", &neoboxv1.CloudflareZoneRule{
		Description: "Raw", Expression: "true", Action: "redirect", ActionParameters: rawParams,
	})
	wantCode(t, "generic raw json write", err, connect.CodeInvalidArgument)

	// Operation log create failure blocks all upstream writes.
	freshSnap := api.getCategory(catRedirect).GetSnapshot()
	f.ops.FailCreate = true
	writesBefore = len(f.cf.Writes())
	_, err = api.update(catRedirect, created.GetRule().GetId(), freshSnap, redirectRuleProto("Blocked", "true", "https://alpha.com", 302, false, true))
	f.ops.FailCreate = false
	wantCode(t, "log create failure", err, connect.CodeUnavailable)
	if len(f.cf.Writes()) != writesBefore {
		t.Fatalf("write was sent even though logging failed")
	}

	// Outcome log failure still reports confirmed write + separate operation_log_error.
	f.ops.FailFinish = true
	updRes, err := api.update(catRedirect, created.GetRule().GetId(), freshSnap, redirectRuleProto("Logged pending", "true", "https://alpha.com", 302, false, true))
	f.ops.FailFinish = false
	if err != nil || !updRes.GetResult().GetChanged() || updRes.GetResult().GetOperationLogError() == "" {
		t.Fatalf("finish log error handling = %+v, %v", updRes, err)
	}

	// Uncertain outcome on ruleset initialization logs StatusUnknown and is NOT resent.
	f.cf.DropAnswer = func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rulesets")
	}
	writesBefore = len(f.cf.Writes())
	_, err = api.create(catOrigin, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Dropped init", Expression: "true", Enabled: boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_Origin{Origin: &neoboxv1.CloudflareOriginRuleConfig{DestinationPort: int32Ptr(8443)}},
	})
	f.cf.DropAnswer = nil
	wantCode(t, "uncertain ruleset init", err, connect.CodeUnknown)
	if len(f.cf.Writes()) != writesBefore+1 {
		t.Fatalf("uncertain write retried: writes=%d want=%d", len(f.cf.Writes()), writesBefore+1)
	}
	var foundUnknown bool
	for _, op := range resourceOps(t, f, conn) {
		if op.Status == cfrepo.StatusUnknown && op.Action == "create_http_rule" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Fatalf("uncertain init was not logged as unknown")
	}

	// Quota exceeded and plan/entitlement restriction classification.
	f.cf.Intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rulesets") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":20217,"message":"exceeded the maximum number of rules for this phase"}]}`))
			return true
		}
		return false
	}
	_, err = api.create(catURLRewrite, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Quota hit", Expression: "true", Enabled: boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_UrlRewrite{
			UrlRewrite: &neoboxv1.CloudflareURLRewriteConfig{
				PathMode:  neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_STATIC,
				PathValue: "/x",
			},
		},
	})
	wantCode(t, "quota exceeded", err, connect.CodeResourceExhausted)

	f.cf.Intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rulesets") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":20213,"message":"your current plan is not entitled to use this feature"}]}`))
			return true
		}
		return false
	}
	_, err = api.create(catURLRewrite, cloudflare.AbsentRulesetSnapshot, &neoboxv1.CloudflareZoneRule{
		Description: "Plan hit", Expression: "true", Enabled: boolPtr(true),
		Config: &neoboxv1.CloudflareZoneRule_UrlRewrite{
			UrlRewrite: &neoboxv1.CloudflareURLRewriteConfig{
				PathMode:  neoboxv1.CloudflareRewritePartMode_CLOUDFLARE_REWRITE_PART_MODE_STATIC,
				PathValue: "/x",
			},
		},
	})
	f.cf.Intercept = nil
	wantCode(t, "feature restriction", err, connect.CodeFailedPrecondition)
	if !strings.Contains(strings.ToLower(err.Error()), "plan") && !strings.Contains(strings.ToLower(err.Error()), "entitlement") {
		t.Fatalf("feature restriction error did not explain plan/entitlement: %v", err)
	}

	// Revoked token returns FailedPrecondition (never Unauthenticated).
	f.cf.AddToken("full", &cftest.Token{Status: "revoked"})
	_, err = f.srv.GetCloudflareZoneRules(asUser("u1"), &neoboxv1.GetCloudflareZoneRulesRequest{ConnectionId: conn, ZoneId: "za1"})
	wantCode(t, "revoked token", err, connect.CodeFailedPrecondition)
}
