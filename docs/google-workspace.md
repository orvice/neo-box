# Google Workspace user management

One Google Workspace Connection stores one reusable Service Account key. Attach
multiple independent Workspace organizations as Tenants of that Connection.
Each Tenant has its own Customer ID, delegated administrator, and last
successful or failed authorization check. Multiple domains within one
organization use the same Tenant.

## Setup

1. In the Service Account's Google Cloud project, enable the **Admin SDK API**.
2. Create/download a Service Account JSON key. In Neo Box, add a **Google
   Workspace** Connection and upload or paste the key. The private key is
   encrypted with `crypto.encryption_key` and never returned by the API.
3. In **each independent organization's** Google Admin console, a super
   administrator opens **Security → Access and data control → API controls →
   Manage Domain Wide Delegation** and authorizes the same numeric Service
   Account Client ID for:

   ```text
   https://www.googleapis.com/auth/admin.directory.user,https://www.googleapis.com/auth/admin.directory.domain.readonly
   ```

   The user scope supports reading and managing users. The domain read scope
   allows Neo Box to check that a new user's email domain belongs to the
   selected organization before sending the creation request.
4. Find the organization's Customer ID under **Account → Account settings →
   Profile**. In the Connection's page, add a Tenant with this explicit `C...`
   ID and an administrator email **from that organization**. The administrator
   must have the privileges for the user operations you need. `my_customer`
   and a domain name are not accepted as Customer IDs.
5. Neo Box verifies that the delegated administrator belongs to the configured
   customer and can read its users before saving. This read check does not
   prove write privileges; Google checks those on each operation. Delegation
   changes may take time to propagate.

