# Cloudflare Email Routing v1

Status: design approved 2026-10-16; implemented per docs/superpowers/plans/2026-10-16-cloudflare-email-routing.md.

## Goal

Manage Cloudflare Email Routing from a Zone's page: enable it, manage
forwarding rules and the catch-all, and manage the Account's destination
addresses. Every change is logged before it is sent, like DNS and
Worker / Pages changes.

## Scope

In:

- Read a Zone's Email Routing status, its rules and its catch-all.
- Enable Email Routing for a Zone (Cloudflare writes the MX / SPF records).
- Create, update (including enable / disable), delete forwarding rules.
- Set the catch-all (forward to a verified address, drop, or off).
- List, create and delete the Account's destination addresses.

Out (first version):

- **Disabling** Email Routing. It deletes the MX records and stops inbound
  mail; there is no RPC for it.
- Rules whose action is "send to Worker", or that a Worker's wrangler
  configuration owns (`source` other than `api`). They are shown read-only and
  cannot be edited or created here.
- Sending mail, email logs, subaddressing settings.

## Decisions

- **Operation log: reuse `cloudflare_resource_operations`.** A new
  `ResourceKind` `email_routing` (`Resource` = Zone name, `Target` = rule
  ID, `catch_all` or the address). `kind` is a plain string column, so
  there is no ent change. All writes go through `operate`, with
  read-modify-write under `lockResource("email/<acc>/<zone>")` for Zone-level
  changes and `lockResource("email/<acc>/addresses")` for destination
  addresses; no-op changes
  return `noChange()` unlogged.
- **Destination addresses are Account-level**, rules are Zone-level. Both
  appear in the Zone page's Email routing tab; the address list is labelled
  as shared by the Account's Zones.
- Destination-address operations are logged with Resource "destination
  addresses"; the tab's log lists all `email_routing` operations of the
  connection.
- **`Verify` is unchanged.** Email Routing permissions are optional; a
  missing one surfaces in the tab only.
- Errors follow the Cloudflare conventions: missing permission is
  PermissionDenied, a rejected token FailedPrecondition (never
  Unauthenticated), an unconfirmed write Unknown.

## Backend

### `internal/cloudflare/email.go`

Client methods (reads retry 429/5xx/network errors; writes retry only 429,
otherwise `UncertainError`, never resent):

- `GetEmailRouting(zone)` — status (enabled, synced, name).
- `EnableEmailRouting(zone)` — `POST /zones/{id}/email/routing/dns`.
- `EmailRoutingDNS(zone)` — the DNS records Email Routing needs (for the
  enable confirmation).
- `ListEmailRules`, `CreateEmailRule`, `UpdateEmailRule`,
  `DeleteEmailRule`, `GetEmailCatchAll`, `UpdateEmailCatchAll`.
- `ListEmailAddresses`, `CreateEmailAddress`, `DeleteEmailAddress`
  (account-level).

Input validation here: the matcher must be a valid email address in the
Zone's domain; the action is `forward` (one or more addresses) or `drop`.
Unknown fields of rules the client reads are kept when it writes them back.
The exact endpoint paths and permission names are confirmed against
Cloudflare's API docs when the plan is written.

### `internal/cloudflareacct/email.go`

- Every Zone-level call first checks the Zone belongs to the Account
  (`a.Zone`), as the other Zone calls do.
- `EnableEmailRouting`: no-op when already enabled.
- Creating or updating a forward action requires every target address to be
  verified; otherwise `FailedPrecondition` with the offending address.
- Deleting a destination address that a rule or the catch-all of the **Zone
  named in the request** (`zone_id`) still forwards to is refused with
  `FailedPrecondition` naming the rule. Rules of other Zones are not checked
  (one rules read per Zone is too slow); Cloudflare's own answer applies.
- Worker-action and wrangler-owned rules are returned flagged read-only;
  update and delete of them is refused here, not sent.
- Errors classified by `a.classify` with the permission name
  ("Email Routing Rules Write", "Email Routing Addresses Write", DNS Write
  for enabling).

## Proto (`cloudflare.proto`)

One request and response per RPC (buf STANDARD).

- Read: `GetCloudflareEmailRouting` (status, rules, catch-all in one
  response), `ListCloudflareEmailAddresses`.
- Write: `EnableCloudflareEmailRouting`, `CreateCloudflareEmailRule`,
  `UpdateCloudflareEmailRule`, `DeleteCloudflareEmailRule`,
  `SetCloudflareEmailCatchAll`, `CreateCloudflareEmailAddress` (Cloudflare
  mails the verification), `DeleteCloudflareEmailAddress` (requires
  `zone_id` to check that Zone's rules and catch-all).
- Writes return `CloudflareChangeResult`.
- `ListCloudflareResourceOperations` accepts the new kind.
- Enums follow the no-acronym-run naming rule.

Run `make buf` and commit the generated output.

## Frontend

- `front/src/features/cloudflare/email-routing-tab.tsx`, added to
  `zone-page.tsx` beside DNS records and Operation log.
  - Status bar; when not enabled, an "Enable" button opening
    `ConfirmChangeDialog` that lists the MX / SPF records Cloudflare will
    add.
  - Rules table: enable / disable, edit, delete; Worker-action rules and
    rules owned by a wrangler configuration (`source` other than `api`) are
    read-only.
  - Catch-all card.
  - Destination addresses: pending / verified, add (notes that a
    verification mail was sent), delete.
  - Missing permission shown as a notice in the tab, not a page error.
- `email.ts` + `email.test.ts` (Vitest): address and rule input checks,
  status and log wording.
- Hooks in `front/src/api/` for the new RPCs.
- The Operation log tab shows the new kind.

## Testing

- `cftest`: stateful fake Email Routing API (status, rules, catch-all,
  addresses with pending / verified, per-token permissions).
- Client and `cloudflareacct` tests: unverified target refused, enable is a
  no-op when enabled, address in use refused, Worker-action and
  wrangler-source rules not editable,
  permission denied, uncertain write logged as unknown, Zone of another
  Account not found, secret-free log.
- Vitest for `email.ts`.

## Docs

- `CONTEXT.md`: Email Routing, Destination address, Routing rule,
  Catch-all.
- ADR `0011`: reuse of the resource operation log; no disable in v1.
- `AGENTS.md` / `CLAUDE.md`: architecture entries for `email.go` and the
  new RPCs.
