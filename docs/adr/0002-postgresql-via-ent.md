# ADR 0002: PostgreSQL via ent instead of MongoDB

Status: accepted (2026-09-25). Supersedes the "Metadata lives in MongoDB"
point of ADR 0001; the rest of 0001 stands.

## Context

Neo Box stored users, sessions, OAuth state, NocoDB Connections, Backup
Policies, and Snapshot metadata in MongoDB. The data is relational and small:
everything is owned by a User, and the queries are equality filters plus a
sort. We want PostgreSQL as the one database to operate.

No MongoDB data needed carrying over when we switched.

## Decision

- The connection comes from butterfly `store.db.<key>` (driver `postgres`,
  pgx under `database/sql`). `db_store` names the key, the same way
  `storage.s3_store` names a `store.s3` client. Bootstrap refuses a
  connection whose driver is not pgx.
- Data access goes through ent. Schemas live in `internal/ent/schema/`, and
  the generated client is committed (`make ent`). Repository interfaces in
  `internal/repo/*` stay as they were; the implementations moved from
  `mongo/` to `postgres/` subpackages and do not expose ent types.
- The schema is migrated on startup with ent's `Schema.Create`, which never
  drops tables, columns, or indexes. There are no versioned migration files.
- MongoDB features got these replacements:
  - TTL indexes (sessions, OAuth state): queries already filter on
    `expires_at`; a goroutine deletes expired rows every hour.
  - Sparse unique `(provider, external_id)`: a partial unique index
    `WHERE external_id <> ''`.
  - Embedded Snapshot table list: a JSONB column.
  - A missing optional time (`omitempty`): a NULL column, which reads back
    as the zero `time.Time`.
- Tables have no foreign keys. The services already delete children before
  parents, which matches what the old store did.

## Consequences

- The database user needs DDL rights on every start. If a schema change needs
  to drop or rewrite data, `Schema.Create` cannot do it. That is when to
  switch to versioned migrations (ent + Atlas).
- Repository tests run against a real PostgreSQL. Each test gets its own
  schema, and the tests are skipped unless `NEOBOX_TEST_POSTGRES_DSN` is set.
  CI provides a postgres service.
- butterfly puts `store.db` credentials into the connection URL without
  escaping them, so passwords must avoid URL-reserved characters until
  butterfly fixes that.
