# Cloudflare Email Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Manage Cloudflare Email Routing from a Zone's page: enable it, manage forwarding rules and the catch-all, and manage the Account's destination addresses, with every change logged before it is sent.

**Architecture:** A new `internal/cloudflare/email.go` client (reads retry, writes never resent), an `internal/cloudflareacct/email.go` Manager part that checks Zone ownership and logs every write through the existing `operate` (new `ResourceKind` `email_routing`, no ent change), new RPCs on `CloudflareService`, and an "Email routing" tab on the Zone page. Spec: `docs/specs/cloudflare-email-routing-v1.md`.

**Tech Stack:** Go (Butterfly, ConnectRPC, `cftest` fake Cloudflare API), protobuf via `buf`, React 19 + TanStack Query + shadcn/ui, Vitest.

## Global Constraints

Copied from the spec; every task includes them.

- In scope: read a Zone's status / rules / catch-all; **enable** Email Routing (Cloudflare writes MX / SPF); create, update (incl. enable / disable) and delete forwarding rules; set the catch-all (forward to a verified address, drop, or off); list, create, delete the Account's destination addresses.
- Out of scope: **disabling** Email Routing (no RPC for it), creating or editing "send to Worker" rules (shown read-only), sending mail, email logs, subaddressing settings.
- Operation log: reuse `cloudflare_resource_operations`, `ResourceKind` `email_routing`; `kind` is a plain string column, so **no ent change and no `make ent`**. Every write goes through `operate`; read-modify-write holds `lockResource`; no-op changes return `noChange()` unlogged.
- Destination addresses are Account-level, rules are Zone-level; both appear in the Zone page's Email routing tab.
- `Verify` is unchanged (Email Routing permissions are optional; a missing one shows in the tab only).
- Errors: missing permission is PermissionDenied, a rejected token FailedPrecondition (never Unauthenticated), an unconfirmed write Unknown.
- Proto: one request and response per RPC (buf STANDARD); enum names without an acronym run; run `make buf` and commit generated Go (`pkg/proto/`) and TypeScript (`front/src/gen/`); never hand-edit generated code.
- Writes retry only 429 and otherwise return `UncertainError`; reads retry 429 / 5xx / network errors.
- Log states never hold secrets (they hold only addresses, names, and flags).
- Frontend uses npm; prettier config in `front/.prettierrc`.

## Amendments to the spec found while planning

Cloudflare's API docs (checked 2026-10-16) add two facts the spec did not have. Task 7 writes them into the spec.

1. Rules have a `source` (`api` or `wrangler`). A `wrangler` rule is owned by a Worker's wrangler config, so it is **read-only** here, like Worker-action rules.
2. Deleting a destination address is checked against the rules and catch-all of **the Zone the delete is made from** (`zone_id` is a request field). Rules in other Zones are not checked, since reading every Zone's rules would be too slow.
3. Destination-address operations are logged with `Resource` = `"destination addresses"`; the tab's log lists all `email_routing` operations of the connection.

Cloudflare facts used (verified in the API docs):

- `GET /zones/{id}/email/routing` → settings `{id, name, enabled, status, support_subaddress}`; `status` is `ready`, `unconfigured`, `misconfigured`, `misconfigured/locked`, `unlocked`.
- `POST /zones/{id}/email/routing/dns` enables (adds and locks MX / SPF), returns settings. `GET .../dns` lists the records needed.
- Rules: `GET|POST /zones/{id}/email/routing/rules`, `GET|PUT|DELETE .../rules/{id}`, `GET|PUT .../rules/catch_all`. A rule is `{id, name, enabled, priority, source, matchers:[{type:"literal",field:"to",value}], actions:[{type:"forward"|"drop"|"worker", value:[...]}]}`. Forward actions need exactly one **verified** destination address. PUT replaces the rule.
- Addresses: `GET|POST /accounts/{id}/email/routing/addresses`, `DELETE .../addresses/{id}`; `verified` is null until verified.
- Permissions: `Email Routing Rules Read/Write` (zone), `Email Routing Addresses Read/Write` (account).

## File Structure

| File | Responsibility |
|---|---|
| `internal/cloudflare/email.go` (new) | Types, client methods, input validation and rule building for Email Routing |
| `internal/cloudflare/email_test.go` (new) | Client + validation tests against `cftest` |
| `internal/cloudflare/cftest/email.go` (new) | Stateful fake Email Routing API |
| `internal/cloudflare/cftest/fake.go` (modify) | State fields, init, two route cases |
| `proto/neobox/v1/cloudflare.proto` (modify) | Messages, enum value, 8 RPCs |
| `internal/repo/cloudflare/repository.go` (modify) | `KindEmailRouting` |
| `internal/cloudflareacct/email.go` (new) | Manager: read view, enable, rule / catch-all / address changes |
| `internal/application/cloudflare_email.go` (new) | RPC handlers and proto conversion |
| `internal/application/cloudflare_manage.go`, `cloudflare_service.go` (modify) | New kind in conversions, new errors in `mapCloudflareErr` |
| `internal/application/cloudflare_email_test.go` (new) | Service-level tests |
| `internal/application/cloudflare_service_test.go` (modify) | Fixture tokens get email permissions |
| `front/src/api/cloudflare.ts` (modify) | Exports, query keys, hooks |
| `front/src/features/cloudflare/email.ts`, `email.test.ts` (new) | Input checks and wording |
| `front/src/features/cloudflare/email-routing-tab.tsx`, `email-rules.tsx`, `email-addresses.tsx` (new) | The tab |
| `front/src/features/cloudflare/zone-page.tsx`, `manage.ts`, `manage.test.ts`, `format.ts` (modify) | Tab, log wording, token permission list |
| `CONTEXT.md`, `docs/adr/0011-...md`, `AGENTS.md`, `README.md`, spec (modify / new) | Docs |

Work on a branch: `git switch -c feat/cloudflare-email-routing`.

---

### Task 1: Email Routing client, validation and fake API

**Files:**
- Create: `internal/cloudflare/email.go`, `internal/cloudflare/cftest/email.go`, `internal/cloudflare/email_test.go`
- Modify: `internal/cloudflare/cftest/fake.go` (struct fields near line 111-128, `New` near line 139, `handle` near line 368, `zoneRoutes` near line 478)

**Interfaces:**
- Consumes: `Client.get/send/eachPage`, `esc`, `invalid` (all exist in package `cloudflare`).
- Produces (used by Tasks 3-4):
  - Constants `EmailActionForward = "forward"`, `EmailActionDrop = "drop"`, `EmailActionWorker = "worker"`, `EmailMatcherLiteral`, `EmailMatcherAll`, `EmailSourceAPI`.
  - Types `EmailRoutingSettings{ID, Name string; Enabled bool; Status string; SupportSubaddress bool}`, `EmailDNSRecord{Type, Name, Content string; Priority *int; TTL int}`, `EmailAction{Type string; Value []string}`, `EmailMatcher{Type, Field, Value string}`, `EmailRule{ID, Name string; Enabled bool; Priority int; Source string; Matchers []EmailMatcher; Actions []EmailAction}`, `EmailAddress{ID, Email string; Created, Verified *time.Time}`, `EmailRuleInput{Name, Address, Action, Target string; Enabled bool}`, `EmailCatchAllInput{Enabled bool; Action, Target string}`.
  - Methods on `EmailRule`: `Address() string`, `ActionType() string`, `Target() string`, `ReadOnlyReason(catchAll bool) string`, `Same(o EmailRule) bool`. On `EmailAddress`: `IsVerified() bool`.
  - Methods on inputs: `EmailRuleInput.Normalized() EmailRuleInput`, `.Rule(base *EmailRule) EmailRule`; `EmailCatchAllInput.Normalized()`, `.Rule(base EmailRule) EmailRule`.
  - Funcs `ValidateEmailAddress(field, addr string) error`, `ValidateEmailRuleInput(zoneName string, in EmailRuleInput) error`, `ValidateEmailCatchAllInput(in EmailCatchAllInput) error`.
  - Client: `GetEmailRouting(ctx, zoneID) (*EmailRoutingSettings, error)`, `EmailRoutingDNS(ctx, zoneID) ([]EmailDNSRecord, error)`, `EnableEmailRouting(ctx, zoneID) (*EmailRoutingSettings, error)`, `ListEmailRules(ctx, zoneID) ([]EmailRule, error)` (literal rules only), `CreateEmailRule(ctx, zoneID, EmailRule) (*EmailRule, error)`, `UpdateEmailRule(ctx, zoneID, id string, EmailRule) (*EmailRule, error)`, `DeleteEmailRule(ctx, zoneID, id string) error`, `GetEmailCatchAll(ctx, zoneID) (*EmailRule, error)`, `UpdateEmailCatchAll(ctx, zoneID string, EmailRule) (*EmailRule, error)`, `ListEmailAddresses(ctx, accountID) ([]EmailAddress, error)`, `CreateEmailAddress(ctx, accountID, email string) (*EmailAddress, error)`, `DeleteEmailAddress(ctx, accountID, id string) error`.
  - Fake: constants `cftest.PermEmailRulesRead/Edit`, `PermEmailAddrRead/Edit`; types `cftest.EmailRule{ID, Name, Source string; Enabled bool; Priority int; Address, Action, Target string}`, `cftest.EmailAddress{ID, Email string; Verified bool}`, `cftest.EmailState{Enabled bool; Rules []EmailRule; CatchAll EmailRule}`; methods `AddEmailAddress(account, *EmailAddress)`, `EmailAddresses(account) []EmailAddress`, `VerifyEmailAddress(account, email)`, `SetEmailEnabled(zoneID, bool)`, `AddEmailRule(zoneID, *EmailRule)`, `EmailRouting(zoneID) EmailState`.

- [ ] **Step 1: Write the failing tests**

Create `internal/cloudflare/email_test.go`:

