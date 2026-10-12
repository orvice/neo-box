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
PostgreSQL repository tests use `NEOBOX_TEST_POSTGRES_DSN` and an isolated schema.
Real Google key exchange and operations need authorized test tenants and have
not been exercised against real Workspace accounts in this implementation.
