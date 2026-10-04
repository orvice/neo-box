# ADR 0004: Restore a Snapshot into a new Base through the OSS REST API

Status: accepted (2026-10-03)

## Context

Snapshots (ADR 0001) were taken with restore in mind. A restore must work
against open-source NocoDB using only a connection's API token. NocoDB's
paid Base Snapshots restore into a new Base and never touch the original,
and we follow that.

What the OSS API offers was checked against the controllers on `develop`
and then exercised on a local `nocodb/nocodb` 2026.09.1:

- `POST /api/v2/meta/bases` creates an empty Base (no tables). v3 base
  routes are workspace-scoped.
- v3 meta creates tables (`POST .../tables` with `fields`), adds fields
  (`POST .../tables/{id}/fields`), renames fields
  (`PATCH .../fields/{id}`), and sets a table's `display_field_id`
  (`PATCH .../tables/{id}`).
- v3 data inserts records (`POST .../records`, at most 10 per request by
  default, 422 above that) and adds links
  (`POST .../links/{field}/{record}`). Record IDs are always assigned by
  NocoDB, and a whole batch fails (400/422) if one value is invalid.
- Creating a relation always creates its inverse field in the related
  table, with a generated title. Self-links get no inverse. `hm`/`bt` come
  back as `om`/`mo`.
- A v3 field definition read back from GET can be posted again once `id`,
  `table_id`, `system` and select-choice IDs are dropped. Re-posting a
  choice ID violates a unique constraint. Lookup, rollup, barcode and QR
  options name fields by ID; formulas and URL buttons name them by title.
- Field options don't name a relation's inverse field.
- Created/modified time and by, AutoNumber, and computed fields reject
  writes. User values must be `{email}` of a member of the Base.

## Decision

- A restore always creates a new Base on one of the user's NocoDB
  connections. `snapshot.Restore` reads the snapshot document once per
  pass and works in this order:
  1. Create the Base. The caller is told its ID at once.
  2. Create each table with its plain and system-valued fields. If NocoDB
     rejects the table, create it bare and add the fields one by one.
  3. Create each relation from one side, find the inverse NocoDB added,
     and rename it to the snapshot's title. Snapshots hold both sides; the
     inverse is the link back with the mirrored relation type. When two
     tables share several such relations, the candidate whose captured
     links mirror the field's links wins, and only when the links can't
     tell them apart is the first taken (with a warning).
  4. Insert records in snapshot order, 10 per request, mapping old record
     IDs to new ones. A rejected batch is retried one record at a time.
  5. Add links from the side each relation was created from.
  6. Create lookups, rollups, formulas, buttons, barcodes and QR codes in
     passes until no more can be created. Field IDs in their options are
     replaced with the new ones.
  7. Set each table's display field.
- Computed fields come after the data. They need nothing written, and
  NocoDB 2026.09.1 fails many-to-many link writes with a 500 on a table
  that already has a lookup through another relation. The same links
  succeed before the lookup exists.
- Anything that can't be restored becomes a grouped warning (code,
  table, field, count, message) rather than a failure: unsupported field
  types, buttons that run webhooks or scripts, rejected records, links to
  them, attachments, and users who aren't members of the new Base. Other
  errors (auth, 5xx, transport) abort the restore.
- A write (POST/PATCH/DELETE) is retried only on 429. After a 5xx or a
  transport error it may already have been applied, and repeating an
  insert would duplicate records. GET keeps retrying on 5xx as before.
- A restore that fails partway keeps the partial Base and reports its ID,
  so the user can inspect or delete it. A retry builds another new Base.

## Consequences

A restored Base matches its snapshot's tables, field titles and types,
record values, links, and computed fields, except:

- Record IDs, created/modified time and by, and AutoNumber values are new.
- Relations are stored the way v3 creates them: junction-based `om`/`mo`
  for has-many.
- Attachments are not restored, because snapshots hold only their
  metadata. Signed URLs expire, and NocoDB's SSRF guard blocks private
  addresses, so URLs can't be handed to the target to fetch.
- User values survive only for members of the new Base. A new Base's only
  member is the token's owner.
- Views, filters, sorts, webhooks and table icons are not in format
  version 1, so they are not restored.

A restore costs roughly one request per 10 records plus one per linked
record, at the connection's rate limit.

`TestLiveRestoreRoundTrip` builds a fixture Base, snapshots it, restores
it, snapshots the result and compares the two. It needs
`NEOBOX_TEST_NOCODB_URL`, `NEOBOX_TEST_NOCODB_TOKEN` and
`NEOBOX_TEST_NOCODB_EMAIL`.
