# ADR 0005: Snapshot format v2: attachment files, views and webhooks

Status: accepted (2026-10-07)

## Context

Format version 1 (ADR 0001) leaves out attachment files, views and
webhooks, so a Restore (ADR 0004) can't bring them back. Attachment files
matter most: a v1 snapshot keeps only their metadata, and once the files
change or their signed links expire, the snapshot has nothing to restore
them from.

NocoDB's paid Base Snapshots (self-hosted Business and up) don't restore
permissions or share settings either. Their restore also creates a new
Base.

Checked live on a local `nocodb/nocodb` 2026.09.1 (OSS, no licence):

**Attachments**
- A record's Attachment value lists `path`, `title`, `mimetype`, `size`
  and `signedPath` when NocoDB stores files itself. With S3 storage it
  lists `url` and `signedUrl`. A file added by URL has only `url`.
- `GET {instance}/{signedPath}` returns the bytes.
- Each upload gets a new random file name, and a stored file never changes.
  So `path` (or `url`) plus `size` identifies the content.
- **Writing attachments**
  - A v3 record write rejects a new attachment without a `url`: "New
    attachment must include a url". NocoDB then fetches that URL itself,
    which its SSRF guard blocks for private addresses (ADR 0004).
  - v3 `POST .../records/{id}/fields/{fieldId}/upload` takes one base64
    file per request, appends it, and renames its title to the stored
    file name.
  - What works: `POST /api/v2/storage/upload` takes multipart `files`,
    several per request. It returns one attachment object per file and
    keeps the original title, quotes included. `PATCH
    /api/v2/tables/{tableId}/records` with `[{"<ID field title>": id,
    "<field>": [...]}]` then sets those objects on the record.

**Views**
- v3 views are licence-gated (`ERR_LICENSE_REQUIRED`,
  `feature_api_view_v3`).
- v2 meta views work for reading and creating views, their columns,
  filters (including nested groups) and sorts.
- v3 filters and sorts are in OSS, but drop v2 fields such as
  `comparison_sub_op`.

**Webhooks**
- v2 `/api/v2/meta/tables/{tableId}/hooks` and `/api/v2/meta/hooks/{id}/filters`
  list and create hooks and their conditions.
- A hook can be created with `"active": false`.
- A hook `PATCH` needs the full body; a partial one fails with "hook
  version is deprecated".

## Decision

