# Neo Box — Domain Context

Neo Box is a personal system for managing and viewing resources held in
third-party services (cloud providers, SaaS accounts, subscriptions, …) from
one place.

## Terms

- **User** — a person who signs in to Neo Box. Role `admin` can manage other
  users; role `user` manages only their own data. Users sign in with a
  password or an OAuth provider (GitHub, Google).
- **Session** — a bearer token issued at sign-in. Only its sha256 is stored;
  it expires after `auth.session_ttl`.
- **Provider** — a kind of third-party service Neo Box integrates with
  (today: NocoDB, Wasabi, Cloudflare and Google Workspace). Each Provider decides what it
  shows and does for a Connection; Neo Box has no provider-independent model
  of resources. Not to be confused with the OAuth sign-in providers above.
- **Connection** — access to a Provider, owned by one User: a name,
  the Provider's settings, and its credentials. Credentials are encrypted
  with `crypto.encryption_key` and never returned by the API. A Connection
  has a health status (ok / error, with a message), set whenever its
  credentials are checked and by the Provider's background work. Deleting a
  Connection deletes everything the Provider stored for it.

### NocoDB

- **NocoDB Connection** — a Connection whose Provider is NocoDB: one
  self-hosted NocoDB instance (base URL) plus an API token, and the request
  rate the instance tolerates (default 5/s, NocoDB Cloud's limit). A Snapshot run
  rejected with 401/403 marks it error; a successful run marks it ok.
- **Base** — NocoDB's top-level container of tables (a "project"; IDs start
  with `p`). Neo Box does not store Bases; it lists them live from NocoDB.
- **Snapshot** — a point-in-time, read-only capture of one Base: the base
  meta, every table's schema (fields), every record, every link between
  records, every view (with its column settings, filters and sorts), every
  webhook (with its conditions and notification settings), and the attachment
  files the records hold (unless the Base's Backup Policy leaves them out).
  Content is a gzip JSON document in blob storage; metadata (status, counts,
  size) is in PostgreSQL.
- **Attachment file** — the bytes behind an Attachment value. Stored once
  per User by content and shared by every Snapshot that holds it; deleted
  when no Snapshot holds it any more. A file NocoDB can't serve when the
  Snapshot is taken is recorded as missing rather than failing the
  Snapshot.
- **Snapshot trigger** — `manual` (user clicked "Snapshot now") or
  `scheduled` (fired by a Backup Policy).
- **Backup Policy** — per (Connection, Base): enabled flag, cron expression,
  retention N, and whether to include attachment files (on by default; also
  followed by manual Snapshots). After a scheduled Snapshot succeeds,
  scheduled Snapshots beyond the newest N are deleted. Manual Snapshots are
  never pruned.

- **Restore** — a succeeded Snapshot rebuilt into a **new** Base on one of
  the user's NocoDB Connections (by default the Snapshot's own). A Restore
  never changes an existing Base. It brings back tables, fields (links,
  lookups, rollups and formulas included), records, links, and attachments
  (re-uploaded, so with new storage paths) when the Snapshot holds their
  files, views with their column settings, filters and sorts, and webhooks
  with their conditions, always turned off. The user must turn webhooks on
  in NocoDB; non-URL notifications need the target instance's integration.
  It does not bring back original record IDs, created/modified time and by,
  AutoNumber values, User values for people who aren't members of the new
  Base, or views' share links, row colouring and form images. A Restore that
  fails partway leaves its partial Base in NocoDB and records its ID; trying
  again builds another new Base.
- **Restore warning** — one kind of loss in a Restore, grouped by table and
  field with a count, e.g. "Orders / Files: 12 attachments not restored".
  Warnings don't fail a Restore.

A Base has at most one Snapshot pending or running at a time. A Snapshot
can't be deleted while a Restore of it is pending or running.

### Wasabi

- **Wasabi Connection** — a Connection whose Provider is Wasabi: one
  standalone Wasabi account, read through the Stats API with a (preferably
  read-only sub-user) access key. A sync marks it ok or error.
- **Daily usage** — one UTC day of Stats API figures for the account or one
  bucket. Storage figures are a snapshot at midnight UTC; activity figures
  (API calls, egress) cover that day. Neo Box keeps every numeric field.
- **Active storage** — padded object bytes plus metadata: what Wasabi bills
  as stored data, with a 1 TB minimum.
- **Deleted storage** — deleted data still billed because it is younger than
  the minimum storage duration (typically 90 days).
