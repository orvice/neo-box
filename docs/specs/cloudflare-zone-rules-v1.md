# Cloudflare Zone Rules v1

Status: scope approved in the design interview on 2026-11-06; specification only, not implemented.

## Problem Statement

Neo Box manages Cloudflare Connections, Zones, DNS records, Workers, Pages,
Tunnels and Email Routing, but cannot manage a Zone's HTTP redirect, transform
or origin rules. The User must leave Neo Box to inspect and maintain those
rules, and changes made there do not appear in Neo Box's resource operation log.

The User wants to manage these rules alongside a Zone's other resources,
including existing rules created outside Neo Box, without losing unfamiliar
configuration or silently overwriting concurrent changes.

## Solution

Add a Rules tab to each Zone's detail page with five independent categories:

| Category | v1 actions |
| --- | --- |
| Single Redirects | Static or expression target; 301, 302, 307 or 308; preserve-query-string option |
| URL Rewrite Rules | Independently keep, statically rewrite or dynamically rewrite the path and query string |
| Request Header Transform Rules | Set a static value, set an expression value or remove a header; multiple modifications per rule |
| Response Header Transform Rules | Set or add a static/expression value, or remove a header; multiple modifications per rule |
| Origin Rules | Independently override destination port, Host header, SNI and DNS resolution target |

The tab reads live configuration from Cloudflare and supports creating,
editing, enabling, disabling, deleting and ordering supported rules within
each category. Every mutation has a confirmation and takes effect when
Cloudflare accepts it. New rules default to enabled, with an explicit option
to disable them before submission.

Use Cloudflare product names in technical contracts. "Zone Rules" is the
feature's umbrella name; it is distinct from DNS records, Worker routes,
Tunnel public hostnames and Email Routing rules.

## User Stories

1. As the User, I want a Rules tab on a Zone, so that I can manage HTTP rules
   where I already manage that domain.
2. As the User, I want all five categories to load independently, so that a
   permission problem in one category does not hide the others.
3. As the User, I want to see existing rules in their current order, with their
   identifiers, descriptions, expressions, actions and enabled state, so that
   I can understand the Zone's live configuration.
4. As the User, I want to create static and dynamic redirects with a status
   code and query-string policy, so that I can redirect domains and URLs.
5. As the User, I want to rewrite a path and query string independently,
   so that I can change the upstream request URL without redirecting the visitor.
6. As the User, I want to set or remove multiple request headers in one rule,
   so that I can configure what the origin receives.
7. As the User, I want to set, add or remove response headers, so that I can
   configure what the visitor receives.
8. As the User, I want to override origin connection settings independently,
   so that requests can reach the intended origin.
9. As the User, I want a form for an exact hostname and optional exact or
   prefix path match, so that I can express common conditions quickly.
10. As the User, I want to edit a Cloudflare matching expression directly,
    so that I can use conditions the common form does not represent.
11. As the User, I want switching editing modes to preserve my expression,
    so that changing the editor does not silently change rule behavior.
12. As the User, I want matching all requests to be an explicit choice,
    so that an empty form does not accidentally affect the entire Zone.
13. As the User, I want to edit supported rules created in Cloudflare,
    so that I can maintain existing configuration from Neo Box.
14. As the User, I want to enable or disable a rule without deleting it,
    so that I can temporarily suspend its behavior.
15. As the User, I want to delete rules after confirmation, so that I can
    remove configuration I no longer need.
16. As the User, I want to adjust order within a category, so that I can
    control the sequence exposed by Cloudflare.
17. As the User, I want to see the proposed change before submission,
    so that I can check its match, action, enabled state and order.
18. As the User, I want unsupported configurations to remain visible and
    protected from writes, so that using Neo Box does not discard them.
19. As the User, I want stale edits to be rejected while preserving my input,
    so that I can reconcile changes made in another tab or the Cloudflare dashboard.
20. As the User, I want permission, entitlement, quota and input errors to be
    distinguished when possible, so that I know how to correct a rejected change.
21. As the User, I want every submitted mutation logged with its outcome,
    so that I can inspect what Neo Box requested and what Cloudflare confirmed.
22. As the User, I want an uncertain outcome to tell me to refresh and verify,
    so that I do not accidentally submit the same change twice.
23. As the User, I want live reads scoped to the Connection's Account and my
    ownership, so that a broader API token cannot expose unrelated resources.
24. As the User, I want successful saves to distinguish accepted configuration
    from verified traffic behavior, so that I understand what was checked.

## Implementation Decisions

### Architecture and contracts

- Follow the existing Cloudflare client → Account manager → Connect service
  → typed frontend client/hooks → feature UI structure. Reuse the existing
  Zone page, confirmation dialogs, error classification and operation log.
