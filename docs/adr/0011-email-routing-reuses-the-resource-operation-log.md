# ADR 0011: Email Routing reuses the resource operation log and never disables

Status: accepted (2026-10-16); API behavior checked against Cloudflare's Email Routing API documentation on 2026-10-16

## Decisions

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