See Google's [Service Account delegation guide](https://developers.google.com/identity/protocols/oauth2/service-account#delegatingauthority),
[Directory users API](https://developers.google.com/workspace/admin/directory/reference/rest/v1/users),
and [domain list API](https://developers.google.com/workspace/admin/directory/reference/rest/v1/domains/list).

## Available operations

- List users with cursor pagination and Google's Directory query syntax; click
  an email to read current user details.
- Create a user on a verified primary or secondary domain of the selected
  customer. Domain aliases are not user creation domains.
- Edit given name, family name and organization unit path. The user identity,
  administrator role, aliases and other unshown fields are preserved.
- Reset passwords and optionally require a password change at next sign-in
  (enabled by default in the form).
- Suspend or restore access. Suspension retains the account and its data.

Users are read live, with no background directory sync. Neo Box resolves each
existing user and checks its Customer ID before making a change, and uses its
immutable Google ID for the write. A user belonging to another customer is
rejected even when the shared Service Account can access that customer.

Each request obtains a delegated token for that Tenant's administrator;
credentials and tokens are not reused across Tenant clients. Reads retry
transient failures. Mutations are sent once. If Google does not confirm a
mutation, refresh to inspect the current user before deciding whether to retry.
Passwords and Google's raw error messages are not stored in Tenant settings or
returned in user responses.

## Optional directory features

Authorize these extra scopes **per tenant** for the features you want. Keep the
two original scopes above. Additional scopes are requested only when using the
corresponding feature; they are not added to base user listing, profile updates,
password resets, suspension, or the Tenant's authorization test.

| Feature | Additional scope |
| --- | --- |
| User aliases | None; existing `admin.directory.user` suffices |
| Groups and members | `https://www.googleapis.com/auth/admin.directory.group` |
| Organization units | `https://www.googleapis.com/auth/admin.directory.orgunit` |
| Sign-out and application grants | `https://www.googleapis.com/auth/admin.directory.user.security` |

OAuth scope authorization and the delegated administrator's Google privileges
are both required. Feature failures are shown beside that feature and do not
change the Tenant's base user health. No new key upload is needed when extending
delegation for the same Service Account.

- **Aliases:** open a User's details → Aliases to list/add/remove email aliases.
  Google-generated aliases from domain aliases are shown as managed by Google
  and cannot be removed here. Added aliases must be on a verified primary or
  secondary domain of the selected tenant.
- **Groups:** select a Tenant → Groups to search/page groups, create/edit/delete
  them, and manage direct members with Member/Manager/Owner roles. Group email
  changes are validated against this customer's domains. External members and
  nested groups are accepted when Google permits them. Unshown group and member
  settings are preserved. User details → Groups lists memberships within this
  tenant only.
- **Organization units:** select a Tenant → Organization units to view its tree,
  create/edit/move/delete units. The root cannot be edited/deleted, and moves
  into the unit's own subtree are rejected. Edit a User's profile to choose a
  new OU from the selector. When this optional scope is unavailable, the
  existing manual path input remains usable. Moving a unit may change inherited
  Google policies for users in its subtree.
- **User security:** User details → Security supports forced sign-out and viewing
  or revoking third-party application grants. Sign-out resets web/device
  sessions and sign-in cookies; application grants are revoked separately.
  Revocation removes the application's existing access tokens for that user,
  and does not permanently block the application from being authorized again.
  Only client IDs and granted scopes are displayed; actual tokens are never
  exposed or stored.

Deleting groups or organization units changes Google resources and requires an
explicit confirmation in the UI. Google enforces deletion requirements and
administrator privileges. These operations, alias removal, member removal,
sign-out and grant revocation retain the same single-send/unknown-outcome rules
as other mutations. Refresh after an unconfirmed result before retrying.

API references: [aliases](https://developers.google.com/workspace/admin/directory/reference/rest/v1/users.aliases),
[groups](https://developers.google.com/workspace/admin/directory/reference/rest/v1/groups),
[members](https://developers.google.com/workspace/admin/directory/reference/rest/v1/members),
[organization units](https://developers.google.com/workspace/admin/directory/reference/rest/v1/orgunits),
[sign-out](https://developers.google.com/workspace/admin/directory/reference/rest/v1/users/signOut),
and [application grants](https://developers.google.com/workspace/admin/directory/reference/rest/v1/tokens).

## Operations, security overview and Google audit

### Neo Box operation history

A Connection's **Neo Box operations** tab lists all of its Workspace changes;
a Tenant's tab filters that history. Search by actor, action, tenant or target,
filter by result, and open an operation to inspect its sanitized requested
change and confirmed result.

This history starts when this version is deployed. It covers Google writes
through Workspace management and local Tenant create/update/remove operations.
It does not cover changes made directly in Google, nor the generic Connection
creation, key rotation or Connection removal APIs. Google-side resource checks
and local validation happen before a Google mutation is recorded.

- **Pending:** recorded before sending, with no persisted outcome yet. The
  request may still be running, the server may have stopped, or recording its
  outcome may have failed. It does not prove the change happened.
- **Succeeded:** Google confirmed the change, or the local Tenant change
  completed, and its outcome was recorded.
- **Failed:** the change was rejected or local configuration storage failed.
- **Unknown:** Google did not confirm the result. Refresh the resource before
  deciding whether to retry; Neo Box never automatically resends the change.

If the pending record cannot be saved, the mutation is refused before sending.
A failure to save the outcome is reported to the caller and leaves pending
history. Password reset entries contain only `password_changed: true` and
non-secret settings. Passwords, keys, bearer tokens and raw provider errors are
not logged. Removing a Tenant retains its history and old name; removing the
Connection removes the local history with its Tenant configurations.

### Security and cross-tenant overview

Open **Tenant overview** from the Google Workspace Connection list to read
across Service Accounts. Its selector can limit reads to one Service Account.
The Connection's Overview tab covers its Tenants; a Tenant's Account security
tab covers that Tenant alone. Click an account to inspect its current profile,
aliases, memberships and security actions in the correct Tenant context.

The overview shows observed users, active/suspended users, administrators,
administrators without two-step verification, inactive accounts and unknown
security data. Filters support account names/emails, organization paths, tenant
names, administrator status, suspension and the security findings.

Two-step enrollment and enforcement are separate optional fields. Missing
values are **unknown**, never treated as disabled. An administrator-without-
two-step finding requires known administrator status and known false enrollment.
Last-login data distinguishes a missing/unparseable value from Google's epoch
sentinel for a user who never logged in. The default inactivity threshold is
90 days (configurable from 1 to 3650); never-used accounts are considered inactive
only when a known creation date is also older than the threshold.

Counts and rows are deduplicated by Customer ID plus immutable Google user ID
when multiple connections access the same customer. A more complete readable
record is preferred. Tenant rows still show each configured credential's own
read result. User/group scans read all pages up to 5,000 entries per Tenant, with
at most 20 configured Tenants per request, four concurrent reads, 20-second
Tenant time limits and a 60-second overall limit. Larger setups can select a
Service Account or inspect one Tenant.

A failure, timeout or scan cap is shown explicitly, and observed counts are
marked partial. Search/pagination operate only on observed accounts. Reading
all returned pages does not prove visibility of accounts outside the delegated
administrator's privileges. Reads are live and not an atomic directory snapshot;
refresh/page/filter changes may observe later Google changes. Group counts are
optional and require the existing group scope; a missing group permission does
not stop user/security reads.

### Google audit activities

Authorize this additional scope in **each Tenant** that needs audit access:

```text
https://www.googleapis.com/auth/admin.reports.audit.readonly
```

Keep the existing base scopes. The delegated administrator must also have the
required Google report privileges. This scope is requested only for the Google
audit feature and does not change base user or directory feature authorization.

A Tenant's **Google audit** tab reads login, admin, groups, groups enterprise,
token or user-account events. Filter by an existing Tenant user's email/ID,
event name and local-time range; results use Google's cursor pagination. Empty
user filters read the configured customer. The default window is the last seven
days; explicit windows must be within the last 180 days. Times are sent to Google
as RFC3339 UTC values. Deleted users are not supported by the user filter in this
version.

Every Reports request supplies the Tenant's explicit Customer ID, and returned
activity Customer IDs/application names are checked before showing results.
The detail dialog shows event parameters and actor/IP metadata as provided;
missing actor/IP fields are not invented. Known credential parameter names are
redacted, Google's `sensitiveParameters` are not decoded, and large integer
parameter values remain strings. Events are read live, not stored or streamed;
Google's delays, report privileges, edition and event availability determine
what can be read. No automated audit alerts are enabled in this version.

See Google's [Reports activities API](https://developers.google.com/workspace/admin/reports/reference/rest/v1/activities/list)
and [Directory user fields](https://developers.google.com/workspace/admin/directory/reference/rest/v1/users).

## Gmail settings

A User's **Gmail** tab shows that mailbox's *settings* — auto-reply, automatic
forwarding, forwarding addresses, delegates, filters and send-as addresses —
and lets you change the auto-reply. It never reads or changes messages.

Authorize this additional scope **per tenant**, keeping the base scopes:

```text
https://www.googleapis.com/auth/gmail.settings.basic
```

This scope reads and changes settings only; it does not allow reading mail.
Neo Box requests no other Gmail scope. Changing forwarding, delegates and
send-as would need `gmail.settings.sharing`, which Neo Box does not use.

**Understand what you are authorizing.** Gmail settings can only be reached as
the mailbox owner, so Neo Box asks Google for a token whose subject is the
target user, not the delegated administrator. Domain-wide delegation of this
scope lets the Service Account reach *any* user's Gmail settings in the
organization; the delegated administrator's own privileges do not narrow it.
Neo Box's checks are the control, and they are accountability rather than
authorization:

- The target is first resolved inside the Tenant (Customer ID checked); an
  address from another organization, or one Directory cannot find, never gets a
  token. The email you type must be the user's **primary** address, not an alias.
- Every request needs a one-line **access reason** (up to 200 characters) and the
  target's email typed again. The backend checks both on each request.
- Every read and write is recorded before it is sent, with the reason, and the
  request is refused when it cannot be recorded. Find these under **Neo Box
  operations** by searching `gmail`. Records hold counts, flags and dates (for
  example "3 filters, 1 forwarding, external destination: yes"), never the
  auto-reply text, filter criteria, label names or addresses.
- One user per request. The page holds your confirmation for ten minutes and
  nothing refreshes it automatically; **Read again** is a new, recorded access.

Each section is read independently. A section Gmail refused or does not offer
for this mailbox shows why instead of values, and is never shown as "off". A
missing Gmail authorization is reported on this tab only; it does not change
the Tenant's health. Delegates are only available to Service Accounts using
domain-wide delegation.

Addresses are marked **outside the tenant** when their domain is not one of the
Tenant's verified domains. Domain aliases are not included in that list, so an
address on a domain alias can be marked as outside. If the domains cannot be
read, addresses show **not checked** rather than a guess. Filters are flagged
when they forward, delete (move to trash) or skip the inbox. These flags raise
no alerts.

**Auto-reply.** Saving replaces the whole auto-reply: subject, plain-text
message, first/last day, and the contacts / organization restrictions. A stored
HTML message is replaced by the plain text you see, and a start or end time you
clear is removed. Like other writes it is sent once; if Google does not confirm
it, read again before retrying.

See Google's [Gmail settings API](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.settings)
and [scopes](https://developers.google.com/workspace/gmail/api/auth/scopes).

## Drive governance

A Tenant's **Drive** tab shows Drive sharing activity, per-account storage use
and the domain's shared drives; a user's **Drive** tab lists that user's
exposed files. Sharing activity and storage use are built on Google's Reports
API only; the shared drive list and exposed files use Drive API scopes and are
described under "Drive API reads" below.

Authorize in **each Tenant** that needs it (the first is the same scope as the
Google audit tab):

```text
https://www.googleapis.com/auth/admin.reports.audit.readonly
https://www.googleapis.com/auth/admin.reports.usage.readonly
```

The delegated administrator also needs Google's report privileges. Each scope
is requested only by its own view; a missing scope or privilege is reported
beside that view and does not change the Tenant's health.

**Sharing activity** reads Drive audit events and filters them at Google by
preset: public on the web, anyone with the link, shared outside the domain,
ownership transfers, downloads, deletions, moved to trash, shared drive
members, shared drive settings, or all activity. A row shows who acted, the
file title and owner as Google reports them, what changed (visibility, role,
target user or domain) and an exposure badge. A value Google did not return is
shown as not provided, and an unrecognised visibility is shown as unknown, never
as private. One user action can emit several events; only the user's direct
action (`primary_event`) is shown unless "Include side effects" is on, and the
page says how many events it hid. Time range, user filter, cursor paging and the
180-day window match the Google audit tab. Events arrive minutes to hours late.

**Storage usage** reads Google's daily per-user usage report: Drive used, quota
and share of quota, sortable and searchable. The report lags by a few days, so
Neo Box uses the newest day that has data and shows its date. No data is shown
as "no data", never as zero; an account without a figure shows Unknown, and an
unlimited quota shows no percentage. At most 5,000 accounts and 20 seconds are
read per Tenant, and a larger or slower read is marked incomplete.

Both views read live from Google and store nothing; neither is written to the
Neo Box operation log.

### Drive API reads

Two further reads use Drive API scopes. They are separate opt-ins, each
requested only for its own request, never together in one token, and neither is
needed for anything above. Neo Box never requests the full `drive` scope and
never downloads, exports or opens a file.

| Read | Scope | Subject of the token | Can the scope read content? |
| --- | --- | --- | --- |
| Shared drive list (Tenant → Drive → Shared drives) | `https://www.googleapis.com/auth/drive.readonly` | the Tenant's delegated administrator | **Yes** |
| A user's exposed files (User → Drive) | `https://www.googleapis.com/auth/drive.metadata.readonly` | that user | No |

`drive.readonly` is the only scope Google accepts for listing shared drives
(`drives.list`). It could read file content, and domain-wide delegation lets the
Service Account act as any user of the Tenant once it is authorized, so Neo
Box's own behaviour is the control: it calls only `drives.list`, always with a
field mask for the drive's name, creation time, organizational unit and
restrictions, and keeps no copy. If you do not want a content-capable scope on
the Service Account, do not authorize it; the other Drive views keep working.

Both reads follow the Gmail settings rules (ADR 0008): each request carries an
access reason (1-200 characters, one line), a user's files also need that
user's primary email typed again, the read is recorded in the Neo Box operation
history **before** it is sent (and refused if it cannot be recorded), and the
log keeps counts and flags only, never drive or file names, links or domains.
The dashboard remembers a confirmation for ten minutes in page memory; the
backend checks the reason on every request and keeps no session. Paging or
changing a filter is another read and another record. Missing authorization is
shown beside the view and does not change the Tenant's health.

**Shared drives** lists name, creation date, organizational unit ID and the
restrictions Google reports as set (domain users only, members only,
admin-managed restrictions, no copy for readers, only organizers share
folders). It can filter by name prefix, "no organizer", "at most N members" and
organizational unit ID; Google evaluates those filters, so Neo Box does not
read members. The organizational unit appears as the ID Google returns. A
restriction that is not listed is not claimed to be off.

**Exposed files** lists the user's own, untrashed files that are visible beyond
named people: anyone can find, anyone with the link, domain can find, domain
with the link. Each row shows the exposure from the file's permissions; a
domain exposure is marked "outside the tenant" only when the Tenant's verified
domains could be read, otherwise "not checked". A file for which Google
returned no permissions says so. Files in shared drives have no owner and are
not listed; Neo Box lists one page at a time and never scans every user.

## Key rotation and removal

Edit the Connection and upload another key for the **same** Service Account.
Every Tenant uses the new key on its next request; test each Tenant afterward.
A different Service Account needs a new Connection and tenant authorization.

Removing a Tenant removes only its Neo Box configuration. Removing the
Connection removes its local Tenant configurations. Neither action deletes
Google users or revokes delegation in Google; revoke authorization separately
in each organization's Admin console when appropriate.

## Validation

Unit/service tests use a simulated Google Directory API, including two
independent tenants sharing one key, cross-customer refusal, verified-domain
checks, pagination, partial updates, permission errors and non-retried writes.
Directory extension tests also cover editable/Google-managed aliases, later-page
group ownership, membership filtering across tenants, external members and role
changes, OU root/cycle protection, scope isolation, and security write failures.
Frontend tests cover organization hierarchy ordering and valid parent choices.
Observability tests cover write-ahead failure, cancelled-request finalization,
secret exclusion, outcome uncertainty, optional report scopes, audit pagination
and tenant isolation, multi-value parameter encoding, unknown security fields,
never-used account age and deduplicated/partial cross-tenant scans.
PostgreSQL repository tests use `NEOBOX_TEST_POSTGRES_DSN` and an isolated schema.
Gmail tests cover refusal before any token is requested (missing reason, wrong
or alias target, other-tenant user, other owner), write-ahead logging of reads
and writes, content-free logs, independent sections, unknown external status,
missing authorization, validation and single-send vacation updates.
Real Google key exchange and operations need authorized test tenants and have
not been exercised against real Workspace accounts in this implementation.