- Require an owned Cloudflare Connection and a Zone belonging to its Account
  for every read and mutation. Recheck ownership on the server.
- Use Cloudflare's Zone-level Rulesets capabilities for these five categories.
  Confirm phase names, endpoint semantics, writable fields and permission
  names against official documentation during implementation. Do not use
  legacy Page Rules or invent one generic writable JSON interface.
- Use one request and response per RPC. Mutations return the existing
  Cloudflare change result, including operation information and a separate
  outcome-log error. Contracts carry category, rule identity, expression,
  description, enabled state, typed action configuration, read-only reason
  and the concurrency precondition needed for the operation.
- Carry unsupported original configuration as display data only. Server-side
  validation must enforce read-only status; the UI is not the authority.
- Cloudflare remains the source of truth. Persist operation history, not a
  local desired-state copy of rules. Reuse resource operations with a distinct
  Zone Rules resource kind; adding that kind does not require a new log table.
- Rules permissions remain optional for Connection verification. A missing
  permission in this feature must not make otherwise usable features unavailable.

### Matching and actions

- The common matcher supports one exact hostname and an optional path match
  (exact or prefix), joined with AND. Matching all requests is a separate,
  explicit option. Generate correctly escaped expressions.
- Opening advanced mode generates the equivalent matching expression.
  Return to the common form only when the expression can be represented
  without changing its meaning; otherwise keep advanced mode and its text.
- Supported action configuration remains editable when the matching
  expression requires advanced mode. The action table above is the complete
  v1 action scope.
- Validate known structural errors locally and on the server. Cloudflare
  remains the authority on expression validity, account entitlements and
  action restrictions. Do not implement a complete expression interpreter.
- A rule with unsupported writable action/configuration fields is read-only:
  show its original configuration and the reason, and reject editing,
  enable/disable, deletion and movement on the server.
- Distinguish Cloudflare response metadata from unsupported writable fields;
  ordinary metadata must not make all rules read-only.
- Preserve unknown configuration in every read-modify-write. Never replace
  an unfamiliar rule with a reduced model. If a safe write cannot preserve
  the surrounding configuration, refuse it before sending.
- Sorting must not indirectly move a protected rule relative to other rules.
  Refuse movements crossing a read-only rule; supported rules can be reordered
  within the intervening groups. Preserve all untouched rules and their order.
- Origin DNS resolution overrides change the rule only; they never create or
  modify DNS records. Advanced expressions are still bound to the selected Zone.

### Submission, concurrency and outcomes

- Opening an editor or confirmation does not write. Before every mutation,
  show its category, affected rule and proposed changes. Deletion identifies
  the rule; sorting shows the order change.
- Create defaults to enabled and appends to the category's current list.
  Subsequent ordering is explicit. Supported disabled rules remain editable.
- Submit each rule change independently. Do not maintain drafts or combine
  several user edits into one publish workflow.
- Serialize Neo Box writes to the same Zone/category ruleset. Before sending,
  re-read the relevant live state and compare it with the state on which the
  User's operation was based. For ordering, compare the affected ordered list;
  for other writes include any surrounding state the chosen API would replace.
- A detected conflict sends no mutation and retains the User's input with a
  refresh/reconcile message. Do not silently rebase and submit the edit.
- This is optimistic conflict detection, not a cross-system atomicity
  guarantee. External edits between the final read and write remain possible
  unless Cloudflare provides an enforceable conditional-write mechanism.
- Route mutations through the existing operation wrapper: persist pending
  intent before sending; if logging fails, send nothing. Record succeeded,
  failed or unknown; surface a separate error if the final log update fails.
  A no-op sends no mutation and creates no operation.
- Log the Zone identity, category, affected rule, action, actor and appropriate
  before/requested/after state. Preserve enough ordering information to explain
  a move. Never log credentials or sensitive header values.
- Never automatically repeat an unconfirmed mutation. Preserve the existing
  client's policy for explicit rate-limit refusals; transport/ambiguous failures
  must not become automatic retries or fallback writes.
- If a necessary Ruleset does not yet exist, provide a supported initialization
  path for the first rule. Any upstream mutation needed for initialization
  follows the same log-before-send and uncertainty rules. If initialization has
  an uncertain result, stop and require a refresh before attempting rule creation.
- After confirmed success, refresh the affected category. A refresh failure
  does not turn a confirmed write into a failed write or offer an automatic retry.

### Errors and presentation

- Categories have independent loading, empty and error states. Only a
  confirmed absent ruleset/empty list is an empty state; access failure is not.