```go
package cloudflare_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
)

func emailFixture(t *testing.T) (*cftest.Server, *cloudflare.Client) {
	t.Helper()
	f := cftest.New()
	t.Cleanup(f.Close)
	f.AddAccount(accA, "A")
	f.AddToken("tok", &cftest.Token{Accounts: []string{accA}, Perms: []string{
		cftest.PermZoneRead, cftest.PermEmailRulesRead, cftest.PermEmailRulesEdit,
		cftest.PermEmailAddrRead, cftest.PermEmailAddrEdit,
	}})
	f.AddToken("ro", &cftest.Token{Accounts: []string{accA}, Perms: []string{
		cftest.PermZoneRead, cftest.PermEmailRulesRead, cftest.PermEmailAddrRead,
	}})
	f.AddZone(&cftest.Zone{ID: "z1", Name: "example.com", Account: accA})
	f.AddEmailAddress(accA, &cftest.EmailAddress{Email: "me@gmail.com", Verified: true})
	return f, newClient(t, f.URL, "tok")
}

func TestEmailRoutingEnable(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	s, err := c.GetEmailRouting(ctx, "z1")
	if err != nil || s.Enabled {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	recs, err := c.EmailRoutingDNS(ctx, "z1")
	if err != nil || len(recs) == 0 {
		t.Fatalf("dns records = %+v, %v", recs, err)
	}
	var mx bool
	for _, r := range recs {
		mx = mx || (r.Type == "MX" && r.Priority != nil)
	}
	if !mx {
		t.Fatalf("no MX record with a priority in %+v", recs)
	}
	s, err = c.EnableEmailRouting(ctx, "z1")
	if err != nil || !s.Enabled || s.Status != "ready" {
		t.Fatalf("enable = %+v, %v", s, err)
	}
	if !f.EmailRouting("z1").Enabled {
		t.Fatal("the fake was not enabled")
	}
}

func TestEmailRuleLifecycle(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()
	f.SetEmailEnabled("z1", true)

	in := cloudflare.EmailRuleInput{Name: "Info", Address: "info@example.com", Action: "forward", Target: "me@gmail.com", Enabled: true}
	created, err := c.CreateEmailRule(ctx, "z1", in.Rule(nil))
	if err != nil || created.ID == "" || created.Address() != "info@example.com" ||
		created.Target() != "me@gmail.com" || created.ActionType() != "forward" || !created.Enabled {
		t.Fatalf("create = %+v, %v", created, err)
	}
	f.AddEmailRule("z1", &cftest.EmailRule{Name: "hook", Address: "hook@example.com", Action: "worker", Target: "w", Enabled: true})

	rules, err := c.ListEmailRules(ctx, "z1")
	if err != nil || len(rules) != 2 {
		t.Fatalf("list = %+v, %v", rules, err)
	}
	for _, r := range rules {
		reason := r.ReadOnlyReason(false)
		if (r.Address() == "hook@example.com") != (reason != "") {
			t.Fatalf("rule %s read-only reason = %q", r.Address(), reason)
		}
	}

	off := in
	off.Enabled = false
	got, err := c.UpdateEmailRule(ctx, "z1", created.ID, off.Rule(created))
	if err != nil || got.Enabled || got.ID != created.ID || got.Target() != "me@gmail.com" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if !got.Same(off.Rule(created)) || got.Same(in.Rule(created)) {
		t.Fatal("Same does not tell the enabled flag apart")
	}

	bad := in
	bad.Target = "stranger@gmail.com"
	if _, err := c.UpdateEmailRule(ctx, "z1", created.ID, bad.Rule(created)); !cloudflare.IsRejected(err) {
		t.Fatalf("forward to an unverified address: %v", err)
	}

	if err := c.DeleteEmailRule(ctx, "z1", created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rules, _ := c.ListEmailRules(ctx, "z1"); len(rules) != 1 {
		t.Fatalf("after delete = %+v", rules)
	}
}

func TestEmailCatchAll(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	cur, err := c.GetEmailCatchAll(ctx, "z1")
	if err != nil || cur.Enabled || cur.ActionType() != "drop" || cur.ReadOnlyReason(true) != "" {
		t.Fatalf("catch-all = %+v, %v", cur, err)
	}
	in := cloudflare.EmailCatchAllInput{Enabled: true, Action: "forward", Target: "me@gmail.com"}
	got, err := c.UpdateEmailCatchAll(ctx, "z1", in.Rule(*cur))
	if err != nil || !got.Enabled || got.Target() != "me@gmail.com" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if st := f.EmailRouting("z1").CatchAll; !st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all = %+v", st)
	}
	// Off keeps the current action.
	off := cloudflare.EmailCatchAllInput{Enabled: false}.Rule(*got)
	if off.Enabled || off.ActionType() != "forward" || off.Target() != "me@gmail.com" {
		t.Fatalf("off rule = %+v", off)
	}
}

func TestEmailAddresses(t *testing.T) {
	f, c := emailFixture(t)
	ctx := context.Background()

	list, err := c.ListEmailAddresses(ctx, accA)
	if err != nil || len(list) != 1 || !list[0].IsVerified() {
		t.Fatalf("list = %+v, %v", list, err)
	}
	created, err := c.CreateEmailAddress(ctx, accA, "new@gmail.com")
	if err != nil || created.IsVerified() || created.Email != "new@gmail.com" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := c.CreateEmailAddress(ctx, accA, "new@gmail.com"); !cloudflare.IsRejected(err) {
		t.Fatalf("duplicate: %v", err)
	}
	f.VerifyEmailAddress(accA, "new@gmail.com")
	list, _ = c.ListEmailAddresses(ctx, accA)
	if len(list) != 2 || !list[1].IsVerified() {
		t.Fatalf("after verify = %+v", list)
	}
	if err := c.DeleteEmailAddress(ctx, accA, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := f.EmailAddresses(accA); len(got) != 1 {
		t.Fatalf("after delete = %+v", got)
	}
}

func TestEmailPermissions(t *testing.T) {
	f, _ := emailFixture(t)
	ro := newClient(t, f.URL, "ro")
	ctx := context.Background()
	if _, err := ro.GetEmailRouting(ctx, "z1"); err != nil {
		t.Fatalf("read with a read token: %v", err)
	}
	if _, err := ro.EnableEmailRouting(ctx, "z1"); !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("enable with a read token: %v", err)
	}
	if _, err := ro.CreateEmailAddress(ctx, accA, "x@gmail.com"); !cloudflare.IsPermissionDenied(err) {
		t.Fatalf("create address with a read token: %v", err)
	}
}

func TestValidateEmailRuleInput(t *testing.T) {
	ok := cloudflare.EmailRuleInput{Address: "info@example.com", Action: "forward", Target: "me@gmail.com"}
	if err := cloudflare.ValidateEmailRuleInput("example.com", ok); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	if err := cloudflare.ValidateEmailRuleInput("example.com", cloudflare.EmailRuleInput{Address: "info@example.com", Action: "drop"}); err != nil {
		t.Fatalf("drop: %v", err)
	}
	for name, in := range map[string]cloudflare.EmailRuleInput{
		"no address": {Action: "drop"},
		"other zone": {Address: "info@other.com", Action: "drop"},
		"bad target": {Address: "info@example.com", Action: "forward", Target: "nope"},
		"no action":  {Address: "info@example.com"},
		"worker":     {Address: "info@example.com", Action: "worker", Target: "w"},
		"long name":  {Address: "info@example.com", Action: "drop", Name: strings.Repeat("x", 257)},
	} {
		var fe *cloudflare.FieldError
		if err := cloudflare.ValidateEmailRuleInput("example.com", in); !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a FieldError", name, err)
		}
	}
	n := cloudflare.EmailRuleInput{Name: " X ", Address: " Info@Example.COM ", Action: " Forward ", Target: " Me@Gmail.com "}.Normalized()
	if n.Name != "X" || n.Address != "info@example.com" || n.Action != "forward" || n.Target != "me@gmail.com" {
		t.Fatalf("normalized = %+v", n)
	}
	if err := cloudflare.ValidateEmailCatchAllInput(cloudflare.EmailCatchAllInput{Enabled: true, Action: "forward", Target: "x"}); err == nil {
		t.Fatal("catch-all forward to a bad target was accepted")
	}
	if err := cloudflare.ValidateEmailCatchAllInput(cloudflare.EmailCatchAllInput{Enabled: false}); err != nil {
		t.Fatalf("catch-all off: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/cloudflare/ -run 'Email' 2>&1 | head -20`
Expected: build failure, `undefined: cftest.PermEmailRulesRead` and `undefined: cloudflare.EmailRuleInput`.

- [ ] **Step 3: Write the fake API**

Create `internal/cloudflare/cftest/email.go`:

```go
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
```

Edit `internal/cloudflare/cftest/fake.go`:

1. In `type Server struct`, after `routeSeq   int` add:

```go
	emailZones map[string]*emailZone      // zone ID → Email Routing state
	emailAddrs map[string][]*EmailAddress // account → destination addresses
```

2. In `New()`, extend the composite literal's last line to
   `projects: map[string][]*PagesProject{}, tunnels: map[string][]*Tunnel{}, emailZones: map[string]*emailZone{}, emailAddrs: map[string][]*EmailAddress{},`.

3. In `handle`, before the `case len(parts) >= 3 && parts[0] == "accounts" && parts[2] == "workers":` line add:

```go
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "email" && parts[3] == "routing":
		s.emailAccountRoutes(w, r, tok, parts, body)
```

4. In `zoneRoutes`, before `case len(parts) >= 3 && parts[2] == "dns_records":` add:

```go
	case len(parts) >= 4 && parts[2] == "email" && parts[3] == "routing":
		s.emailRoutes(w, r, tok, z, parts[4:], body)
```

- [ ] **Step 4: Write the client**

Create `internal/cloudflare/email.go`:

```go
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
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/cloudflare/... -run 'Email' -v 2>&1 | tail -30`
Expected: all of `TestEmailRoutingEnable`, `TestEmailRuleLifecycle`, `TestEmailCatchAll`, `TestEmailAddresses`, `TestEmailPermissions`, `TestValidateEmailRuleInput` PASS.

Then the whole package: `go test ./internal/cloudflare/... && go vet ./internal/cloudflare/...`
Expected: `ok` for both packages, no vet output.

- [ ] **Step 6: Commit**

```bash
git add internal/cloudflare
git commit -m "feat(cloudflare): Email Routing client, validation and fake API"
```

---

### Task 2: Proto contract, resource kind, generated code

**Files:**
- Modify: `proto/neobox/v1/cloudflare.proto` (service block ends line ~118; enum `CloudflareResourceKind` line ~653; append messages at end of file), `internal/repo/cloudflare/repository.go` (lines 83-86)
- Generated (commit): `pkg/proto/**`, `front/src/gen/**`

**Interfaces:**
- Consumes: existing `CloudflareChangeResult`, `CloudflareReadIssue`, `CloudflareResourceOperation`.
- Produces (used by Tasks 3-6): the Go and TS types below, and `cfrepo.KindEmailRouting ResourceKind = "email_routing"`.

- [ ] **Step 1: Edit the proto**

In the `service CloudflareService` block, after the `ListCloudflareResourceOperations` rpc, add:

```proto

  // GetCloudflareEmailRouting reads a Zone's Email Routing status, its
  // rules and its catch-all. Each part that cannot be read is empty and
  // named in issues. required_dns_records is filled only while Email
  // Routing is not enabled.
  rpc GetCloudflareEmailRouting(GetCloudflareEmailRoutingRequest) returns (GetCloudflareEmailRoutingResponse);
  // ListCloudflareEmailAddresses lists the Account's destination addresses
  // (shared by all its Zones).
  rpc ListCloudflareEmailAddresses(ListCloudflareEmailAddressesRequest) returns (ListCloudflareEmailAddressesResponse);
  // EnableCloudflareEmailRouting enables Email Routing; Cloudflare adds and
  // locks the MX and SPF records. There is no way to disable it here.
  rpc EnableCloudflareEmailRouting(EnableCloudflareEmailRoutingRequest) returns (EnableCloudflareEmailRoutingResponse);
  // CreateCloudflareEmailRule adds a rule for one address. A forward needs
  // a verified destination address (FailedPrecondition otherwise).
  rpc CreateCloudflareEmailRule(CreateCloudflareEmailRuleRequest) returns (CreateCloudflareEmailRuleResponse);
  // UpdateCloudflareEmailRule replaces a rule's fields, which includes
  // enabling and disabling it. A rule managed by a Worker's wrangler
  // configuration, or sending to a Worker, is FailedPrecondition.
  rpc UpdateCloudflareEmailRule(UpdateCloudflareEmailRuleRequest) returns (UpdateCloudflareEmailRuleResponse);
  rpc DeleteCloudflareEmailRule(DeleteCloudflareEmailRuleRequest) returns (DeleteCloudflareEmailRuleResponse);
  // SetCloudflareEmailCatchAll sets the catch-all: off keeps its current
  // action, drop discards, forward needs a verified destination address.
  rpc SetCloudflareEmailCatchAll(SetCloudflareEmailCatchAllRequest) returns (SetCloudflareEmailCatchAllResponse);
  // CreateCloudflareEmailAddress adds a destination address; Cloudflare
  // emails it a link that verifies it.
  rpc CreateCloudflareEmailAddress(CreateCloudflareEmailAddressRequest) returns (CreateCloudflareEmailAddressResponse);
  // DeleteCloudflareEmailAddress deletes a destination address, refused
  // while a rule or the catch-all of zone_id still forwards to it.
  rpc DeleteCloudflareEmailAddress(DeleteCloudflareEmailAddressRequest) returns (DeleteCloudflareEmailAddressResponse);
```

Add `CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING = 3;` to `enum CloudflareResourceKind`. Update the comment above `ListCloudflareResourceOperationsRequest.kind` to `// Only this kind; unspecified lists them all.`

Append at the end of the file:

```proto

// --- Email Routing ---

// CloudflareEmailAction is what a routing rule does with matching mail.
enum CloudflareEmailAction {
  CLOUDFLARE_EMAIL_ACTION_UNSPECIFIED = 0;
  CLOUDFLARE_EMAIL_ACTION_FORWARD = 1;
  CLOUDFLARE_EMAIL_ACTION_DROP = 2;
  // Sends to a Worker. Shown, never created or changed here.
  CLOUDFLARE_EMAIL_ACTION_WORKER = 3;
}

message CloudflareEmailRoutingSettings {
  bool enabled = 1;
  // ready, unconfigured, misconfigured, misconfigured/locked or unlocked.
  string status = 2;
  bool support_subaddress = 3;
}

// CloudflareEmailDNSRecord is a DNS record Email Routing needs.
message CloudflareEmailDNSRecord {
  string type = 1;
  string name = 2;
  string content = 3;
  // MX preference; 0 for other types.
  int32 priority = 4;
}

message CloudflareEmailRule {
  string id = 1;
  string name = 2;
  bool enabled = 3;
  int32 priority = 4;
  // The one address the rule matches.
  string address = 5;
  CloudflareEmailAction action = 6;
  // The forward address, or the Worker's name.
  string target = 7;
  // False when the rule is managed by a Worker's wrangler configuration,
  // sends to a Worker, or does not match a single address.
  bool editable = 8;
  // Why it is not editable.
  string readonly_reason = 9;
}

message CloudflareEmailCatchAll {
  bool enabled = 1;
  CloudflareEmailAction action = 2;
  string target = 3;
  bool editable = 4;
  string readonly_reason = 5;
}

message CloudflareEmailAddress {
  string id = 1;
  string email = 2;
  bool verified = 3;
  google.protobuf.Timestamp created_at = 4;
  google.protobuf.Timestamp verified_at = 5;
}

// CloudflareEmailRuleInput is every field of a rule the user sets. An
// update replaces the rule with it.
message CloudflareEmailRuleInput {
  string name = 1;
  // An address of the Zone, e.g. info@example.com.
  string address = 2 [(validate.rules).string.min_len = 1];
  // Forward or drop.
  CloudflareEmailAction action = 3;
  // The verified destination address of a forward.
  string target = 4;
  bool enabled = 5;
}

message GetCloudflareEmailRoutingRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
}

message GetCloudflareEmailRoutingResponse {
  CloudflareEmailRoutingSettings settings = 1;
  repeated CloudflareEmailRule rules = 2;
  // Unset when it could not be read.
  CloudflareEmailCatchAll catch_all = 3;
  repeated CloudflareEmailDNSRecord required_dns_records = 4;
  repeated CloudflareReadIssue issues = 5;
}

message ListCloudflareEmailAddressesRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
}

message ListCloudflareEmailAddressesResponse {
  repeated CloudflareEmailAddress addresses = 1;
}

message EnableCloudflareEmailRoutingRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
}

message EnableCloudflareEmailRoutingResponse {
  CloudflareChangeResult result = 1;
}

message CreateCloudflareEmailRuleRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
  CloudflareEmailRuleInput rule = 3 [(validate.rules).message.required = true];
}

message CreateCloudflareEmailRuleResponse {
  CloudflareChangeResult result = 1;
}

message UpdateCloudflareEmailRuleRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
  string rule_id = 3 [(validate.rules).string.min_len = 1];
  CloudflareEmailRuleInput rule = 4 [(validate.rules).message.required = true];
}

message UpdateCloudflareEmailRuleResponse {
  CloudflareChangeResult result = 1;
}

message DeleteCloudflareEmailRuleRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
  string rule_id = 3 [(validate.rules).string.min_len = 1];
}

message DeleteCloudflareEmailRuleResponse {
  CloudflareChangeResult result = 1;
}

message SetCloudflareEmailCatchAllRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string zone_id = 2 [(validate.rules).string.min_len = 1];
  // False is off: the catch-all keeps its action and is switched off.
  bool enabled = 3;
  // Forward or drop; ignored when not enabled.
  CloudflareEmailAction action = 4;
  string target = 5;
}

message SetCloudflareEmailCatchAllResponse {
  CloudflareChangeResult result = 1;
}

message CreateCloudflareEmailAddressRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  string email = 2 [(validate.rules).string.min_len = 1];
}

message CreateCloudflareEmailAddressResponse {
  CloudflareChangeResult result = 1;
}

message DeleteCloudflareEmailAddressRequest {
  string connection_id = 1 [(validate.rules).string.min_len = 1];
  // The Zone whose rules and catch-all are checked for use of the address.
  string zone_id = 2 [(validate.rules).string.min_len = 1];
  string address_id = 3 [(validate.rules).string.min_len = 1];
}

message DeleteCloudflareEmailAddressResponse {
  CloudflareChangeResult result = 1;
}
```

