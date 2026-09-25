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

### NocoDB

- **NocoDB Connection** — one self-hosted NocoDB instance (base URL) plus an
  API token, owned by one User. The token is encrypted with
  `crypto.encryption_key` and never returned by the API.
- **Base** — NocoDB's top-level container of tables (a "project"; IDs start
  with `p`). Neo Box does not store Bases; it lists them live from NocoDB.
- **Snapshot** — a point-in-time, read-only capture of one Base: the base
  meta, every table's schema (fields), every record, and every link between
  records. Content is a gzip JSON document in blob storage; metadata
  (status, counts, size) is in MongoDB. Attachments are captured as their
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
- Other third-party **Providers** beyond NocoDB.
