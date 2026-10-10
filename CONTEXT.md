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
  (today: NocoDB, Wasabi and Cloudflare). Each Provider decides what it
  shows and does for a Connection; Neo Box has no provider-independent model
  of resources. Not to be confused with the OAuth sign-in providers above.
- **Connection** — one account at a Provider, owned by one User: a name,
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
  records, every view (with its column settings, filters and sorts), and
  the attachment files the records hold (unless the Base's Backup Policy
  leaves them out). Content is a gzip JSON document in blob storage;
  metadata (status, counts, size) is in PostgreSQL. Webhooks are not
  captured yet.
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
  files, and views with their column settings, filters and sorts. It does
  not bring back original record IDs, created/modified time and by,
  AutoNumber values, User values for people who aren't members of the new
  Base, or views' share links, row colouring and form images. A Restore that fails partway leaves its partial Base in NocoDB
  and records its ID; trying again builds another new Base.
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
- **Visible resources** — the Zones, Workers and Pages projects the token can
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