In `internal/repo/cloudflare/repository.go` change the const block to:

```go
const (
	KindWorker       ResourceKind = "worker"
	KindPages        ResourceKind = "pages"
	KindEmailRouting ResourceKind = "email_routing"
)
```

and update the comment on `ResourceKind` to `// ResourceKind says which kind of Cloudflare resource an operation changed: a Worker, a Pages project, or a Zone's Email Routing.`

- [ ] **Step 2: Generate and lint**

Run:
```bash
buf format -w && make buf && buf lint && buf format --diff --exit-code
```
Expected: no output from lint and format. New generated symbols appear in `pkg/proto/neobox/v1/cloudflare.pb.go`, `cloudflare.pb.validate.go`(if used), `neoboxv1connect/cloudflare.connect.go`, and `front/src/gen/neobox/v1/cloudflare_pb.ts`.

Run: `go build ./... 2>&1 | head`
Expected: a compile error that `*CloudflareServiceServer does not implement neoboxv1connect.CloudflareServiceHandler (missing method CreateCloudflareEmailAddress ...)` in `internal/app/routes.go`. This is the expected failing state that Task 3 fixes.

- [ ] **Step 3: Commit**

```bash
git add proto internal/repo pkg/proto front/src/gen
git commit -m "feat(proto): Email Routing RPCs and email_routing operation kind"
```

