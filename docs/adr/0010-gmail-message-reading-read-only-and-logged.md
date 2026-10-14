# ADR 0010: Gmail messages are read through `gmail.readonly`, one user at a time, and every access is logged without content

Status: accepted (2026-10-14); API behavior checked against Google's Gmail API
documentation on 2026-10-14

ADR 0008 let Neo Box impersonate one Workspace User to read and change that
user's Gmail *settings*, and ruled out message content ("reading message
content is a separate decision and would need its own ADR"). This is that
decision, for **reading** messages, labels and attachments. Changing messages
(read state, labels, archive, trash, delete) and sending are not part of it.

## Decisions

**Read-only scope.** Neo Box requests `gmail.readonly` for mail reading, and
nothing broader: not `gmail.modify`, `gmail.compose`, `gmail.send` or
`mail.google.com`. Google lists `gmail.readonly` as a restricted scope. It
accepts it for `messages.list` / `get`, `labels.list` / `get` and
`messages.attachments.get`. `gmail.metadata` is not enough: it cannot use the
`q` search parameter. The scope is requested only for mail requests; Directory
operations, the Gmail settings feature (`gmail.settings.basic`) and other
optional features keep their own scopes. Reading never marks a message read:
`messages.get` does not change labels. Changing messages would need a new ADR
and `gmail.modify`.

**Same impersonation rules as ADR 0008.** The target is resolved through
Directory, its Customer ID must equal the Tenant's, and only then is a token
with the user's primary email as subject requested, used for one request and
never cached. Each request carries a required reason (1–200 characters) and the
target's primary email typed again, checked by the backend every time. There is
no endpoint that accepts a list of users and no cross-user search: finding "who
received X" would read every mailbox in a Tenant and needs its own bounded,
administrator-selected scope and decision.

**Reads are logged before they are sent, per access.** Following ADR 0007, a
pending Workspace operation is stored first and nothing is sent if that fails.
Each of these is one operation with its own action: `gmail.labels.list`,
`gmail.messages.search`, `gmail.message.read`, `gmail.attachment.download`.

**Logs hold structure, not content.** An operation stores its action, tenant,
target user, reason, and an allowlisted summary:

- search: which Gmail operators the query used (names only, such as `from`,
  `has`, `newer_than`, from a fixed list), whether a label filter was applied,
  the page size, and the number of results returned. Not the query text, not
  operator values, not label names.
- message read: the message ID, the number of attachments, and whether an HTML
  part was present.
- attachment download: the message ID, the part ID, the attachment's MIME type
  and size. Not its file name.
- labels list: the number of labels.

Never stored: subjects, bodies, snippets, addresses (from / to / cc), file
names, query values, tokens, or Google's raw error text. A message ID is stored
because it identifies what was opened without revealing it; Google can resolve
it, Neo Box's log cannot. The detail the page shows is read live and never
stored.

**Live, bounded, and not stored.** Messages, labels and attachments are read
from Gmail on each request and not kept in PostgreSQL or blob storage.

- A search page holds at most 50 messages. Gmail's `messages.list` returns only
  IDs, so Neo Box then reads each message's headers with a bounded number of
  concurrent requests and shows a result whose headers could not be read as
  unavailable rather than dropping it.
- Gmail returns an attachment as base64url inside a JSON response, so it cannot
  be streamed: it is held in memory for that one response, bounded at **25 MB**,
  and never persisted. Neo Box refuses an attachment declared larger than that
  before fetching any bytes; the limit is Neo Box's own, so a larger file must be
  taken from Gmail directly. Gmail's attachment IDs change between reads, so a
  download reads the message again and finds the part by its part ID.
- This feature has no writes; reads retry transient errors like other reads.

**Showing a message safely.** The detail view shows headers and the plain-text
part. An HTML part is shown only inside an iframe with an empty `sandbox`
attribute and a Content-Security-Policy that blocks scripts, remote images and
every other external request. Without that, opening a message would run its
markup in the dashboard's origin and would tell the sender, through tracking
images, that an administrator had opened it.

**Failures stay beside the feature.** A missing `gmail.readonly` authorization,
or a mailbox Gmail refuses (unlicensed or suspended), appears on the Mail view
only and never changes the Tenant's health, as with Gmail settings.

## Consequences

- Once a Tenant authorizes `gmail.readonly` for the Service Account, the
  Service Account can read *any* user's mail in that Tenant; the delegated
  administrator's own privileges do not narrow it. Neo Box's checks are
  accountability, not authorization, and the documentation says so when it
  tells an administrator to authorize the scope.
- Operation volume grows with explicit actions, including one operation per
  opened message and per attachment. The history already searches by action, so
  searching `gmail` finds these too.
- Without the query text in the log, an after-the-fact review shows that and
  how a search was made, not what was searched for. This is deliberate.
- Because Google treats `gmail.readonly` as restricted, an organization that
  runs its own Workspace Tenant can authorize it for its own Service Account,
  but Neo Box could not be offered as a public OAuth app with it without
  Google's security assessment. Neo Box's model (a Service Account the
  administrator owns) does not go through that process.
- ADR 0008's sentence "Neo Box does not request `gmail.readonly` ... for this
  feature" still holds for *settings*; mail reading is a separate feature with
  its own scope.
