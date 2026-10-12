# ADR 0007: Workspace writes require a local operation record

Workspace mutations are recorded at the Google HTTP write boundary, after
resource ownership checks, so adding a new RPC cannot silently bypass logging.
A pending record must be stored before sending; if that fails, the write is not
sent. Outcome persistence uses a short context independent of request cancellation.
The database record and Google change cannot be atomic: unconfirmed writes become
unknown, and interrupted processes or failed outcome writes leave pending records.
Neither status is automatically retried or interpreted as success.

Raw RPC messages and credentials are not suitable audit payloads. Records use an
allowlist of resource fields, plus a password-changed flag, never password values,
private keys, token strings, request headers or Google's raw error messages.
Tenant configuration changes are also logged. Tenant removal retains history;
connection removal deletes it with the provider's other local data, matching the
existing Connection lifecycle. Generic Connection creation, key rotation and
removal are outside this Workspace-specific operation log.

Google audit activities remain a separate live read through the Reports API with
an optional read-only scope. They include changes made outside Neo Box and are
not persisted as local operations. Security and cross-tenant overviews also read
live, with bounded scans and explicit partial/unknown results; all returned pages
being read does not prove that a restricted administrator can see every account.
Cross-credential counts are deduplicated by Customer ID plus immutable user ID.
