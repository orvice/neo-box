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
  (today: NocoDB). Each Provider decides what it shows and does for a
  Connection; Neo Box has no provider-independent model of resources. Not
  to be confused with the OAuth sign-in providers above.
- **Connection** — one account at a Provider, owned by one User: a name,
  the Provider's settings, and its credentials. Credentials are encrypted
  with `crypto.encryption_key` and never returned by the API. A Connection
  has a health status (ok / error, with a message), set whenever its
  credentials are checked and by the Provider's background work. Deleting a
  Connection deletes everything the Provider stored for it.

### NocoDB

- **NocoDB Connection** — a Connection whose Provider is NocoDB: one
  self-hosted NocoDB instance (base URL) plus an API token. A Snapshot run
  rejected with 401/403 marks it error; a successful run marks it ok.
- **Base** — NocoDB's top-level container of tables (a "project"; IDs start
  with `p`). Neo Box does not store Bases; it lists them live from NocoDB.
- **Snapshot** — a point-in-time, read-only capture of one Base: the base
  meta, every table's schema (fields), every record, and every link between
  records. Content is a gzip JSON document in blob storage; metadata
  (status, counts, size) is in PostgreSQL. Attachments are captured as their
  metadata/URLs only, not file bytes. Views are not captured (OSS NocoDB has
  no v3 views API).
- **Snapshot trigger** — `manual` (user clicked "Snapshot now") or
  `scheduled` (fired by a Backup Policy).
- **Backup Policy** — per (Connection, Base): enabled flag, cron expression,
  and retention N. After a scheduled Snapshot succeeds, scheduled Snapshots
  beyond the newest N are deleted. Manual Snapshots are never pruned.

A Base has at most one Snapshot pending or running at a time.

## Planned (not yet modeled)

- **Restore** — rebuild a Snapshot into a new Base (never overwrite).
- More **Providers** beyond NocoDB (next: Wasabi, #5).