- **Billing cycle** — Wasabi invoices every 30 days from the day the account
  became paid, not per calendar month. A connection may record one cycle's
  start day (its anchor).
- **Estimated cost** — Neo Box's estimate of a billing cycle's storage charge
  (or of the last 30 days without an anchor), from Daily usage and the
  connection's price. Never an invoice; egress is not priced.
- **Cost breakdown** — the Estimated cost's period split by what the charge
  pays for (active storage, deleted storage still billed, the 1 TB minimum)
  and by bucket. Wasabi doesn't say that bucket figures add up to the
  account's, so any difference is shown as unattributed.
- **Budget** — what a user wants to spend on a Wasabi Connection per billing
  cycle (or per 30 days without an anchor). Alerts fire when the Estimated
  cost is on track to go over it, and again when it does.
- **Trend forecast** — the Estimated cost with the days still to come
  following a line fitted to the last 30 days' charges (never below the 1 TB
  minimum), plus the next cycle on the same line.
- **Backfill** — the first sync of a Wasabi Connection, covering the last 12
  months.
- **Bucket settings** — a bucket's configuration as read through the S3 API
  after each sync: versioning, Object Lock, Wasabi compliance, lifecycle
  rules, policy and ACL, logging, replication, tags. Only the latest read is
  kept. A setting the key may not read is shown as such rather than as off.
  Risky settings are flagged (a public bucket, versioning without a rule that
  expires old versions) but raise no Alert. "Public" covers the policy and
  ACL; Wasabi's console Public Access Override can't be read.

### Cloudflare

- **Cloudflare Connection** — a Connection whose Provider is Cloudflare: one
  Cloudflare account (its Account ID) and an API token that reaches it. The
  token may hold only some permissions; whatever it lacks shows as missing
  where it is needed, never as an empty account. A token Cloudflare no longer
  accepts marks the Connection error.
- **Visible resources** — the Zones, Workers, Pages projects and Tunnels the token can
  see in the account: not necessarily all the account holds. They are read
  from Cloudflare when a page opens or is refreshed, never stored or synced.
- **Zone** — a domain on Cloudflare, the way into its DNS records. Only Zones
  of the Connection's account are reachable, even when the token sees more.
- **DNS record** — Neo Box adds, changes and deletes A, AAAA, CNAME, TXT,
  MX, NS, SRV and CAA records, and switches the proxy of records Cloudflare
  can proxy; other types are shown but not changed. A change keeps whatever
  the form does not show (comment, tags, settings). A change saved at
  Cloudflare may take a while to reach resolvers.
- **DNS access** — whether the token may change a Zone's records:
  read-only when Cloudflare says so or has refused a change for lack of
  permission, otherwise unknown, since Cloudflare does not tell a token's
  own permissions. Unknown is shown as unknown: changes are offered and
  Cloudflare refuses what the token may not do.
- **DNS operation** — one DNS change started from Neo Box (add, change,
  delete, proxy switch): who started it, when (UTC), the Zone and record, what
  was asked for, the record before and after, and its status. Recorded
  before the change is sent, so a change that can't be recorded isn't made.
  Its status is succeeded, failed, unknown (Cloudflare never answered; it is
  never sent again) or pending (sent, but its outcome could not be
  recorded). Only a succeeded operation has an "after". Changes made
  anywhere else are not DNS operations.
- **Address** — a way to reach a Worker (its workers.dev URL when enabled,
  custom domains, routes) or a Pages project (its pages.dev subdomain, custom
  domains with their status, production deployment URL), as Cloudflare
  reports it; never made up from a name. A route with wildcards is a
  pattern, and a custom domain that isn't active may not answer: neither is
  opened as a URL. Addresses whose source can't be read are reported as
  incomplete.
- **Tunnel** — a cloudflared Tunnel of the Connection's account, read live
  with its status (inactive, degraded, healthy, down) and connectors. A
  Tunnel is *managed remotely* when Cloudflare holds its ingress (created
  here or in the dashboard) and *local* when cloudflared reads a config
  file; only the first can have its hostnames changed here. Neo Box creates
  and deletes Tunnels, cleans up their stale connections, and shows the
  token that runs one without storing it. Deleting a Tunnel leaves DNS
  records that point at it.