**Format version 2.** Readers and Restore accept versions 1 and 2. Records
stay verbatim. The header gains `"attachments": true` when attachment
files were captured. Each table gains a list per kind of addition:
- `files` (#32): for a table with Attachment fields, every distinct file
  its values reference, as `{source, size, sha256}`, or `{source, size,
  error}` when the file couldn't be read. `source` is the attachment's
  `path`, else its `url`.
- `views` (#33): every view, with its columns, filters and sorts, read
  through v2.
- `hooks` (#34): every webhook, with its filters.

A missing list means "not captured" and an empty one means "none".

**Attachment files are stored once per user, by content.** The bytes go
to the blob store under `nocodb/{user}/files/{sha256}`, outside the
snapshot document. PostgreSQL keeps three tables:
- `nocodb_files`: which files the user has stored.
- `nocodb_snapshot_files`: which snapshots use each file.
- `nocodb_file_sources`: a hint per connection, mapping `(source, size)`
  to the `sha256` stored last time. A later snapshot of an unchanged file
  then records a reference without downloading it. The hint is ignored
  when its file is gone.

**Capture**
- A snapshot downloads from `signedUrl`, `signedPath`, `url` and `path`, in
  that order. The API token and the connection's rate limit apply only to
  the instance itself, never to another host such as S3.
- A file that no reference can serve is recorded with its error. The
  snapshot still succeeds, counts the file as missing, and a restore warns
  about it.

**Collecting unused files**
- Deleting a snapshot deletes its references in the same transaction. Then
  every file of the user that no snapshot uses is deleted. This covers
  manual deletes, retention, and connection cleanup.
- A failed snapshot drops its references the same way.
- Registering a file holds an in-process `RWMutex` shared, and collection
  holds it exclusively. So a file can't be collected between a running
  snapshot finding it stored and recording that it uses it. Like the
  scheduler, this assumes one neo-box process (ADR 0001).

**Backup Policies** gain `include_attachments`, on by default. A manual
snapshot follows its Base's policy, and a Base without a policy includes
attachments. Off gives a v2 document without `files` (`attachments`
false), which restores like v1.

**Restore of attachments** comes after display fields. Records are
inserted without attachments. Then, for each restored record:
- Each Attachment field's files are uploaded through v2 storage, one
  request per cell, keeping title and mimetype.
- The objects are set with a v2 record update, 10 records per request,
  keyed by the new table's ID field title.
- A batch rejected with 400 is retried one record at a time.
- An upload refused with 400, 413 or 422 is a warning and the restore goes
  on.
- A file never captured or missing from storage is an
  `attachments_skipped` warning.

**Views and webhooks** (#33, #34)
- Views are restored after attachments, through v2:
  - The new table's default view takes the default view's settings.
  - Other views are created by type, with field IDs remapped and kanban
    stacks matched by choice title.
  - Share settings (public link, password), ownership and form images are
    not restored.
- Webhooks are restored last and always turned **off**, so the restore's
  own writes can't fire them and the user decides when to turn them on.
  Their headers can hold secrets, which are captured as they are, like
  record data.

### Views, as built (#33)

More checks on 2026.09.1 while building the views restore:
- **Default view.** `is_default` comes back null, even for the default
  view. The view NocoDB creates with a table is recognised as the earliest
  created grid, falling back to the lowest order. Its settings go onto the
  new table's own default view, which is renamed if needed. Views are put
  back in their snapshot order with `PATCH /api/v2/meta/views/{id}`
  (`order`), along with `lock_type`, `description`,
  `show_system_fields` and `meta`. NocoDB rebuilds `groupingFieldColumn`
  itself, so it is not copied from `meta`.
- **Hidden system columns.** Column lists include NocoDB's hidden system
  columns (created/updated time, …), which the v3 schema doesn't list.
  Their settings are left alone, and so are the settings of fields the
  restore skipped. A sort or filter on a skipped field is dropped with a
  warning, because the view would otherwise show other records.
- **Type-specific settings.**
  - A grid's `row_height`.
  - A form's heading, messages, redirect and flags.
  - Gallery, kanban and calendar covers.
  - Kanban stacks: the meta is keyed by the grouping field's ID and each
    stack by its choice ID, so both are rewritten, with stacks matched by
    choice title.
  - These are set with `PATCH /api/v2/meta/{grids|forms|galleries|kanbans|calendars|maps}/{id}`.
  - A kanban needs `fk_grp_col_id`, a calendar `calendar_range` and a map
    `fk_geo_data_col_id` when created. Without that field the view is
    skipped with a warning; maps usually are, since GeoData fields aren't
    restored.
- **Column settings.**
  - `show` and `order` go through `PATCH /api/v2/meta/views/{id}/columns/{col}`.
  - Grid width, grouping and aggregation go through
    `PATCH /api/v2/meta/grid-columns/{col}`.
  - Form label, help, description and required go through
    `PATCH /api/v2/meta/form-columns/{col}`.
  - Only values that differ from the new view's defaults are sent.
  - Calendar column styling (bold, italic, underline) is not restored.
- **Filters** keep `comparison_sub_op` (e.g. `isWithin` /
  `pastNumberOfDays`). Groups are created before their members, and
  `fk_parent_id` and every `fk_*_col_id` are remapped.
- **Warnings.** View warnings (`view_skipped`, `view_setting_skipped`)
  carry the view's title, and each different loss is reported separately.

### Webhooks, as built (#34)

The 2026.09.1 hook controllers, service, model and request schema confirm:
- v2 hook and hook-filter lists use the same `{list: [...]}` envelope as
  views. Nested filter groups use the shared filter-children endpoint.
- Only `version: "v3"` is accepted for creation. The captured version is
  kept; older hooks rejected by the target become `hook_skipped` warnings,
  not silently upgraded hooks.
- `operation` is an array for v3 hooks. `notification` is returned as a
  stored JSON string and is decoded to an object for creation, retaining
  headers, auth and payload values. `trigger_field` is a boolean flag;
  `trigger_fields` lists field IDs and is remapped to the new fields.
- Hooks are created last with `active: false`, then their filters, groups
  before members. A missing trigger field skips the hook rather than
  broadening its trigger. A missing or rejected filter is dropped with a
  warning; the hook stays off even with incomplete conditions.
- `hooks_disabled` counts created hooks per table and tells the user to
  turn them on in NocoDB. Each non-URL hook has a `hook_needs_setup` warning
  about its target integration. Each rejected hook or condition has a
  `hook_skipped` warning naming the hook.
- `hook_count` counts captured or successfully created hooks per table and
  per snapshot/restore. v1 and older v2 documents without hooks still
  restore without webhook requests or warnings. Captured empty hooks are
  written as `[]`, distinct from an absent list.

The live round-trip fixture now has a URL hook with a condition group,
trigger fields and an Authorization header. It checks retained settings,
headers and remapped conditions, and that the restored hook is inactive.

## Consequences

- A snapshot's `size_bytes` covers only its document. Its files are
  counted separately (`file_count`, `file_bytes`, `files_missing`). Several
  snapshots of an unchanged Base cost one copy of its files.
- The first snapshot with attachments downloads every file once, at the
  connection's rate for files NocoDB serves itself. Later snapshots
  download only new files.
- A restore holds one cell's files in memory while uploading them.
- Restored attachments get new storage paths. Titles, types, sizes and
  bytes are kept.
- `TestLiveRestoreRoundTrip` uploads two files into the fixture and
  compares each attachment's title, type, size and content hash after the
  round trip. S3-backed NocoDB storage (`url`/`signedUrl`) has not been
  exercised live.
- The fixture also gives a table one view of each type v2 creates, except
  maps. The round trip compares each view's settings, visible columns in
  order with their settings, sorts, the nested filter tree and kanban
  stacks.
- A restore costs a few requests per view plus one per changed column,
  sort and filter.
