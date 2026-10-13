# ADR 0008: Gmail settings are read by impersonating one user, and every access is logged first

Status: proposed (2026-10-13)

Gmail settings (vacation responder, forwarding, delegates, filters, send-as)
can only be reached as the mailbox owner: the Gmail API takes the user as the
token subject, and a Tenant's delegated administrator is not that user. Neo Box
therefore asks the Tenant's Service Account for a token whose subject is the
target Workspace User. This is the first Workspace feature that acts as someone
other than the delegated administrator, so it needs its own boundaries.

## Decisions

**No mail content, ever.** Neo Box requests only `gmail.settings.basic` and
`gmail.settings.sharing`. Neither scope reads or changes messages. Neo Box does
not request `gmail.readonly`, `gmail.modify`, `mail.google.com` or any Drive
scope for this feature. Reading message content is a separate decision and
would need its own ADR.

**The subject is resolved, not supplied.** The target email is first resolved
through the Directory API with the Tenant's administrator, and its Customer ID
must equal the Tenant's. Only then is a token with the user's immutable primary
email as subject requested. A user of another customer, an alias of another
user, or an address Directory cannot find never reaches the token exchange. The
token is used for one request and is not cached or shared across users or
Tenants, as with Directory clients.

**Reads are logged before they are sent.** A read is an access to a person's
mailbox configuration, so it follows ADR 0007's write-ahead rule: a pending
Workspace operation is stored first and, if that fails, nothing is sent. Reads
and writes both use the `workspace_operations` table, with new actions
(`gmail.settings.read`, `gmail.vacation.update`, …). A read's outcome is
`succeeded` or `failed`; an unreachable outcome write leaves `pending`, never
a guess. Reads are retried like other reads; writes are sent once.

**Every session starts with a reason and a typed target.** Before any Gmail
settings request for a user, the caller supplies a required free-text reason
(1–200 characters) and the target email typed again. Both are validated by the
backend, not only by the form, and the reason is stored on each operation it
covers. A confirmation covers one user in one Tenant and expires after a
short, fixed time; it is not a bearer capability. The backend rejects a
request whose reason or typed target is missing, so a script cannot skip the
form.

**One user per request.** No endpoint accepts a list of users. A scan of "who
forwards externally" would impersonate every user in a Tenant; it needs a
bounded, administrator-selected scope and its own decision, and is not part of
this feature.

**Logs hold structure, not content.** Operation records for Gmail contain the
action, tenant, target user, reason, and an allowlisted summary: whether a
vacation responder is enabled and its date window, counts of filters and
forwarding rules, whether any destination is outside the Tenant's domains.
They never hold response subject or body text, filter criteria strings, label
names, forwarding or delegate addresses, tokens, or Google's raw error
messages. Detail screens show current settings read live; they are not stored.

**Failures stay beside the feature.** A missing Gmail scope or a mailbox that
Gmail refuses (for example an unlicensed or suspended user) is shown on the
Gmail settings view, with the Tenant's base health untouched.

## Consequences

- The operation history now records reads. Its volume is bounded by explicit
  user actions, and the history screen needs a filter for reads.
- Delegated administrator privileges no longer limit what Neo Box can do with
  Gmail settings: once a Tenant authorizes the Gmail scopes for the Service
  Account, the Service Account can reach any user's settings, so Neo Box's
  checks above are the control. Documentation must say this plainly when it
  tells an administrator to authorize the scopes.
- Impersonation depends on domain-wide delegation, so Neo Box cannot add
  per-user consent. The reason and typed target are accountability, not
  authorization, and are shown as such.
