# ADR 0012: Email logs are read live from the GraphQL Analytics API and are not stored

Status: accepted (2026-10-26); API behavior checked against Cloudflare's Email Service documentation ("Metrics and analytics", "Activity log") on 2026-10-26, not against a live account

## Decisions

Cloudflare's REST API has no endpoint that lists Email Routing mail. The
dashboard's activity log is the GraphQL Analytics API dataset
`emailRoutingAdaptive` (per-event rows; `emailRoutingAdaptiveGroups` holds the
aggregates), queried with `POST /client/v4/graphql` and a `zoneTag`. Neo Box
uses the per-event dataset for the new Email logs tab.

**Read live, never stored.** Cloudflare keeps the events 31 days and serves
them on demand, so, like Zones and DNS records, they are read when the tab
opens or is refreshed. Copying sender, recipient and subject of every
message into PostgreSQL would add personal data Neo Box has no other reason to
hold, plus a sync job and a retention policy, for a view that Cloudflare
already serves. The cost is that history older than 31 days is not available
anywhere; archiving would be a later, separate decision.

**Not logged as an operation.** Resource operations record changes before
they are sent (ADRs 0007 and 0011). Reading Email Routing logs changes
nothing, so it is not an operation. Gmail message bodies are logged on read
(ADR 0010) because the content is private and Neo Box impersonates the user;
here only message metadata Cloudflare already shows its dashboard is read,
with the token the user supplied, and no body is available.

**A separate permission, a separate failure.** The dataset needs the token's
Analytics Read permission (zone, or account-level Account Analytics), which no
other Neo Box feature uses. `Verify` is unchanged; a missing permission shows
only in the Email logs tab, as PermissionDenied naming "Analytics Read", like
the other optional modules. Because GraphQL reports a token without access as
HTTP 200 with an `authz` error, `readGraphQL` maps it to a 403 so the existing
`classify` (which re-verifies the token to tell a bad token from a missing
permission) applies unchanged.

**Newest N, filtered in the browser.** One query returns the newest events in
a range, at most 500. The page narrows them by status and text on the client:
the set of `status` values is Cloudflare's and not documented as a closed list,
so filtering on a value written into the query would make a wrong guess a
failed query. Paging past the limit is not offered (GraphQL has no offset);
when the limit is reached the response says so and a shorter range shows the
rest. The limit is part of the query text, validated as an integer, so no
GraphQL variable type has to be guessed.

The query is a POST but changes nothing, so the client retries it on 429, 5xx
and network errors like a read, and the fake does not count it as a write.
