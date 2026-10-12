# ADR 0006: One Workspace Connection owns multiple tenants

Status: accepted (2026-10-12)

Google Workspace management must reuse one Service Account across independent
organizations. A Workspace Connection therefore holds one encrypted key and its
public identity; provider-specific Workspace Tenants hold Customer IDs and
delegated administrator emails. This revisits ADR 0003's assumption that every
Connection represents exactly one third-party account, while retaining its
provider-specific resource boundary.

Duplicating the key into one Connection per tenant makes rotation inconsistent.
A generic credential vault would change every existing Provider without a second
use case. Keeping tenants within this Provider gives one key rotation and
independent authorization checks and health per organization. Credential rotation
must keep the same Service Account identity. Removing a tenant or connection
removes local configuration only, never Google users or Google authorization.

User reads and mutations check the Customer ID; a user ID from another tenant is
not accepted even when the shared Service Account can access both. No tenant's
delegated token is shared with another tenant. Writes are sent once; transport
failures or server errors have an unknown outcome and are not retried.

Optional directory features request their additional scope only for the request
that needs it: groups/members, organization units, or user security. Base user
requests and tenant verification retain their original scopes, so a tenant can
continue using them without granting the new capabilities. Feature permission
failures do not change base user health.

Groups have no Customer ID field. Group mutations first locate the group in the
explicit customer's paged group list, then use its immutable ID. User membership
queries cannot combine customer and userKey in Google; their results are
intersected with the customer's group IDs before being exposed. Organization
units use customer-qualified endpoints, and user security operations resolve and
check the user's Customer ID before writing.