(The build is red between this commit and Task 3's; do Task 3 immediately. If you prefer green commits, squash Tasks 2 and 3 when merging.)

---

### Task 3: Manager and service handlers

**Files:**
- Create: `internal/cloudflareacct/email.go`, `internal/application/cloudflare_email.go`, `internal/application/cloudflare_email_test.go`
- Modify: `internal/application/cloudflare_service.go` (`mapCloudflareErr`, lines ~517-559), `internal/application/cloudflare_manage.go` (`resourceKindToProto` ~line 77, list filter switch ~line 106), `internal/application/cloudflare_service_test.go` (fixture, lines 54-70)

**Interfaces:**
- Consumes: everything Task 1 and Task 2 produced; `Account.operate`, `resourceChange`, `Account.Zone`, `Account.classify`, `Manager.lockResource`, `issueSet`, `AddressIssue`, `noChange()`, `ErrInUse`, `ErrZoneNotFound`; in `application`: `s.openAccount`, `s.change`, `trim`, `readIssuesToProto`, `mapCloudflareErr`, `wantCode`, `resourceOps`, `newCFFixture`.
- Produces: on `*cloudflareacct.Account`: `EmailRouting(ctx, zoneID) (*EmailRoutingView, error)`, `EnableEmailRouting(ctx, actor, zoneID)`, `CreateEmailRule(ctx, actor, zoneID, in)`, `UpdateEmailRule(ctx, actor, zoneID, ruleID, in)`, `DeleteEmailRule(ctx, actor, zoneID, ruleID)`, `SetEmailCatchAll(ctx, actor, zoneID, in)`, `EmailAddresses(ctx)`, `CreateEmailAddress(ctx, actor, email)`, `DeleteEmailAddress(ctx, actor, zoneID, addressID)` (all writes return `(*OperationResult, error)`); errors `ErrEmailRuleNotFound`, `ErrEmailAddressNotFound`, `ErrAddressUnverified`, `ErrAddressInUse`, `ErrRuleReadOnly`. Logged actions: `enable_routing`, `create_rule`, `update_rule`, `enable_rule`, `disable_rule`, `delete_rule`, `set_catch_all`, `create_address`, `delete_address`.

- [ ] **Step 1: Give the test fixture email permissions**

In `internal/application/cloudflare_service_test.go`, add the four permissions to `all`:

```go
	all := []string{
		cftest.PermAccountRead, cftest.PermZoneRead, cftest.PermDNSRead, cftest.PermDNSEdit,
		cftest.PermWorkersRead, cftest.PermRoutesRead, cftest.PermPagesRead,
		cftest.PermTunnelRead, cftest.PermTunnelEdit,
		cftest.PermWorkersEdit, cftest.PermRoutesEdit, cftest.PermPagesEdit,
		cftest.PermEmailRulesRead, cftest.PermEmailRulesEdit, cftest.PermEmailAddrRead, cftest.PermEmailAddrEdit,
	}
```

and, after the `"tunnel-no-dns"` token, add:

```go
	cf.AddToken("email-read", &cftest.Token{Accounts: []string{cfAccA}, Perms: []string{
		cftest.PermZoneRead, cftest.PermEmailRulesRead, cftest.PermEmailAddrRead,
	}})
```

- [ ] **Step 2: Write the failing tests**

Create `internal/application/cloudflare_email_test.go`:

```go
package application

import (
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"go.orx.me/apps/neo-box/internal/cloudflare/cftest"
	cfrepo "go.orx.me/apps/neo-box/internal/repo/cloudflare"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

const (
	emailForward = neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD
	emailDrop    = neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP
)

// emailFixture is a connection with one verified (me@) and one unverified
// (new@) destination address.
func emailFixture(t *testing.T) (*cfFixture, string) {
	f := newCFFixture(t)
	f.cf.AddEmailAddress(cfAccA, &cftest.EmailAddress{Email: "me@gmail.com", Verified: true})
	f.cf.AddEmailAddress(cfAccA, &cftest.EmailAddress{Email: "new@gmail.com"})
	return f, f.connect(t, "u1", cfAccA, "full")
}

type emailAPI struct {
	t    *testing.T
	f    *cfFixture
	conn string
	zone string
}

func (e emailAPI) get() *neoboxv1.GetCloudflareEmailRoutingResponse {
	e.t.Helper()
	r, err := e.f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: e.conn, ZoneId: e.zone}))
	if err != nil {
		e.t.Fatalf("GetCloudflareEmailRouting: %v", err)
	}
	return r.Msg
}

func (e emailAPI) enable() (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.EnableCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.EnableCloudflareEmailRoutingRequest{ConnectionId: e.conn, ZoneId: e.zone}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) createRule(in *neoboxv1.CloudflareEmailRuleInput) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.CreateCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.CreateCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, Rule: in}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) updateRule(id string, in *neoboxv1.CloudflareEmailRuleInput) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.UpdateCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.UpdateCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, RuleId: id, Rule: in}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) deleteRule(id string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.DeleteCloudflareEmailRule(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteCloudflareEmailRuleRequest{ConnectionId: e.conn, ZoneId: e.zone, RuleId: id}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) catchAll(enabled bool, action neoboxv1.CloudflareEmailAction, target string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.SetCloudflareEmailCatchAll(asUser("u1"), connect.NewRequest(&neoboxv1.SetCloudflareEmailCatchAllRequest{ConnectionId: e.conn, ZoneId: e.zone, Enabled: enabled, Action: action, Target: target}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) addresses() []*neoboxv1.CloudflareEmailAddress {
	e.t.Helper()
	r, err := e.f.srv.ListCloudflareEmailAddresses(asUser("u1"), connect.NewRequest(&neoboxv1.ListCloudflareEmailAddressesRequest{ConnectionId: e.conn}))
	if err != nil {
		e.t.Fatalf("ListCloudflareEmailAddresses: %v", err)
	}
	return r.Msg.GetAddresses()
}

func (e emailAPI) createAddress(email string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.CreateCloudflareEmailAddress(asUser("u1"), connect.NewRequest(&neoboxv1.CreateCloudflareEmailAddressRequest{ConnectionId: e.conn, Email: email}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func (e emailAPI) deleteAddress(id string) (*neoboxv1.CloudflareChangeResult, error) {
	r, err := e.f.srv.DeleteCloudflareEmailAddress(asUser("u1"), connect.NewRequest(&neoboxv1.DeleteCloudflareEmailAddressRequest{ConnectionId: e.conn, ZoneId: e.zone, AddressId: id}))
	if err != nil {
		return nil, err
	}
	return r.Msg.GetResult(), nil
}

func addressByEmail(list []*neoboxv1.CloudflareEmailAddress, email string) *neoboxv1.CloudflareEmailAddress {
	for _, a := range list {
		if a.GetEmail() == email {
			return a
		}
	}
	return nil
}

func ruleIn(addr string, action neoboxv1.CloudflareEmailAction, target string, enabled bool) *neoboxv1.CloudflareEmailRuleInput {
	return &neoboxv1.CloudflareEmailRuleInput{Address: addr, Action: action, Target: target, Enabled: enabled}
}

func TestCloudflareEmailRoutingEnableAndRead(t *testing.T) {
	f, conn := emailFixture(t)
	api := emailAPI{t, f, conn, "za1"}

	got := api.get()
	if got.GetSettings().GetEnabled() || len(got.GetRequiredDnsRecords()) == 0 || len(got.GetRules()) != 0 || len(got.GetIssues()) != 0 {
		t.Fatalf("before enabling = %+v", got)
	}
	res, err := api.enable()
	op := res.GetOperation()
	if err != nil || !res.GetChanged() || op.GetKind() != neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING ||
		op.GetAction() != "enable_routing" || op.GetResource() != "alpha.com" ||
		op.GetStatus() != neoboxv1.CloudflareOperationStatus_CLOUDFLARE_OPERATION_STATUS_SUCCEEDED {
		t.Fatalf("enable = %+v, %v", res, err)
	}
	if !f.cf.EmailRouting("za1").Enabled {
		t.Fatal("Email Routing was not enabled at Cloudflare")
	}
	again, err := api.enable()
	if err != nil || again.GetChanged() || len(resourceOps(t, f, conn)) != 1 {
		t.Fatalf("enable again = %+v, %v (%d operations)", again, err, len(resourceOps(t, f, conn)))
	}
	got = api.get()
	if !got.GetSettings().GetEnabled() || got.GetSettings().GetStatus() != "ready" || len(got.GetRequiredDnsRecords()) != 0 {
		t.Fatalf("after enabling = %+v", got)
	}

	// A Zone of another account is never reachable.
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: conn, ZoneId: "zb1"}))
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u2"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: conn, ZoneId: "za1"}))
	wantCode(t, "connection of another user", err, connect.CodeNotFound)
}

func TestCloudflareEmailRules(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	res, err := api.createRule(ruleIn(" Info@Alpha.com ", emailForward, "me@gmail.com", true))
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "create_rule" || res.GetOperation().GetTarget() != "info@alpha.com" {
		t.Fatalf("create = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1"); len(st.Rules) != 1 || st.Rules[0].Address != "info@alpha.com" || st.Rules[0].Target != "me@gmail.com" {
		t.Fatalf("fake rules = %+v", st.Rules)
	}

	_, err = api.createRule(ruleIn("info@alpha.com", emailDrop, "", true))
	wantCode(t, "duplicate address", err, connect.CodeAlreadyExists)
	_, err = api.createRule(ruleIn("info@bravo.com", emailDrop, "", true))
	wantCode(t, "address of another zone", err, connect.CodeInvalidArgument)
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "not-an-address", true))
	wantCode(t, "bad target", err, connect.CodeInvalidArgument)
	_, err = api.createRule(&neoboxv1.CloudflareEmailRuleInput{Address: "x@alpha.com"})
	wantCode(t, "no action", err, connect.CodeInvalidArgument)
	writes := len(f.cf.Writes())
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "new@gmail.com", true))
	wantCode(t, "unverified target", err, connect.CodeFailedPrecondition)
	_, err = api.createRule(ruleIn("x@alpha.com", emailForward, "stranger@gmail.com", true))
	wantCode(t, "unknown target", err, connect.CodeFailedPrecondition)
	if len(f.cf.Writes()) != writes {
		t.Fatalf("a refused rule was sent: %+v", f.cf.Writes()[writes:])
	}
	if _, err := api.createRule(ruleIn("sales@alpha.com", emailDrop, "", true)); err != nil {
		t.Fatalf("create drop rule: %v", err)
	}

	// Rules come back sorted by address.
	rules := api.get().GetRules()
	if len(rules) != 2 || rules[0].GetAddress() != "info@alpha.com" || rules[1].GetAddress() != "sales@alpha.com" || !rules[0].GetEditable() {
		t.Fatalf("rules = %+v", rules)
	}
	info := rules[0]

	// Disable it, again (nothing to do), re-enable to an unverified address
	// (refused), then rename.
	off := &neoboxv1.CloudflareEmailRuleInput{Name: info.GetName(), Address: info.GetAddress(), Action: info.GetAction(), Target: info.GetTarget(), Enabled: false}
	res, err = api.updateRule(info.GetId(), off)
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "disable_rule" || f.cf.EmailRouting("za1").Rules[0].Enabled {
		t.Fatalf("disable = %+v, %v", res, err)
	}
	if res, err = api.updateRule(info.GetId(), off); err != nil || res.GetChanged() {
		t.Fatalf("disable again = %+v, %v", res, err)
	}
	on := &neoboxv1.CloudflareEmailRuleInput{Address: info.GetAddress(), Action: emailForward, Target: "new@gmail.com", Enabled: true}
	_, err = api.updateRule(info.GetId(), on)
	wantCode(t, "re-enable to unverified", err, connect.CodeFailedPrecondition)
	on.Target, on.Name = "me@gmail.com", "Info"
	if res, err = api.updateRule(info.GetId(), on); err != nil || res.GetOperation().GetAction() != "update_rule" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	// Another rule's address cannot be taken.
	on.Address = "sales@alpha.com"
	_, err = api.updateRule(info.GetId(), on)
	wantCode(t, "address of another rule", err, connect.CodeAlreadyExists)
	_, err = api.updateRule("nope", ruleIn("info@alpha.com", emailDrop, "", true))
	wantCode(t, "unknown rule", err, connect.CodeNotFound)

	// A rule that sends to a Worker, or that a Worker's wrangler owns, is
	// shown read-only and never changed.
	f.cf.AddEmailRule("za1", &cftest.EmailRule{Name: "hook", Address: "hook@alpha.com", Action: "worker", Target: "mailer", Enabled: true})
	f.cf.AddEmailRule("za1", &cftest.EmailRule{Name: "wr", Address: "wr@alpha.com", Action: "drop", Source: "wrangler", Enabled: true})
	var worker, wrangler *neoboxv1.CloudflareEmailRule
	for _, r := range api.get().GetRules() {
		switch r.GetAddress() {
		case "hook@alpha.com":
			worker = r
		case "wr@alpha.com":
			wrangler = r
		}
	}
	if worker == nil || worker.GetEditable() || worker.GetReadonlyReason() == "" || worker.GetAction() != neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_WORKER ||
		wrangler == nil || wrangler.GetEditable() {
		t.Fatalf("read-only rules = %+v, %+v", worker, wrangler)
	}
	writes = len(f.cf.Writes())
	for _, r := range []*neoboxv1.CloudflareEmailRule{worker, wrangler} {
		_, err = api.updateRule(r.GetId(), ruleIn(r.GetAddress(), emailDrop, "", false))
		wantCode(t, "update read-only rule", err, connect.CodeFailedPrecondition)
		_, err = api.deleteRule(r.GetId())
		wantCode(t, "delete read-only rule", err, connect.CodeFailedPrecondition)
	}
	if len(f.cf.Writes()) != writes {
		t.Fatalf("a read-only rule was changed: %+v", f.cf.Writes()[writes:])
	}

	// Delete.
	res, err = api.deleteRule(info.GetId())
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "delete_rule" {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	_, err = api.deleteRule(info.GetId())
	wantCode(t, "delete again", err, connect.CodeNotFound)

	for _, op := range resourceOps(t, f, conn) {
		if op.Kind != cfrepo.KindEmailRouting || op.Resource != "alpha.com" {
			t.Errorf("operation = %+v", op)
		}
	}
}

func TestCloudflareEmailCatchAll(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	if c := api.get().GetCatchAll(); c.GetEnabled() || c.GetAction() != emailDrop || !c.GetEditable() {
		t.Fatalf("catch-all at the start = %+v", c)
	}
	res, err := api.catchAll(true, emailForward, "me@gmail.com")
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "set_catch_all" {
		t.Fatalf("forward = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; !st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all = %+v", st)
	}
	if res, err = api.catchAll(true, emailForward, "ME@gmail.com"); err != nil || res.GetChanged() {
		t.Fatalf("same again = %+v, %v", res, err)
	}
	// Off keeps the forward address.
	if res, err = api.catchAll(false, 0, ""); err != nil || !res.GetChanged() {
		t.Fatalf("off = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; st.Enabled || st.Action != "forward" || st.Target != "me@gmail.com" {
		t.Fatalf("fake catch-all after off = %+v", st)
	}
	if res, err = api.catchAll(false, 0, ""); err != nil || res.GetChanged() {
		t.Fatalf("off again = %+v, %v", res, err)
	}
	_, err = api.catchAll(true, emailForward, "new@gmail.com")
	wantCode(t, "unverified target", err, connect.CodeFailedPrecondition)
	_, err = api.catchAll(true, emailForward, "nope")
	wantCode(t, "bad target", err, connect.CodeInvalidArgument)
	if res, err = api.catchAll(true, emailDrop, ""); err != nil || !res.GetChanged() {
		t.Fatalf("drop = %+v, %v", res, err)
	}
	if st := f.cf.EmailRouting("za1").CatchAll; !st.Enabled || st.Action != "drop" {
		t.Fatalf("fake catch-all after drop = %+v", st)
	}
}

func TestCloudflareEmailAddresses(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.SetEmailEnabled("za1", true)
	api := emailAPI{t, f, conn, "za1"}

	list := api.addresses()
	if len(list) != 2 || list[0].GetEmail() != "me@gmail.com" || !list[0].GetVerified() || list[1].GetEmail() != "new@gmail.com" || list[1].GetVerified() {
		t.Fatalf("addresses = %+v", list)
	}
	_, err := api.createAddress("nope")
	wantCode(t, "bad address", err, connect.CodeInvalidArgument)
	if res, err := api.createAddress(" ME@gmail.com "); err != nil || res.GetChanged() {
		t.Fatalf("existing address = %+v, %v", res, err)
	}
	res, err := api.createAddress("extra@gmail.com")
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "create_address" ||
		res.GetOperation().GetResource() != "destination addresses" || res.GetOperation().GetTarget() != "extra@gmail.com" {
		t.Fatalf("create = %+v, %v", res, err)
	}
	if got := api.addresses(); len(got) != 3 || addressByEmail(got, "extra@gmail.com") == nil || addressByEmail(got, "extra@gmail.com").GetVerified() {
		t.Fatalf("addresses after create = %+v", got)
	}

	// An address a rule forwards to cannot be deleted.
	if _, err := api.createRule(ruleIn("info@alpha.com", emailForward, "me@gmail.com", true)); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	me := addressByEmail(api.addresses(), "me@gmail.com")
	_, err = api.deleteAddress(me.GetId())
	wantCode(t, "address in use by a rule", err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "info@alpha.com") {
		t.Errorf("the error does not name the rule: %v", err)
	}
	if len(f.cf.EmailAddresses(cfAccA)) != 3 {
		t.Fatal("an address in use was deleted")
	}
	rule := api.get().GetRules()[0]
	if _, err := api.deleteRule(rule.GetId()); err != nil {
		t.Fatal(err)
	}
	// ... nor one the catch-all forwards to.
	if _, err := api.catchAll(true, emailForward, "me@gmail.com"); err != nil {
		t.Fatal(err)
	}
	_, err = api.deleteAddress(me.GetId())
	wantCode(t, "address in use by the catch-all", err, connect.CodeFailedPrecondition)
	if _, err := api.catchAll(true, emailDrop, ""); err != nil {
		t.Fatal(err)
	}
	res, err = api.deleteAddress(me.GetId())
	if err != nil || !res.GetChanged() || res.GetOperation().GetAction() != "delete_address" || len(f.cf.EmailAddresses(cfAccA)) != 2 {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	_, err = api.deleteAddress("nope")
	wantCode(t, "unknown address", err, connect.CodeNotFound)
	other := emailAPI{t, f, conn, "zb1"}
	_, err = other.deleteAddress(api.addresses()[0].GetId())
	wantCode(t, "zone of another account", err, connect.CodeNotFound)
}

func TestCloudflareEmailPermissions(t *testing.T) {
	f := newCFFixture(t)
	read := emailAPI{t, f, f.connect(t, "u1", cfAccA, "email-read"), "za1"}

	if got := read.get(); got.GetSettings().GetEnabled() {
		t.Fatalf("settings = %+v", got)
	}
	_ = read.addresses()
	_, err := read.enable()
	wantCode(t, "enable with a read token", err, connect.CodePermissionDenied)
	_, err = read.createAddress("x@gmail.com")
	wantCode(t, "create address with a read token", err, connect.CodePermissionDenied)
	_, err = read.createRule(ruleIn("x@alpha.com", emailDrop, "", true))
	wantCode(t, "create rule with a read token", err, connect.CodePermissionDenied)

	// A token without any Email Routing permission cannot read it either.
	none := emailAPI{t, f, f.connect(t, "u1", cfAccA, "manage-read"), "za1"}
	_, err = f.srv.GetCloudflareEmailRouting(asUser("u1"), connect.NewRequest(&neoboxv1.GetCloudflareEmailRoutingRequest{ConnectionId: none.conn, ZoneId: "za1"}))
	wantCode(t, "read without permission", err, connect.CodePermissionDenied)
}

func TestCloudflareEmailUnconfirmedWriteIsLoggedAsUnknown(t *testing.T) {
	f, conn := emailFixture(t)
	f.cf.DropAnswer = func(r *http.Request) bool {
		return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/email/routing/dns")
	}
	api := emailAPI{t, f, conn, "za1"}
	_, err := api.enable()
	wantCode(t, "enable without an answer", err, connect.CodeUnknown)
	ops := resourceOps(t, f, conn)
	if len(ops) != 1 || ops[0].Status != cfrepo.StatusUnknown || ops[0].Action != "enable_routing" {
		t.Fatalf("operations = %+v", ops)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/application/ -run 'CloudflareEmail' 2>&1 | head -20`
Expected: build failure (`s.GetCloudflareEmailRouting undefined` or the missing handler methods), since neither the manager nor the handlers exist.

- [ ] **Step 4: Write the Manager part**

Create `internal/cloudflareacct/email.go`:

```go
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
```

- [ ] **Step 5: Write the handlers and wire the errors**

Create `internal/application/cloudflare_email.go`:

```go
package application

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.orx.me/apps/neo-box/internal/cloudflare"
	"go.orx.me/apps/neo-box/internal/cloudflareacct"
	neoboxv1 "go.orx.me/apps/neo-box/pkg/proto/neobox/v1"
)

// --- Email Routing ---

func emailActionToProto(t string) neoboxv1.CloudflareEmailAction {
	switch t {
	case cloudflare.EmailActionForward:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD
	case cloudflare.EmailActionDrop:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP
	case cloudflare.EmailActionWorker:
		return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_WORKER
	}
	return neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_UNSPECIFIED
}

// emailActionFromProto leaves out Worker: Neo Box does not create or
// change Worker rules, so it fails the input check as "must be forward or
// drop".
func emailActionFromProto(a neoboxv1.CloudflareEmailAction) string {
	switch a {
	case neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_FORWARD:
		return cloudflare.EmailActionForward
	case neoboxv1.CloudflareEmailAction_CLOUDFLARE_EMAIL_ACTION_DROP:
		return cloudflare.EmailActionDrop
	}
	return ""
}

func emailRuleToProto(r cloudflare.EmailRule) *neoboxv1.CloudflareEmailRule {
	why := r.ReadOnlyReason(false)
	return &neoboxv1.CloudflareEmailRule{
		Id: r.ID, Name: r.Name, Enabled: r.Enabled, Priority: int32(r.Priority),
		Address: r.Address(), Action: emailActionToProto(r.ActionType()), Target: r.Target(),
		Editable: why == "", ReadonlyReason: why,
	}
}

func emailCatchAllToProto(r cloudflare.EmailRule) *neoboxv1.CloudflareEmailCatchAll {
	why := r.ReadOnlyReason(true)
	return &neoboxv1.CloudflareEmailCatchAll{
		Enabled: r.Enabled, Action: emailActionToProto(r.ActionType()), Target: r.Target(),
		Editable: why == "", ReadonlyReason: why,
	}
}

func emailRuleInputFromProto(in *neoboxv1.CloudflareEmailRuleInput) cloudflare.EmailRuleInput {
	return cloudflare.EmailRuleInput{
		Name: in.GetName(), Address: in.GetAddress(), Action: emailActionFromProto(in.GetAction()),
		Target: in.GetTarget(), Enabled: in.GetEnabled(),
	}
}

func (s *CloudflareServiceServer) GetCloudflareEmailRouting(ctx context.Context, req *connect.Request[neoboxv1.GetCloudflareEmailRoutingRequest]) (*connect.Response[neoboxv1.GetCloudflareEmailRoutingResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	v, err := a.EmailRouting(ctx, trim(req.Msg.GetZoneId()))
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.GetCloudflareEmailRoutingResponse{
		Settings: &neoboxv1.CloudflareEmailRoutingSettings{
			Enabled: v.Settings.Enabled, Status: v.Settings.Status, SupportSubaddress: v.Settings.SupportSubaddress,
		},
		Issues: readIssuesToProto(v.Issues),
	}
	for _, r := range v.Rules {
		out.Rules = append(out.Rules, emailRuleToProto(r))
	}
	if v.CatchAll != nil {
		out.CatchAll = emailCatchAllToProto(*v.CatchAll)
	}
	for _, d := range v.RequiredDNS {
		rec := &neoboxv1.CloudflareEmailDNSRecord{Type: d.Type, Name: d.Name, Content: d.Content}
		if d.Priority != nil {
			rec.Priority = int32(*d.Priority)
		}
		out.RequiredDnsRecords = append(out.RequiredDnsRecords, rec)
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) ListCloudflareEmailAddresses(ctx context.Context, req *connect.Request[neoboxv1.ListCloudflareEmailAddressesRequest]) (*connect.Response[neoboxv1.ListCloudflareEmailAddressesResponse], error) {
	_, a, err := s.openAccount(ctx, req.Msg.GetConnectionId())
	if err != nil {
		return nil, err
	}
	list, err := a.EmailAddresses(ctx)
	if err != nil {
		return nil, mapCloudflareErr(err)
	}
	out := &neoboxv1.ListCloudflareEmailAddressesResponse{}
	for _, ad := range list {
		pa := &neoboxv1.CloudflareEmailAddress{Id: ad.ID, Email: ad.Email, Verified: ad.IsVerified()}
		if ad.Created != nil {
			pa.CreatedAt = timestamppb.New(*ad.Created)
		}
		if ad.IsVerified() {
			pa.VerifiedAt = timestamppb.New(*ad.Verified)
		}
		out.Addresses = append(out.Addresses, pa)
	}
	return connect.NewResponse(out), nil
}

func (s *CloudflareServiceServer) EnableCloudflareEmailRouting(ctx context.Context, req *connect.Request[neoboxv1.EnableCloudflareEmailRoutingRequest]) (*connect.Response[neoboxv1.EnableCloudflareEmailRoutingResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.EnableEmailRouting(ctx, actor, trim(req.Msg.GetZoneId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.EnableCloudflareEmailRoutingResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) CreateCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.CreateCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.CreateEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), emailRuleInputFromProto(req.Msg.GetRule()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) UpdateCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.UpdateCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.UpdateCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.UpdateEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetRuleId()), emailRuleInputFromProto(req.Msg.GetRule()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.UpdateCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareEmailRule(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareEmailRuleRequest]) (*connect.Response[neoboxv1.DeleteCloudflareEmailRuleResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteEmailRule(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetRuleId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareEmailRuleResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) SetCloudflareEmailCatchAll(ctx context.Context, req *connect.Request[neoboxv1.SetCloudflareEmailCatchAllRequest]) (*connect.Response[neoboxv1.SetCloudflareEmailCatchAllResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.SetEmailCatchAll(ctx, actor, trim(req.Msg.GetZoneId()), cloudflare.EmailCatchAllInput{
			Enabled: req.Msg.GetEnabled(), Action: emailActionFromProto(req.Msg.GetAction()), Target: req.Msg.GetTarget(),
		})
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.SetCloudflareEmailCatchAllResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) CreateCloudflareEmailAddress(ctx context.Context, req *connect.Request[neoboxv1.CreateCloudflareEmailAddressRequest]) (*connect.Response[neoboxv1.CreateCloudflareEmailAddressResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.CreateEmailAddress(ctx, actor, req.Msg.GetEmail())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.CreateCloudflareEmailAddressResponse{Result: r}), nil
}

func (s *CloudflareServiceServer) DeleteCloudflareEmailAddress(ctx context.Context, req *connect.Request[neoboxv1.DeleteCloudflareEmailAddressRequest]) (*connect.Response[neoboxv1.DeleteCloudflareEmailAddressResponse], error) {
	r, err := s.change(ctx, req.Msg.GetConnectionId(), func(a *cloudflareacct.Account, actor cloudflareacct.Actor) (*cloudflareacct.OperationResult, error) {
		return a.DeleteEmailAddress(ctx, actor, trim(req.Msg.GetZoneId()), trim(req.Msg.GetAddressId()))
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&neoboxv1.DeleteCloudflareEmailAddressResponse{Result: r}), nil
}
```

In `internal/application/cloudflare_service.go` `mapCloudflareErr`:
- add `errors.Is(err, cloudflareacct.ErrEmailRuleNotFound), errors.Is(err, cloudflareacct.ErrEmailAddressNotFound),` to the `connectx.NotFound` case (the list ending `errors.Is(err, cloudflareacct.ErrNotAttached)`; add a comma after it);
- change the FailedPrecondition case with `ErrNotEditable` to also include `errors.Is(err, cloudflareacct.ErrAddressUnverified), errors.Is(err, cloudflareacct.ErrAddressInUse), errors.Is(err, cloudflareacct.ErrRuleReadOnly)`.

In `internal/application/cloudflare_manage.go`:
- `resourceKindToProto`: add `case cfrepo.KindEmailRouting: return neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING`;
- in `ListCloudflareResourceOperations`' `switch req.Msg.GetKind()` add `case neoboxv1.CloudflareResourceKind_CLOUDFLARE_RESOURCE_KIND_EMAIL_ROUTING: kind = cfrepo.KindEmailRouting`.

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/application/ -run 'CloudflareEmail' -v 2>&1 | tail -30`
Expected: `TestCloudflareEmailRoutingEnableAndRead`, `...Rules`, `...CatchAll`, `...Addresses`, `...Permissions`, `...UnconfirmedWriteIsLoggedAsUnknown` all PASS.

Then everything Go: `go test ./... 2>&1 | tail -20` (the repository tests skip without `NEOBOX_TEST_POSTGRES_DSN`).
Expected: all `ok` / `no test files`. Then `golangci-lint run --config=.golangci.yml ./internal/cloudflare/... ./internal/cloudflareacct/... ./internal/application/...` with no findings.

If a test fails, debug before moving on. Likely causes: the fake's rule order (sorted in the manager, so the tests do not depend on it), `a.Zone` returning NotFound for `zb1`, and `DropAnswer` needing the write to reach the fake first (the fake handles the request, then closes the connection).

- [ ] **Step 7: Commit**

```bash
git add internal
git commit -m "feat(cloudflare): manage Email Routing rules, catch-all and destination addresses"
```

---

### Task 4: Frontend API hooks and logic

**Files:**
- Modify: `front/src/api/cloudflare.ts` (exports ~line 18-60, keys ~line 62-120, append hooks after `useCloudflareWorkerChanges` ~line 450), `front/src/features/cloudflare/manage.ts` (`actionLabels`), `front/src/features/cloudflare/manage.test.ts`
- Create: `front/src/features/cloudflare/email.ts`, `front/src/features/cloudflare/email.test.ts`

**Interfaces:**
- Consumes: generated `front/src/gen/neobox/v1/cloudflare_pb` (Task 2), `useResourceChanges` (same file as the hooks), `client`.
- Produces: from `@/api/cloudflare`: `CloudflareEmailAction`, types `CloudflareEmailRule`, `CloudflareEmailCatchAll`, `CloudflareEmailAddress`, `CloudflareEmailDNSRecord`, `CloudflareEmailRoutingSettings`, `GetCloudflareEmailRoutingResponse`, `CloudflareEmailRuleInit`; hooks `useCloudflareEmailRouting(connectionId, zoneId)`, `useCloudflareEmailAddresses(connectionId)`, `useCloudflareEmailChanges(connectionId, zoneId)` returning mutations `enable`, `createRule`, `updateRule`, `deleteRule`, `setCatchAll`, `createAddress`, `deleteAddress`. From `email.ts`: `emailProblem`, `ruleAddressProblem`, `targetProblem`, `ruleSummary`, `catchAllSummary`, `catchAllMode`, `catchAllRequest`, `catchAllProblem`, `addressStatus`, `verifiedEmails`, `routingState`, `dnsRecordSummary`, type `CatchAllMode`.

- [ ] **Step 1: Write the failing tests**

Create `front/src/features/cloudflare/email.test.ts`:

```ts
import {
  CloudflareEmailAddressSchema,
  CloudflareEmailCatchAllSchema,
  CloudflareEmailDNSRecordSchema,
  CloudflareEmailRoutingSettingsSchema,
  CloudflareEmailRuleSchema,
} from '@/gen/neobox/v1/cloudflare_pb'
import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'
import { CloudflareEmailAction as Action } from '@/api/cloudflare'
import {
  addressStatus,
  catchAllMode,
  catchAllProblem,
  catchAllRequest,
  catchAllSummary,
  dnsRecordSummary,
  emailProblem,
  routingState,
  ruleAddressProblem,
  ruleSummary,
  targetProblem,
  verifiedEmails,
} from './email'

describe('input checks', () => {
  it('checks an email address', () => {
    expect(emailProblem('me@gmail.com')).toBeUndefined()
    expect(emailProblem(' me@gmail.com ')).toBeUndefined()
    expect(emailProblem('')).toBeTruthy()
    expect(emailProblem('nope')).toBeTruthy()
    expect(emailProblem('a b@c.com')).toBeTruthy()
    expect(emailProblem(`${'x'.repeat(90)}@c.com`)).toBeTruthy()
  })

  it('checks a rule address against its zone', () => {
    expect(ruleAddressProblem('info@example.com', 'example.com')).toBeUndefined()
    expect(ruleAddressProblem('Info@Example.COM', 'example.com')).toBeUndefined()
    expect(ruleAddressProblem('info@other.com', 'example.com')).toMatch(
      /@example\.com/
    )
    expect(ruleAddressProblem('info', 'example.com')).toBeTruthy()
  })

  it('needs a target only for a forward', () => {
    expect(targetProblem(Action.FORWARD, 'me@gmail.com')).toBeUndefined()
    expect(targetProblem(Action.FORWARD, '')).toBeTruthy()
    expect(targetProblem(Action.DROP, '')).toBeUndefined()
  })
})

describe('wording', () => {
  it('summarizes a rule', () => {
    expect(
      ruleSummary(
        create(CloudflareEmailRuleSchema, {
          action: Action.FORWARD,
          target: 'me@gmail.com',
        })
      )
    ).toBe('Forward to me@gmail.com')
    expect(
      ruleSummary(create(CloudflareEmailRuleSchema, { action: Action.DROP }))
    ).toBe('Drop')
    expect(
      ruleSummary(
        create(CloudflareEmailRuleSchema, {
          action: Action.WORKER,
          target: 'mailer',
        })
      )
    ).toBe('Send to Worker mailer')
  })

  it('summarizes the catch-all', () => {
    expect(catchAllSummary(undefined)).toBe('Unknown')
    expect(
      catchAllSummary(create(CloudflareEmailCatchAllSchema, { enabled: false }))
    ).toBe('Off')
    expect(
      catchAllSummary(
        create(CloudflareEmailCatchAllSchema, {
          enabled: true,
          action: Action.FORWARD,
          target: 'me@gmail.com',
        })
      )
    ).toBe('Forward to me@gmail.com')
  })

  it('tells a verified address from a pending one', () => {
    const ok = create(CloudflareEmailAddressSchema, { verified: true })
    const pending = create(CloudflareEmailAddressSchema, { verified: false })
    expect(addressStatus(ok).label).toBe('Verified')
    expect(addressStatus(pending).label).toMatch(/verification/i)
    expect(
      verifiedEmails([
        create(CloudflareEmailAddressSchema, { email: 'b@x.com', verified: true }),
        create(CloudflareEmailAddressSchema, { email: 'p@x.com' }),
        create(CloudflareEmailAddressSchema, { email: 'a@x.com', verified: true }),
      ])
    ).toEqual(['a@x.com', 'b@x.com'])
  })

  it('states how Email Routing stands', () => {
    const s = (v: object) => create(CloudflareEmailRoutingSettingsSchema, v)
    expect(routingState(undefined).label).toBe('Loading')
    expect(routingState(s({ enabled: false })).label).toBe('Not enabled')
    expect(routingState(s({ enabled: true, status: 'ready' })).label).toBe('Active')
    const bad = routingState(s({ enabled: true, status: 'misconfigured' }))
    expect(bad.label).toBe('Needs attention')
    expect(bad.hint).toMatch(/misconfigured/)
  })

  it('describes a required DNS record', () => {
    expect(
      dnsRecordSummary(
        create(CloudflareEmailDNSRecordSchema, {
          type: 'MX',
          name: 'example.com',
          content: 'route1.mx.cloudflare.net',
          priority: 20,
        })
      )
    ).toBe('MX example.com → route1.mx.cloudflare.net (priority 20)')
    expect(
      dnsRecordSummary(
        create(CloudflareEmailDNSRecordSchema, {
          type: 'TXT',
          name: 'example.com',
          content: 'v=spf1 ~all',
        })
      )
    ).toBe('TXT example.com → v=spf1 ~all')
  })
})

describe('catch-all choice', () => {
  it('reads the mode from the catch-all', () => {
    const c = (v: object) => create(CloudflareEmailCatchAllSchema, v)
    expect(catchAllMode(c({ enabled: false, action: Action.FORWARD }))).toBe('off')
    expect(catchAllMode(c({ enabled: true, action: Action.DROP }))).toBe('drop')
    expect(catchAllMode(c({ enabled: true, action: Action.FORWARD }))).toBe(
      'forward'
    )
  })

  it('builds the request from the choice', () => {
    expect(catchAllRequest('off', 'x')).toEqual({
      enabled: false,
      action: Action.DROP,
      target: '',
    })
    expect(catchAllRequest('drop', 'x')).toEqual({
      enabled: true,
      action: Action.DROP,
      target: '',
    })
    expect(catchAllRequest('forward', ' me@gmail.com ')).toEqual({
      enabled: true,
      action: Action.FORWARD,
      target: 'me@gmail.com',
    })
  })

  it('needs a target only to forward', () => {
    expect(catchAllProblem('forward', '')).toBeTruthy()
    expect(catchAllProblem('forward', 'me@gmail.com')).toBeUndefined()
    expect(catchAllProblem('drop', '')).toBeUndefined()
    expect(catchAllProblem('off', '')).toBeUndefined()
  })
})
```

In `front/src/features/cloudflare/manage.test.ts`, import is already `actionLabel`; add a test inside an existing `describe` (or a new one at the end):

```ts
describe('email routing log wording', () => {
  it('words the Email Routing actions', () => {
    expect(actionLabel({ action: 'enable_routing' })).toBe('Enabled Email Routing')
    expect(actionLabel({ action: 'create_rule' })).toBe('Added routing rule')
    expect(actionLabel({ action: 'disable_rule' })).toBe('Disabled routing rule')
    expect(actionLabel({ action: 'set_catch_all' })).toBe('Changed catch-all')
    expect(actionLabel({ action: 'delete_address' })).toBe(
      'Deleted destination address'
    )
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd front && npx vitest run src/features/cloudflare/email.test.ts src/features/cloudflare/manage.test.ts 2>&1 | tail -20`
Expected: FAIL (`Failed to resolve import "./email"`, and the `actionLabel` assertions fail).

- [ ] **Step 3: Add the API exports and hooks**

In `front/src/api/cloudflare.ts`:

1. In the first import from `'@/gen/neobox/v1/cloudflare_pb'` add `type CloudflareEmailRuleInputSchema,` next to `CloudflareDNSRecordInputSchema`.
2. In the `export { ... } from '@/gen/neobox/v1/cloudflare_pb'` block add:

```ts
  CloudflareEmailAction,
  type CloudflareEmailAddress,
  type CloudflareEmailCatchAll,
  type CloudflareEmailDNSRecord,
  type CloudflareEmailRoutingSettings,
  type CloudflareEmailRule,
  type GetCloudflareEmailRoutingResponse,
```

3. After the `CloudflareDNSRecordInit` type add:

```ts
/** A routing rule as sent on create and update. */
export type CloudflareEmailRuleInit = MessageInitShape<
  typeof CloudflareEmailRuleInputSchema
>
```

The `setCatchAll` mutation below annotates `action` as `number` (the generated enum is numeric), which avoids a second import of the enum.

4. In `const keys = { ... }` add (before the closing brace):

```ts
  emailRouting: (connectionId: string, zoneId: string) =>
    ['cloudflare', connectionId, 'email-routing', zoneId] as const,
  emailAddresses: (connectionId: string) =>
    ['cloudflare', connectionId, 'email-addresses'] as const,
```

5. After `useCloudflareWorkerChanges` add:

```ts
/** A Zone's Email Routing: status, rules and catch-all. */
export function useCloudflareEmailRouting(
  connectionId: string,
  zoneId: string
) {
  return useQuery({
    queryKey: keys.emailRouting(connectionId, zoneId),
    queryFn: async () =>
      await client.getCloudflareEmailRouting({ connectionId, zoneId }),
    retry: false,
  })
}

/** The Account's destination addresses. */
export function useCloudflareEmailAddresses(connectionId: string) {
  return useQuery({
    queryKey: keys.emailAddresses(connectionId),
    queryFn: async () =>
      await client.listCloudflareEmailAddresses({ connectionId }),
    retry: false,
  })
}

/** Every Email Routing change made from a Zone's page. */
export function useCloudflareEmailChanges(
  connectionId: string,
  zoneId: string
) {
  const change = useResourceChanges(connectionId, [
    keys.emailRouting(connectionId, zoneId),
    keys.emailAddresses(connectionId),
  ])
  const base = { connectionId, zoneId }
  return {
    enable: useMutation(
      change(() => client.enableCloudflareEmailRouting({ ...base }))
    ),
    createRule: useMutation(
      change((rule: CloudflareEmailRuleInit) =>
        client.createCloudflareEmailRule({ ...base, rule })
      )
    ),
    updateRule: useMutation(
      change((v: { ruleId: string; rule: CloudflareEmailRuleInit }) =>
        client.updateCloudflareEmailRule({ ...base, ...v })
      )
    ),
    deleteRule: useMutation(
      change((ruleId: string) =>
        client.deleteCloudflareEmailRule({ ...base, ruleId })
      )
    ),
    setCatchAll: useMutation(
      change((v: { enabled: boolean; action: number; target: string }) =>
        client.setCloudflareEmailCatchAll({ ...base, ...v })
      )
    ),
    createAddress: useMutation(
      change((email: string) =>
        client.createCloudflareEmailAddress({ connectionId, email })
      )
    ),
    deleteAddress: useMutation(
      change((addressId: string) =>
        client.deleteCloudflareEmailAddress({ ...base, addressId })
      )
    ),
  }
}
```

- [ ] **Step 4: Write the logic and log wording**

Create `front/src/features/cloudflare/email.ts`:

```ts
import {
  CloudflareEmailAction as Action,
  type CloudflareEmailAddress,
  type CloudflareEmailCatchAll,
  type CloudflareEmailDNSRecord,
  type CloudflareEmailRoutingSettings,
  type CloudflareEmailRule,
} from '@/api/cloudflare'

// Light checks that save a round trip; the backend and Cloudflare check
// again and have the last word.

const ADDRESS = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/** The problem with an email address, or undefined when it looks right. */
export function emailProblem(input: string): string | undefined {
  const a = input.trim()
  if (!a) return 'Enter the email address.'
  if (a.length > 90 || !ADDRESS.test(a))
    return 'Use an address such as name@example.com.'
}

/** The problem with the address a rule matches in a Zone, or undefined. */
export function ruleAddressProblem(
  input: string,
  zoneName: string
): string | undefined {
  const p = emailProblem(input)
  if (p) return p
  const domain = input.trim().split('@').pop() ?? ''
  if (domain.toLowerCase() !== zoneName.toLowerCase())
    return `The address must end in @${zoneName}.`
}

/** A forward needs a destination address; a drop needs none. */
export function targetProblem(
  action: Action,
  target: string
): string | undefined {
  if (action !== Action.FORWARD) return undefined
  if (emailProblem(target)) return 'Enter the destination address.'
}

/** What a rule does, in words. */
export function ruleSummary(
  r: Pick<CloudflareEmailRule, 'action' | 'target'>
): string {
  switch (r.action) {
    case Action.FORWARD:
      return `Forward to ${r.target}`
    case Action.DROP:
      return 'Drop'
    case Action.WORKER:
      return `Send to Worker ${r.target}`
  }
  return '-'
}

/** What the catch-all does, in words. */
export function catchAllSummary(c: CloudflareEmailCatchAll | undefined): string {
  if (!c) return 'Unknown'
  if (!c.enabled) return 'Off'
  return ruleSummary(c)
}

export type CatchAllMode = 'off' | 'drop' | 'forward'

export function catchAllMode(c: CloudflareEmailCatchAll): CatchAllMode {
  if (!c.enabled) return 'off'
  return c.action === Action.FORWARD ? 'forward' : 'drop'
}

/** The change request a catch-all choice stands for. */
export function catchAllRequest(
  mode: CatchAllMode,
  target: string
): { enabled: boolean; action: Action; target: string } {
  switch (mode) {
    case 'forward':
      return { enabled: true, action: Action.FORWARD, target: target.trim() }
    case 'drop':
      return { enabled: true, action: Action.DROP, target: '' }
  }
  return { enabled: false, action: Action.DROP, target: '' }
}

export function catchAllProblem(
  mode: CatchAllMode,
  target: string
): string | undefined {
  return mode === 'forward' ? emailProblem(target) : undefined
}

/** How a destination address stands, with the classes that colour its badge. */
export function addressStatus(a: Pick<CloudflareEmailAddress, 'verified'>): {
  label: string
  className: string
} {
  return a.verified
    ? { label: 'Verified', className: 'bg-success-muted text-success-foreground' }
    : {
        label: 'Waiting for verification',
        className: 'bg-warning-muted text-warning-foreground',
      }
}

/** The verified destination addresses, sorted. */
export function verifiedEmails(addresses: CloudflareEmailAddress[]): string[] {
  return addresses
    .filter((a) => a.verified)
    .map((a) => a.email)
    .sort((x, y) => x.localeCompare(y))
}

/** How Email Routing stands in a Zone, with the classes that colour its badge. */
export function routingState(s: CloudflareEmailRoutingSettings | undefined): {
  label: string
  className: string
  hint: string
} {
  if (!s)
    return { label: 'Loading', className: 'bg-muted text-muted-foreground', hint: '' }
  if (!s.enabled)
    return {
      label: 'Not enabled',
      className: 'bg-muted text-muted-foreground',
      hint: 'Enabling adds the MX and SPF records Cloudflare needs, and locks them.',
    }
  if (s.status === 'ready')
    return {
      label: 'Active',
      className: 'bg-success-muted text-success-foreground',
      hint: 'Mail to this zone is routed by the rules below.',
    }
  return {
    label: 'Needs attention',
    className: 'bg-warning-muted text-warning-foreground',
    hint: `Cloudflare reports the status "${s.status}". Check the DNS records in the Cloudflare dashboard.`,
  }
}

/** One required DNS record on a line. */
export function dnsRecordSummary(r: CloudflareEmailDNSRecord): string {
  const base = `${r.type} ${r.name} → ${r.content}`
  return r.type === 'MX' ? `${base} (priority ${r.priority})` : base
}
```

In `front/src/features/cloudflare/manage.ts`, add to `actionLabels` (before the closing brace):

```ts
  enable_routing: 'Enabled Email Routing',
  create_rule: 'Added routing rule',
  update_rule: 'Changed routing rule',
  enable_rule: 'Enabled routing rule',
  disable_rule: 'Disabled routing rule',
  delete_rule: 'Deleted routing rule',
  set_catch_all: 'Changed catch-all',
  create_address: 'Added destination address',
  delete_address: 'Deleted destination address',
```

- [ ] **Step 5: Run the tests**

Run: `cd front && npx vitest run 2>&1 | tail -15 && npx tsc -b 2>&1 | head`
Expected: all test files pass; `tsc` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add front/src/api/cloudflare.ts front/src/features/cloudflare
git commit -m "feat(front): Email Routing hooks, input checks and log wording"
```

---

### Task 5: The Email routing tab

**Files:**
- Create: `front/src/features/cloudflare/email-routing-tab.tsx`, `email-rules.tsx`, `email-addresses.tsx`
- Modify: `front/src/features/cloudflare/zone-page.tsx` (imports, `TabsList` ~line 126-129, `TabsContent` ~line 138-141)

**Interfaces:**
- Consumes: Task 4's hooks and `email.ts`; existing `FormDialog`, `ConfirmChangeDialog`, `Field` (`manage-dialogs.tsx`), `toastChange`, `toastFailure` (`changed.ts`), `changeError` (`access.ts`), `ProblemAlert` (`list-parts.tsx`), `ResourceOperationsLog` (`resource-operations.tsx`), `DataTable`/`Column`, shadcn `Badge`, `Button`, `Switch`, `Select`, `Skeleton`, `Alert`, `cn` from `@/lib/utils`.
- Produces: `EmailRoutingTab({connectionId, zoneId, zoneName})`, and inside it `RulesSection`, `AddressesSection`.

First read the signature of `ProblemAlert` in `front/src/features/cloudflare/list-parts.tsx` (props `error`, optional `permission`) and `DataTable` in `front/src/components/data-table.tsx` (props `columns`, `data`, `isLoading`, `emptyMessage`) and adjust the code below if they differ.

- [ ] **Step 1: Write the addresses section**

Create `front/src/features/cloudflare/email-addresses.tsx`:

```tsx
import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  useCloudflareEmailChanges,
  type CloudflareEmailAddress,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange } from './changed'
import { addressStatus, emailProblem } from './email'
import { ConfirmChangeDialog, FormDialog } from './manage-dialogs'
import { ProblemAlert } from './list-parts'

/**
 * The Account's destination addresses, shared by all its Zones. A new one
 * has to be verified by following the link Cloudflare mails to it.
 */
export function AddressesSection({
  connectionId,
  zoneId,
  addresses,
  error,
  loading,
}: {
  connectionId: string
  zoneId: string
  addresses: CloudflareEmailAddress[] | undefined
  error: unknown
  loading: boolean
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState<CloudflareEmailAddress>()

  const columns: Column<CloudflareEmailAddress>[] = [
    {
      header: 'Address',
      cell: (a) => <span className='font-mono text-xs'>{a.email}</span>,
    },
    {
      header: 'Status',
      cell: (a) => {
        const s = addressStatus(a)
        return <Badge className={cn(s.className)}>{s.label}</Badge>
      },
    },
    {
      header: 'Actions',
      cell: (a) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setDeleting(a)}
          aria-label={`Delete ${a.email}`}
        >
          <Trash2 className='h-3.5 w-3.5' />
        </Button>
      ),
    },
  ]

  return (
    <section className='space-y-2'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
          <h3 className='text-sm font-medium'>Destination addresses</h3>
          <p className='text-xs text-muted-foreground'>
            Shared by every zone of this account. A rule can forward only to a
            verified address.
          </p>
        </div>
        <Button size='sm' onClick={() => setAdding(true)}>
          <Plus className='h-4 w-4' /> Add address
        </Button>
      </div>
      {error ? (
        <ProblemAlert
          error={error}
          permission='Email Routing Addresses · Read'
        />
      ) : (
        <DataTable
          columns={columns}
          data={addresses}
          isLoading={loading}
          emptyMessage='No destination addresses'
        />
      )}
      {adding && (
        <FormDialog
          title='Add a destination address'
          description='Cloudflare emails a verification link to this address. It can be used in a rule once the link is followed.'
          submitLabel='Add address'
          pendingLabel='Adding…'
          what='destination addresses'
          fields={[
            {
              name: 'email',
              label: 'Email address',
              placeholder: 'me@gmail.com',
              mono: true,
              check: emailProblem,
            },
          ]}
          run={async (v) => {
            const res = await changes.createAddress.mutateAsync(v.text.email)
            toastChange(
              `Added ${v.text.email}; a verification email was sent`,
              res.result
            )
          }}
          onClose={() => setAdding(false)}
        />
      )}
      {deleting && (
        <ConfirmChangeDialog
          title='Delete this destination address?'
          description={
            <>
              <span className='font-mono'>{deleting.email}</span> is removed
              from the account. It is refused while a rule or the catch-all of
              this zone still forwards to it; rules of other zones are not
              checked.
            </>
          }
          confirmLabel='Delete address'
          pendingLabel='Deleting…'
          what='destination addresses'
          run={async () => {
            const res = await changes.deleteAddress.mutateAsync(deleting.id)
            toastChange(`Deleted ${deleting.email}`, res.result)
          }}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </section>
  )
}
```

- [ ] **Step 2: Write the rules section**

Create `front/src/features/cloudflare/email-rules.tsx`:

```tsx
import { useState } from 'react'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import {
  CloudflareEmailAction as Action,
  useCloudflareEmailChanges,
  type CloudflareEmailRule,
} from '@/api/cloudflare'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange, toastFailure } from './changed'
import { ruleAddressProblem, ruleSummary, targetProblem } from './email'
import { ConfirmChangeDialog, Field, FormDialog } from './manage-dialogs'

/** A Zone's routing rules: switch, edit and delete the ones that can be. */
export function RulesSection({
  connectionId,
  zoneId,
  zoneName,
  rules,
  verified,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  rules: CloudflareEmailRule[]
  /** The verified destination addresses, for the form's hint. */
  verified: string[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [editing, setEditing] = useState<CloudflareEmailRule | 'new'>()
  const [deleting, setDeleting] = useState<CloudflareEmailRule>()

  function toggle(r: CloudflareEmailRule, enabled: boolean) {
    changes.updateRule.mutate(
      {
        ruleId: r.id,
        rule: {
          name: r.name,
          address: r.address,
          action: r.action,
          target: r.target,
          enabled,
        },
      },
      {
        onSuccess: (res) =>
          toastChange(
            enabled ? `${r.address} is on` : `${r.address} is off`,
            res.result
          ),
        onError: (err) => toastFailure(err, 'routing rules'),
      }
    )
  }

  const busy = (r: CloudflareEmailRule) =>
    changes.updateRule.isPending && changes.updateRule.variables?.ruleId === r.id

  const columns: Column<CloudflareEmailRule>[] = [
    {
      header: 'Custom address',
      cell: (r) => (
        <div>
          <div className='font-mono text-xs'>{r.address}</div>
          {r.name && (
            <div className='text-xs text-muted-foreground'>{r.name}</div>
          )}
        </div>
      ),
    },
    {
      header: 'Action',
      cell: (r) => <span className='text-sm'>{ruleSummary(r)}</span>,
    },
    {
      header: 'On',
      cell: (r) => (
        <Switch
          checked={r.enabled}
          disabled={!r.editable || busy(r)}
          onCheckedChange={(v) => toggle(r, v)}
          aria-label={`Rule for ${r.address}`}
        />
      ),
    },
    {
      header: 'Actions',
      cell: (r) =>
        r.editable ? (
          <div className='flex gap-1'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setEditing(r)}
              aria-label={`Edit the rule for ${r.address}`}
            >
              <Pencil className='h-3.5 w-3.5' />
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setDeleting(r)}
              aria-label={`Delete the rule for ${r.address}`}
            >
              <Trash2 className='h-3.5 w-3.5' />
            </Button>
          </div>
        ) : (
          <span
            className='text-xs text-muted-foreground'
            title={r.readonlyReason}
          >
            Read-only
          </span>
        ),
    },
  ]

  return (
    <section className='space-y-2'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-sm font-medium'>Routing rules</h3>
        <Button size='sm' onClick={() => setEditing('new')}>
          <Plus className='h-4 w-4' /> Add rule
        </Button>
      </div>
      <DataTable
        columns={columns}
        data={rules}
        emptyMessage='No routing rules'
      />
      {editing && (
        <RuleDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zoneName}
          rule={editing === 'new' ? undefined : editing}
          verified={verified}
          onClose={() => setEditing(undefined)}
        />
      )}
      {deleting && (
        <ConfirmChangeDialog
          title='Delete this routing rule?'
          description={
            <>
              Mail to <span className='font-mono'>{deleting.address}</span> is
              no longer routed by a rule.
            </>
          }
          confirmLabel='Delete rule'
          pendingLabel='Deleting…'
          what='routing rules'
          run={async () => {
            const res = await changes.deleteRule.mutateAsync(deleting.id)
            toastChange(`Deleted the rule for ${deleting.address}`, res.result)
          }}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </section>
  )
}

/** Adds a rule, or changes one. The action is chosen in the form's body. */
function RuleDialog({
  connectionId,
  zoneId,
  zoneName,
  rule,
  verified,
  onClose,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  rule?: CloudflareEmailRule
  verified: string[]
  onClose: () => void
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [action, setAction] = useState<Action>(
    rule?.action === Action.DROP ? Action.DROP : Action.FORWARD
  )
  const forward = action === Action.FORWARD
  return (
    <FormDialog
      title={rule ? 'Edit routing rule' : 'Add a routing rule'}
      description={`Mail sent to the custom address at ${zoneName} is handled by this rule.`}
      submitLabel={rule ? 'Save rule' : 'Add rule'}
      what='routing rules'
      fields={[
        {
          name: 'address',
          label: 'Custom address',
          placeholder: `info@${zoneName}`,
          initial: rule?.address,
          mono: true,
          check: (v) => ruleAddressProblem(v, zoneName),
        },
        {
          name: 'target',
          label: 'Destination address',
          initial: rule?.action === Action.FORWARD ? rule.target : '',
          mono: true,
          disabled: !forward,
          hint: forward
            ? verified.length > 0
              ? `Verified: ${verified.join(', ')}`
              : 'No verified destination address yet; add one below first.'
            : 'A dropped message is discarded, so there is no destination.',
          check: (v) => targetProblem(action, v),
        },
        {
          name: 'name',
          label: 'Name (optional)',
          initial: rule?.name,
        },
      ]}
      toggles={[{ name: 'enabled', label: 'On', initial: rule?.enabled ?? true }]}
      run={async (v) => {
        const input = {
          name: v.text.name,
          address: v.text.address,
          action,
          target: forward ? v.text.target : '',
          enabled: v.on.enabled,
        }
        const res = rule
          ? await changes.updateRule.mutateAsync({ ruleId: rule.id, rule: input })
          : await changes.createRule.mutateAsync(input)
        toastChange(rule ? 'Rule saved' : 'Rule added', res.result)
      }}
      onClose={onClose}
    >
      <Field label='Action' id='rule-action'>
        <Select
          value={String(action)}
          onValueChange={(v) => setAction(Number(v) as Action)}
        >
          <SelectTrigger id='rule-action' className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={String(Action.FORWARD)}>
              Forward to a destination address
            </SelectItem>
            <SelectItem value={String(Action.DROP)}>Drop</SelectItem>
          </SelectContent>
        </Select>
      </Field>
    </FormDialog>
  )
}
```

- [ ] **Step 3: Write the tab**

Create `front/src/features/cloudflare/email-routing-tab.tsx`:

```tsx
import { useState } from 'react'
import { CircleAlert, Mail } from 'lucide-react'
import {
  CloudflareResourceKind,
  CloudflareEmailAction as Action,
  useCloudflareEmailAddresses,
  useCloudflareEmailChanges,
  useCloudflareEmailRouting,
  type CloudflareEmailCatchAll,
  type CloudflareEmailDNSRecord,
  type CloudflareEmailRoutingSettings,
  type CloudflareReadIssue,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { changeError } from './access'
import { toastChange } from './changed'
import {
  catchAllMode,
  catchAllProblem,
  catchAllRequest,
  catchAllSummary,
  dnsRecordSummary,
  routingState,
  verifiedEmails,
  type CatchAllMode,
} from './email'
import { AddressesSection } from './email-addresses'
import { RulesSection } from './email-rules'
import { ProblemAlert } from './list-parts'
import { ConfirmChangeDialog } from './manage-dialogs'
import { ResourceOperationsLog } from './resource-operations'

/**
 * A Zone's Email Routing: whether it is on, the routing rules, the
 * catch-all, the Account's destination addresses and the log of changes.
 */
export function EmailRoutingTab({
  connectionId,
  zoneId,
  zoneName,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
}) {
  const routing = useCloudflareEmailRouting(connectionId, zoneId)
  const addresses = useCloudflareEmailAddresses(connectionId)

  if (routing.error) {
    return (
      <ProblemAlert
        error={routing.error}
        permission='Email Routing Rules · Read'
      />
    )
  }
  const data = routing.data
  if (!data) return <Skeleton className='h-24 w-full' />
  const enabled = data.settings?.enabled ?? false
  const catchAll = data.catchAll

  return (
    <div className='space-y-6'>
      <StatusSection
        connectionId={connectionId}
        zoneId={zoneId}
        zoneName={zoneName}
        settings={data.settings}
        records={data.requiredDnsRecords}
        issues={data.issues}
      />
      {enabled && (
        <>
          {catchAll && (
            <CatchAllSection
              key={`${catchAll.enabled}-${catchAll.action}-${catchAll.target}`}
              connectionId={connectionId}
              zoneId={zoneId}
              catchAll={catchAll}
              verified={verifiedEmails(addresses.data?.addresses ?? [])}
            />
          )}
          <RulesSection
            connectionId={connectionId}
            zoneId={zoneId}
            zoneName={zoneName}
            rules={data.rules}
            verified={verifiedEmails(addresses.data?.addresses ?? [])}
          />
        </>
      )}
      <AddressesSection
        connectionId={connectionId}
        zoneId={zoneId}
        addresses={addresses.data?.addresses}
        error={addresses.error}
        loading={addresses.isLoading}
      />
      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>Changes made from Neo Box</h3>
        <ResourceOperationsLog
          connectionId={connectionId}
          kind={CloudflareResourceKind.EMAIL_ROUTING}
          resource=''
        />
      </section>
    </div>
  )
}

function StatusSection({
  connectionId,
  zoneId,
  zoneName,
  settings,
  records,
  issues,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  settings: CloudflareEmailRoutingSettings | undefined
  records: CloudflareEmailDNSRecord[]
  issues: CloudflareReadIssue[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [enabling, setEnabling] = useState(false)
  const enabled = settings?.enabled ?? false
  const state = routingState(settings)

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-3 rounded-md border p-4'>
        <div className='space-y-1'>
          <div className='flex items-center gap-2'>
            <Mail className='h-4 w-4' />
            <span className='font-medium'>Email Routing</span>
            <Badge className={cn(state.className)}>{state.label}</Badge>
          </div>
          <p className='text-sm text-muted-foreground'>{state.hint}</p>
        </div>
        {!enabled && (
          <Button onClick={() => setEnabling(true)}>Enable Email Routing</Button>
        )}
      </div>
      {issues.map((i) => (
        <Alert key={i.source}>
          <CircleAlert />
          <AlertTitle>Could not read the {i.source}</AlertTitle>
          <AlertDescription>{i.message}</AlertDescription>
        </Alert>
      ))}
      {enabling && (
        <ConfirmChangeDialog
          title={`Enable Email Routing for ${zoneName}?`}
          description='Cloudflare adds these DNS records and locks them. Mail to this zone then goes to Cloudflare, so any other mail service on the zone stops receiving it.'
          confirmLabel='Enable'
          pendingLabel='Enabling…'
          destructive={false}
          what='Email Routing settings'
          run={async () => {
            const res = await changes.enable.mutateAsync()
            toastChange('Email Routing is enabled', res.result)
          }}
          onClose={() => setEnabling(false)}
        >
          {records.length > 0 && (
            <ul className='space-y-1 rounded-md border p-3 font-mono text-xs break-all'>
              {records.map((r) => (
                <li key={`${r.type}-${r.content}`}>{dnsRecordSummary(r)}</li>
              ))}
            </ul>
          )}
        </ConfirmChangeDialog>
      )}
    </div>
  )
}

function CatchAllSection({
  connectionId,
  zoneId,
  catchAll,
  verified,
}: {
  connectionId: string
  zoneId: string
  catchAll: CloudflareEmailCatchAll
  verified: string[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const initial = catchAllMode(catchAll)
  const initialTarget = catchAll.action === Action.FORWARD ? catchAll.target : ''
  const [mode, setMode] = useState<CatchAllMode>(initial)
  const [target, setTarget] = useState(initialTarget)
  const [problem, setProblem] = useState<string>()
  const [failure, setFailure] = useState<string>()

  const dirty =
    mode !== initial || (mode === 'forward' && target.trim() !== initialTarget)

  function save() {
    const p = catchAllProblem(mode, target)
    setProblem(p)
    if (p) return
    setFailure(undefined)
    changes.setCatchAll.mutate(catchAllRequest(mode, target), {
      onSuccess: (res) => toastChange('Catch-all saved', res.result),
      onError: (err) => setFailure(changeError(err, 'catch-all rule').message),
    })
  }

  return (
    <section className='space-y-2'>
      <h3 className='text-sm font-medium'>Catch-all</h3>
      <div className='space-y-3 rounded-md border p-4'>
        <p className='text-sm text-muted-foreground'>
          What happens to mail for addresses no rule matches. Now:{' '}
          <span className='font-medium text-foreground'>
            {catchAllSummary(catchAll)}
          </span>
        </p>
        {catchAll.editable ? (
          <div className='flex flex-col gap-2 sm:flex-row sm:items-start'>
            <Select
              value={mode}
              onValueChange={(v) => setMode(v as CatchAllMode)}
            >
              <SelectTrigger className='w-full sm:w-48' aria-label='Catch-all'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='off'>Off</SelectItem>
                <SelectItem value='drop'>Drop</SelectItem>
                <SelectItem value='forward'>Forward to</SelectItem>
              </SelectContent>
            </Select>
            {mode === 'forward' && (
              <div className='w-full space-y-1 sm:max-w-sm'>
                <Input
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                  placeholder='me@gmail.com'
                  className='font-mono'
                  aria-label='Catch-all destination address'
                  autoComplete='off'
                />
                <p className='text-xs text-muted-foreground'>
                  {verified.length > 0
                    ? `Verified: ${verified.join(', ')}`
                    : 'No verified destination address yet.'}
                </p>
              </div>
            )}
            <Button
              onClick={save}
              disabled={!dirty || changes.setCatchAll.isPending}
            >
              {changes.setCatchAll.isPending ? 'Saving…' : 'Save'}
            </Button>
          </div>
        ) : (
          <p className='text-xs text-muted-foreground'>
            Read-only. {catchAll.readonlyReason}
          </p>
        )}
        {problem && <p className='text-xs text-danger-foreground'>{problem}</p>}
        {failure && (
          <p className='text-sm break-words text-danger-foreground'>{failure}</p>
        )}
      </div>
    </section>
  )
}
```

- [ ] **Step 4: Mount the tab in the Zone page**

In `front/src/features/cloudflare/zone-page.tsx`:
- add `import { EmailRoutingTab } from './email-routing-tab'` next to the other `./` imports;
- add `<TabsTrigger value='email'>Email routing</TabsTrigger>` between the `records` and `operations` triggers;
- add, between the `records` and `operations` `TabsContent`:

```tsx
              <TabsContent value='email' className='pt-2'>
                <EmailRoutingTab
                  connectionId={connectionId}
                  zoneId={zoneId}
                  zoneName={zone.data?.name ?? ''}
                />
              </TabsContent>
```

(Radix mounts a tab's content only while it is open, so Email Routing is read only when the user opens the tab.)

- [ ] **Step 5: Verify**

Run:
```bash
cd front && npx tsc -b 2>&1 | head -20 && npm run lint 2>&1 | tail -20 && npm run format -- --log-level warn && npm run format:check 2>&1 | tail -3 && npx vitest run 2>&1 | tail -5 && npm run build 2>&1 | tail -5
```
Expected: no TypeScript or lint errors, format check clean, all tests pass, build succeeds.

Manual check (needs PostgreSQL and a Cloudflare connection; do it if available): `go run ./cmd/neobox` and `cd front && npm run dev`, open a Zone, open "Email routing". With a token lacking Email Routing permissions the tab shows the permission alert (not a page error); with the permissions, enable → add a destination address → verify it from the mailbox → add a rule → toggle it → set the catch-all → delete. The log at the bottom shows each change. If no real token is at hand, say so in the final report rather than claiming it was checked.

- [ ] **Step 6: Commit**

```bash
git add front
git commit -m "feat(front): Email routing tab on the Zone page"
```

---

### Task 6: Token permission list and README

**Files:**
- Modify: `front/src/features/cloudflare/format.ts` (`TOKEN_PERMISSIONS`), `README.md` (Cloudflare section)

- [ ] **Step 1: Add the permissions to the token list**

In `TOKEN_PERMISSIONS` add:

```ts
  {
    scope: 'Zone',
    permission: 'Email Routing Rules · Read',
    unlocks: 'the Email routing tab',
  },
  {
    scope: 'Zone',
    permission: 'Email Routing Rules · Edit',
    unlocks: 'enabling Email Routing, and changing rules and the catch-all',
  },
  {
    scope: 'Account',
    permission: 'Email Routing Addresses · Read',
    unlocks: 'destination addresses',
  },
  {
    scope: 'Account',
    permission: 'Email Routing Addresses · Edit',
    unlocks: 'adding and deleting destination addresses',
  },
```

- [ ] **Step 2: Document it in the README**

Read the Cloudflare section of `README.md` (starts near line 320; the Tunnels paragraph is near line 368) and add after the last feature paragraph of that section:

```markdown
**Email Routing.** A Zone's page has an Email routing tab. It shows whether
Email Routing is on and, if not, enables it (Cloudflare adds and locks the MX
and SPF records, which sends the zone's mail to Cloudflare; the dialog lists
them first). Rules forward mail for one address to a verified destination
address, or drop it; the catch-all handles every other address and can be off,
drop or forward. Destination addresses belong to the account, so one address
serves all zones: a new one is mailed a link by Cloudflare and works once it is
followed. Neo Box never turns Email Routing off, and it shows rules that send
to a Worker, or that a Worker's wrangler configuration owns, read-only.
Deleting a destination address is refused while a rule or the catch-all of the
zone you are in still forwards to it; rules of other zones are not checked.
Every change is in the tab's log. The token needs the four Email Routing
permissions listed above (Zone: Rules, Account: Addresses; Read and Edit).
```

- [ ] **Step 3: Verify and commit**

Run: `cd front && npm run format:check 2>&1 | tail -3 && npx tsc -b`
Expected: clean.

```bash
git add front/src/features/cloudflare/format.ts README.md
git commit -m "docs: Email Routing token permissions and README"
```

---

### Task 7: Domain docs, ADR, agent guide, spec amendments

**Files:**
- Modify: `CONTEXT.md` (Cloudflare section, after the **Worker** / **Pages project** bullet ending near line 175), `AGENTS.md` (`CLAUDE.md` is a symlink to it; edit `AGENTS.md` only), `docs/specs/cloudflare-email-routing-v1.md`
- Create: `docs/adr/0011-email-routing-reuses-the-resource-operation-log.md`

- [ ] **Step 1: CONTEXT.md**

After the **Worker** / **Pages project** bullet add:

```markdown
- **Email Routing** — a Zone's inbound mail routing at Cloudflare, read live.
  Neo Box can *enable* it (Cloudflare adds and locks the MX and SPF records,
  which sends the Zone's mail to Cloudflare) but never disables it.
- **Destination address** — an address of the Connection's account that mail
  can be forwarded to. It belongs to the account, not to a Zone, and is
  *verified* once its owner follows the link Cloudflare mails. A forward needs
  a verified one. Deleting one is refused while a rule or the catch-all of the
  Zone being managed still forwards to it; other Zones' rules are not checked.
- **Routing rule** — a Zone's rule that matches mail to one custom address and
  forwards it to a destination address or drops it. It can be switched on and
  off. A rule that sends to a Worker, or is owned by a Worker's wrangler
  configuration, is shown but not changed.
- **Catch-all** — the Zone's rule for mail no routing rule matches: off, drop,
  or forward. Switching it off keeps its action.
- **Email Routing operation** — a change to Email Routing started from Neo Box
  (enable, rule, catch-all, destination address), logged like a Worker or
  Pages change before it is sent, as an `email_routing` resource operation
  (Resource: the Zone's name, or "destination addresses").
```

- [ ] **Step 2: ADR**

Create `docs/adr/0011-email-routing-reuses-the-resource-operation-log.md`:

```markdown
# ADR 0011: Email Routing reuses the resource operation log and never disables

Email Routing changes are logged in `cloudflare_resource_operations` as a new
`ResourceKind`, `email_routing`, instead of a new table. They need exactly what
Worker and Pages changes need: a pending record stored before the change is
sent (no record, nothing sent), one send, an outcome that becomes succeeded,
failed or unknown (an unconfirmed write is never sent again), and no secrets in
the payload. `kind` is a plain string column, so adding the kind changes no
schema. A separate table would only copy `operate`.

The `Resource` of a Zone's rule, catch-all or enable operation is the Zone's
name; destination addresses belong to the account, so their operations use the
fixed resource "destination addresses". The tab therefore lists every
`email_routing` operation of the connection, not only the open Zone's.

Neo Box does not disable Email Routing. Disabling deletes the MX records and
stops inbound mail, which is hard to notice and slow to recover from because
resolvers cache the change. Enabling is offered behind a confirmation that lists
the records Cloudflare will add. If disabling is wanted later, it needs its own
confirmation that names the records it removes.

Rules that send to a Worker, or that a Worker's wrangler configuration owns
(`source` other than `api`), are shown read-only: editing them here would fight
the configuration that owns them. A forward requires a verified destination
address; Neo Box checks that before sending, because Cloudflare would refuse it
and a refusal is logged as a failed operation for nothing.

Deleting a destination address checks only the rules and catch-all of the Zone
the request names. Checking every Zone's rules would be one rules read and one
catch-all read per Zone, too slow for an account with many Zones; the other
Zones' rules are left to Cloudflare's own answer, and the UI says so.
```

- [ ] **Step 3: AGENTS.md**

In the Backend layers list add two bullets after the `internal/cloudflareacct/` bullet:

```markdown
- `internal/cloudflare/email.go` — Email Routing client: Zone settings and the
  DNS it needs, rules, catch-all, and the Account's destination addresses
  (reads retry; writes never resent), plus the rule input checks
  (`ValidateEmailRuleInput`), `ReadOnlyReason` and `Same`. The fake lives in
  `cftest/email.go`.
- `internal/cloudflareacct/email.go` — Email Routing in the Manager: the live
  read (`EmailRouting`), enable (never disable), rule / catch-all / destination
  address changes. Every write goes through `operate` as an `email_routing`
  operation (`docs/adr/0011`); forwards need a verified address; rules sending
  to a Worker or owned by wrangler are refused; deleting an address checks the
  named Zone's rules and catch-all. Locks: `email/<acc>/<zone>` and
  `email/<acc>/addresses`.
```

In the **Cloudflare** service paragraph, after the sentence about Pages management, add: `Email Routing (GetCloudflareEmailRouting, ListCloudflareEmailAddresses, EnableCloudflareEmailRouting, rule create / update / delete, SetCloudflareEmailCatchAll, destination address create / delete) also returns CloudflareChangeResult; there is no disable.` and extend the Frontend sentence listing the Cloudflare files with `email.ts` and mention `email-routing-tab.tsx`.

- [ ] **Step 4: Spec amendments**

In `docs/specs/cloudflare-email-routing-v1.md`:
- In **Scope → Out**, change the Worker bullet to: `Rules whose action is "send to Worker", or that a Worker's wrangler configuration owns (\`source\` other than \`api\`). They are shown read-only and cannot be edited or created here.`
- In **Backend → cloudflareacct**, replace the destination-address deletion bullet with: `Deleting a destination address that a rule or the catch-all of the **Zone named in the request** (\`zone_id\`) still forwards to is refused with \`FailedPrecondition\` naming the rule. Rules of other Zones are not checked (one rules read per Zone is too slow); Cloudflare's own answer applies.` and add `Worker-action and wrangler-owned rules` where the spec says "Worker-action rules".
- In **Proto**, change `DeleteCloudflareEmailAddress` to mention that it takes `zone_id`.
- In **Decisions**, add: `Destination-address operations are logged with Resource "destination addresses"; the tab's log lists all \`email_routing\` operations of the connection.`
- Change the status line to `Status: design approved 2026-10-16; implemented per docs/superpowers/plans/2026-10-16-cloudflare-email-routing.md.`

- [ ] **Step 5: Verify and commit**

Run: `cd front && npm run format:check 2>&1 | tail -3` (prettier also checks Markdown under `front/` only; the docs are outside it). Re-read the new CONTEXT.md and ADR text once for contradictions with the code (action names, resource strings).

```bash
git add CONTEXT.md AGENTS.md docs
git commit -m "docs: Email Routing domain terms, ADR 0011 and agent guide"
```

---

### Task 8: Final verification

- [ ] **Step 1: Run everything**

```bash
make buf && git diff --stat   # generated code must already be committed: no diff
go build ./... && go vet ./... && go test ./...
golangci-lint run --config=.golangci.yml
buf lint && buf format --diff --exit-code
cd front && npm run lint && npm run format:check && npx tsc -b && npx vitest run && npm run build
```
Expected: no diff after `make buf`; every command succeeds. If the repository tests were skipped (no `NEOBOX_TEST_POSTGRES_DSN`), say so; they do not touch this feature (no schema change).

- [ ] **Step 2: Spec coverage check**

Walk the spec's sections and point to the code: enable (Task 3 `EnableEmailRouting`, test `...EnableAndRead`), rules CRUD + enable / disable (`...Rules`), catch-all (`...CatchAll`), destination addresses and in-use refusal (`...Addresses`), Worker / wrangler read-only (`...Rules`), unverified forward refused with no send (`...Rules`), permissions (`...Permissions`), unconfirmed write logged unknown (`...UnconfirmedWrite...`), Zone of another account not found (`...EnableAndRead`), no disable RPC (proto), tab and enable confirmation listing the records (Task 5), Vitest for `email.ts` (Task 4), docs (Tasks 6-7). Fix any gap before finishing.

- [ ] **Step 3: Report**

Summarize what was built, what was verified (commands and results), and what was not (live Cloudflare, since no token was available, unless it was tested).
