# ADR 0009: Drive governance reads from Reports first; Drive API scopes are a separate, logged step

Status: accepted for the Reports-based first version (2026-10-13); the Drive API steps remain proposed

Google Drive governance (who shares what outside the organization, how much
each user stores, which shared drives exist) can be reached two ways with a
Workspace Service Account, and their risk differs sharply.

**Admin Reports API** (`admin.reports.audit.readonly`, already used for the
Google audit tab, and `admin.reports.usage.readonly`). It reports Drive sharing
events (visibility changes, per-user access changes, ownership changes,
downloads, deletions, shared-drive membership and settings) and per-user
storage figures. The delegated administrator is the token subject, as with
every Directory feature. It returns titles and addresses but never file
content, and no scope in it can read or change a file.

**Drive API.** Listing shared drives (`drives.list` with
`useDomainAdminAccess`) accepts only `drive` or `drive.readonly`. Both are
restricted scopes that can read file content. Listing one user's files and
their permissions can use `drive.metadata.readonly` (to be re-verified against
the docs before it is built), which cannot read content, but it needs the
token's subject to be that user. Changing a permission needs `drive`, which can
change content.

## Decisions

**Reports first.** The first version covers Drive sharing activity and
per-user storage through the Reports API only. It adds one new scope,
`admin.reports.usage.readonly`, and reuses the audit scope. Neither can read
file content, so ADR 0008's impersonation rules do not apply to it.

**A content-capable scope is never added to an existing feature.** Each Drive
API capability is its own opt-in per tenant, requested only for its own
requests, like the optional directory scopes. Using one does not change the
tenant's base health when it fails.

**Drive API access follows ADR 0008.** Any request made with a Drive API scope
requires an access reason and, when it impersonates a user, that user's typed
primary email; is recorded before it is sent and refused if it cannot be
recorded; and keeps content out of the log (counts, flags, no file names,
addresses or link URLs). Neo Box calls only metadata endpoints (`drives.list`,
`files.list` with a field mask that excludes content-bearing fields,
`permissions.list`); it never calls `files.get?alt=media`, `files.export` or
any download. Because `drive.readonly` could read content, the shared-drive
inventory documents this plainly and Neo Box's own call list is the control.

**No permission changes in the first versions.** Revoking a public link or an
external permission needs `drive`, which is the widest scope in the family. It
needs a separate decision once the read side shows what is worth acting on, and
the same write-ahead operation log as other Workspace changes.

**Reads stay live and unstored.** Activity and usage are read from Google when
a page opens, with Google's retention and delay (usage reports lag by a few
days; activity by minutes to hours; retention is 180 days). Bounded scans and
explicit partial results follow ADR 0007.

## Consequences

- The first version is useful without granting anything that can read a file.
- Shared-drive inventory and per-user file listing wait for the second and
  third versions, behind their own opt-in and logging.
- Drive activity shows what Google reports, including actions taken outside
  Neo Box, and cannot show the current state of a file's sharing.
