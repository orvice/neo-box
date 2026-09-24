# ADR 0001: NocoDB snapshots as verbatim gzip JSON built from the REST API

Status: accepted (2026-09-25)

## Context

Open-source NocoDB has no Base backup/snapshot feature. We want periodic,
browsable backups of Bases on self-hosted instances, with restore as a later
step.

What the OSS REST API offers (checked against `swagger-v2.json` /
`swagger-v3.json` and the OSS controllers on `develop`):

- v2 `GET /api/v2/meta/bases/` lists Bases. The v3 base list is
  workspace-scoped, and OSS has no workspaces.
- v3 meta: `GET /api/v3/meta/bases/{baseId}`, `.../tables`,
  `.../tables/{tableId}` (full field definitions incl. options).
- v3 data: `GET /api/v3/data/{baseId}/{tableId}/records?page=&pageSize=`
  (`{records:[{id, fields}], next}`), and
  `GET /api/v3/data/{baseId}/{tableId}/links/{fieldId}/{recordId}`.
- OSS has no v3 views controller; v2 export is per-view CSV only.
- Auth is the `xc-token` header. NocoDB Cloud limits to 5 req/s and asks for
  a 30 s pause after 429.

## Decision

- A Snapshot is one gzip JSON document (format `neobox.nocodb.snapshot`,
  version 1; layout documented in `internal/snapshot/format.go`). NocoDB
  payloads (base meta, table schema, records) are stored **verbatim** so a
  future restore sees exactly what the API returned.
- Records come from paged v3 record lists. Links are captured separately:
  for each Links / LinkToAnotherRecord field, every record whose value shows
  at least one link (a count > 0 or a non-empty object/array) gets a
  `/links/` lookup, stored as `{field_id, record_id, linked_ids}`. Both sides
  of a relation are captured; restore can dedupe.
- Attachments are kept as the metadata/URLs NocoDB returns; file bytes are
  not downloaded.
- Content goes to S3 (butterfly `store.s3`), with a local-directory fallback
  for development. Metadata lives in MongoDB.
- Calls are rate limited per client (default 5 req/s, configurable) and
  retried on 429/5xx, honoring `Retry-After`, else 30 s for 429.
- Snapshots run in an in-process worker queue; schedules are in-process cron.
  One Snapshot per Base at a time. Unfinished Snapshots are marked failed at
  startup. This assumes a single neo-box process.

## Consequences

- Link capture costs one request per linked record per link field; large,
  heavily linked Bases take a while at 5 req/s. Self-hosted users can raise
  `nocodb.requests_per_second`.
- Browsing a Snapshot re-reads the document from storage (the last few tables
  are cached in memory).
- Running multiple neo-box replicas would double-fire schedules; that needs a
  lease before scaling out.
- Views, filters, sorts, webhooks, and attachment bytes are out of scope for
  format version 1.
