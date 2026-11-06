# ADR 0013: Cloudflare Zone Rules use live phase rulesets

Status: accepted (2026-11-06)

## Context

Cloudflare exposes redirects, transforms and origin overrides as ordered Zone
Rulesets. They are independent phases, have different permissions and action
schemas, and can contain configuration Neo Box does not understand. A generic
local desired-state model would either lose that configuration or imply that
Neo Box owns rules created in Cloudflare.

## Decision

Neo Box manages five Cloudflare product phases independently:

- `http_request_redirect` (Single Redirects)
- `http_request_transform` (URL Rewrite Rules)
- `http_request_late_transform` (Request Header Transform Rules)
- `http_response_headers_transform` (Response Header Transform Rules)
- `http_request_origin` (Origin Rules)

The Cloudflare client represents action parameters as raw JSON and retains
unknown rule fields. Before each mutation the Account manager reads the live
ruleset, checks the user's snapshot and ownership constraints, and sends one
operation through the existing resource operation wrapper. A missing ruleset
uses the same logged initialization path as an ordinary write. Unknown or
unsupported writable configuration is shown read-only and cannot be moved
indirectly. Cloudflare remains the source of truth; Neo Box stores operation
history only.

The API contract uses Cloudflare's product names and typed category/action
messages. The frontend exposes independent loading and error states for each
category. A confirmed save means Cloudflare accepted the configuration and
never claims that traffic was tested.

## Consequences

Rulesets preserve unfamiliar fields across safe read-modify-write operations,
but a future Cloudflare schema change may still make a rule read-only until
support is added. Conflict checks prevent stale Neo Box edits from being sent,
while external changes between the final read and Cloudflare's write remain a
cross-system race and are reported according to the existing uncertain-write
policy.
