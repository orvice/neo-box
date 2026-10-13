# ADR 0009: Drive governance reads from Reports first; Drive API scopes are a separate, logged step

Status: accepted (2026-10-13); the Drive API steps were verified against Google's documentation on 2026-10-14

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

## Verified scope facts (2026-10-14)

- `drives.list` and `drives.get` accept only `drive` or `drive.readonly`. With
  `useDomainAdminAccess=true` they return every shared drive of the domain, and
  `drives.list` accepts `memberCount`, `organizerCount` and `orgUnitId` in `q`,
  so an inventory and "no organizer" filter need no member listing.
- `permissions.list` and `files.list` accept `drive.metadata.readonly`.
  `files.list` can filter by `visibility` (`anyoneCanFind`, `anyoneWithLink`,
  `domainCanFind`, `domainWithLink`, `limited`) and `'me' in owners`.

## Implementation

Each Drive API client gets exactly one of the two scopes
(`drive.readonly` for `drives.list` as the Tenant's administrator,
`drive.metadata.readonly` for `files.list` as the target user) and refuses the
other call, so a files read can never run with the content-capable scope. A
request carries the access reason, and a user's files also the typed primary
email resolved through the Directory API inside the Tenant, exactly as in ADR
0008. Shared drive **members** are not read in this version; reading them would
use `permissions.list` per drive and is a separate decision. A scan across all
users for exposed files remains out of scope.

## Consequences

- The first version is useful without granting anything that can read a file.
- Shared-drive inventory and per-user file listing are separate opt-ins with
  their own logging; authorizing `drive.readonly` is the one step that puts a
  content-capable scope on the Service Account, and the documentation says so.
- Drive activity shows what Google reports, including actions taken outside
  Neo Box, and cannot show the current state of a file's sharing.