- **Public hostname** — a route of a Tunnel's ingress: hostname, optional
  path (a regular expression), and the service requests go to. Routes sit
  before the catch-all, which Cloudflare requires last; a change replaces
  the whole config, so Neo Box reads it first, changes only the route, and
  keeps everything else (origin settings, other keys), one change at a
  time per Tunnel. Adding one can create the hostname's proxied CNAME to
  `<tunnel id>.cfargotunnel.com`; deleting one can delete that CNAME. A
  record that is anything else is never replaced or deleted, and a DNS
  problem never undoes the route: it is reported next to it. Route changes
  are not logged; the DNS records they create are DNS operations.
- **Worker** / **Pages project** — managed without touching code. A Worker's
  workers.dev switch, custom domains, routes, cron triggers and secrets can
  be changed, a version rolled back to (a new deployment of that version to
  all traffic) and the Worker deleted. A Pages project can be created
  (empty), its settings and variables changed, its deployments retried,
  rolled back or deleted, its domains added or removed, and the project
  deleted. Neo Box never uploads code and never changes a Git-connected
  project's production branch. A hostname or route that belongs to another
  Worker is never taken over. Secret values are write-only: never stored,
  logged or shown.
- **Resource operation** — one change to a Worker or Pages project made from
  Neo Box, logged like a DNS operation (pending before it is sent, then
  succeeded, failed or unknown; a change already in effect is not logged).
  It names the resource, action and target, with the state before and after
  and what was requested, never a secret value.

### Google Workspace

- **Workspace Connection** — one Service Account owned by a Neo Box User,
  reusable across several independent Workspace Tenants.
- **Workspace Tenant** — one independent Google Workspace organization,
  identified by its Customer ID, that authorizes a Workspace Connection.
  Multiple domains within the same organization belong to the same Tenant.
- **Delegated administrator** — the administrator in a Workspace Tenant
  whose authority the Service Account uses to manage that Tenant's users.
- **Workspace User** — a Google user account within one Workspace Tenant;
  distinct from a User who signs in to Neo Box.

- **Workspace alias** — an additional email address for a Workspace User.
  An alias generated by Google from a domain alias is managed by Google.
- **Workspace Group** — a tenant-owned group with an email address and members;
  members may be users or groups, including external addresses.
- **Group membership** — a direct relationship between a Workspace Group and
  a member, with the role Member, Manager or Owner.
- **Organization unit (OU)** — a node in a Workspace Tenant's organization
  hierarchy. Users belong to an OU and may inherit its Google policies.
- **Application grant** — a Workspace User's authorization for an application
  to access specified Google scopes. Revoking it removes existing grants,
  but does not permanently prohibit future authorization.

- **Workspace operation** — a change initiated from Neo Box, with its actor,
  tenant, requested change and confirmed or unconfirmed result. It does not
  include changes made directly in Google's admin console.
- **Google audit activity** — an event reported by Google for a Workspace
  Tenant, such as a login or an administrator action.
- **Security finding** — an account condition worth reviewing, such as an
  administrator without two-step verification or an inactive account. Missing
  account data is unknown, not evidence that a protection is disabled.

- **Gmail settings** — a Workspace User's own mailbox configuration (vacation
  responder, forwarding, delegates, filters, send-as), never messages. Read
  and changed by impersonating that user; see ADR 0008.
- **Access reason** — the text an administrator gives before Neo Box touches a
  Workspace User's Gmail settings or mail, stored with each operation it covers.
  It is accountability, not authorization.
- **Gmail mail** — a Workspace User's labels, messages and attachments, read
  live (never stored) by impersonating that user with `gmail.readonly`; see
  ADR 0010. Neo Box only reads: it does not change a message's read state or
  labels, and does not send. Opening a message does not mark it read.
- **Mail access** — one search, message open, label list or attachment download,
  each its own Workspace operation. It records the message ID, counts and
  query operator names, never subjects, bodies, addresses, file names or query
  values.

### Notifications

- **Notification channel** — one place a User's alerts are delivered; today a
  Telegram chat reached through the User's own bot. Its secret (the bot
  token) is sealed like a Connection's credentials and never returned. A
  channel can be turned off without deleting it.
- **Alert** — something background work wants a User to know: a failed
  scheduled Snapshot, a failed Restore, a Connection whose health turned to
  error, or a Wasabi Connection that is over or on track to go over its
  Budget, downloading more than it stores, or deleting a large share of its
  data in a day. Each Alert has a dedupe key chosen by whatever raised it, so a
  problem is reported once however often it is seen. Alerts go to every
  enabled channel of the User and are kept as a log with their delivery
  outcome.

## Planned (not yet modeled)

- More **Providers** beyond NocoDB, Wasabi and Cloudflare.