- Distinguish permission denial, rejected token, feature/plan restriction,
  quota exhaustion, invalid input, concurrency conflict and uncertain outcome
  when the response allows it; otherwise show the actual classified error.
- Follow existing authentication conventions: a rejected provider token must
  not return Unauthenticated and sign the User out. Ownership failures must
  not reveal another Account's Zone or rules.
- Do not infer write access from successful reads. Disable controls only for
  restrictions known reliably; otherwise permit submission and report refusal.
  Do not probe permissions with trial writes.
- Preserve entered values after validation, API and conflict errors. Do not
  treat error responses as an empty rule list.
- Integrate Zone Rules changes into the existing Operation log UI with clear
  category/action wording. An unknown result instructs the User to refresh
  and verify the live state before making a new decision.
- A successful save means Cloudflare accepted configuration. It does not
  certify that an actual request matches or reaches the intended origin.

## Testing Decisions

Use existing test seams. Extend the stateful fake Cloudflare API and test
observable behavior through the Account manager and Connect service. Follow
the existing Email Routing and Worker/Pages management tests rather than
introducing a second service abstraction solely for tests.

The fake should model the five categories, ordered rules, absent rulesets,
per-token read/write permissions, representative feature/quota refusals,
external state changes and ambiguous mutation outcomes. Assertions should
check requests, live fake state, RPC results and persisted operation outcomes,
not internal helper call sequences.

Frontend tests cover expression conversion/escaping, action validation,
read-only reasons, order restrictions, conflict/error messages and confirmation
wording through the existing Vitest setup.

Acceptance checks:

1. Each category can read existing rules and distinguish an empty category
   from permission/API failure; another failing category remains usable.
2. Each action in the scope table round-trips without losing its configuration.
3. Supported rules can be created, edited, enabled, disabled, deleted and
   reordered, including rules created outside Neo Box.
4. A first rule can be created when the corresponding ruleset is absent;
   initialization failures and uncertain outcomes do not trigger blind retries.
5. Common and advanced matching modes preserve equivalent behavior; advanced
   expressions that cannot be represented stay in advanced mode.
6. Matching all requests requires explicit selection. New rules default to
   enabled but can be submitted disabled.
7. Unsupported writable configuration is fully visible and protected by the
   server against all disallowed operations, including indirect movement.
8. Untouched rules and unfamiliar configuration remain intact after every
   allowed mutation; unsafe replacement is refused.
9. External edits and stale ordering are detected before writes; User input
   survives the conflict. Concurrent writes inside Neo Box are serialized.
10. Logging failure prevents all upstream mutation; confirmed successes and
    failures are logged correctly; an unconfirmed mutation is unknown and
    is not resent. Outcome-log failure is reported separately.
11. No-op changes neither write nor log; sensitive header values and provider
    credentials never appear in operation payloads or errors.
12. Another User's Connection and a Zone outside the Account are inaccessible.
    Missing permissions and rejected provider tokens do not sign the User out.
13. Feature restrictions, quotas and invalid expressions preserve input and
    produce useful errors without changing unrelated categories.
14. All write actions require a reviewable confirmation. Confirmed success
    followed by failed refresh does not invite resubmission.
15. Saving origin overrides never mutates DNS records. Saving rules never
    initiates traffic probes against the User's domains.
16. The existing Operation log shows rule category, action, target and outcome.

Run relevant backend tests and frontend checks. Repository-backed tests need
the repository's PostgreSQL test DSN; skipped tests must be reported as skipped.
Regenerate protobuf outputs after contract changes and run the required
generation/lint/build checks for the files changed during implementation.

## Out of Scope

- Bulk Redirects, Managed Transforms and legacy Page Rules.
- Cache, Configuration, WAF or other rule families outside the five categories.
- Cross-Zone copying, bulk operations or a cross-category execution-order editor.
- Local drafts, batch publishing, saved desired state and one-click rollback.
- A universal visual expression builder or arbitrary writable JSON editor.
- Editing, toggling, deleting or moving unsupported rules.
- Request simulation, rule-hit statistics, traffic tracing and automatic
  end-to-end verification of redirect/rewrite/origin behavior.
- Automatic DNS changes, token permission changes and plan upgrades.
- Any claim of atomic coordination with edits made outside Neo Box.

## Further Notes

- This specification records the design interview's approved v1 scope.
  Publishing it as an Issue does not start implementation.
- During implementation, verify current Cloudflare API details against
  official documentation; do not hard-code plan entitlements from memory.
  A plan restriction is a capability/error state, not a reason to silently
  narrow the agreed feature set.
- Update the domain glossary, architecture guidance and any necessary ADRs
  with the implementation. Use "HTTP rule" or the specific product/category
  name to distinguish this feature from Email Routing's existing "Routing rule".

