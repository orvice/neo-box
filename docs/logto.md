# Logto sign-in and existing data

Neo Box uses Logto as its only identity provider. It no longer registers the
local password login, OAuth exchange, user-management or local-session handlers.
The existing `AuthService/Me` RPC returns the authenticated business owner; other
authentication RPCs return Unimplemented after authentication. Legacy passwords,
sessions and OAuth states cannot authenticate requests. They are not imported
into Logto, and no local user is created when someone signs in.

This deployment has one owner. Only the configured Logto user ID (`sub`) can
access its data, even if other people can register in the same Logto tenant.
An arbitrary first sign-in never claims existing data. Emails are not used for
account linking. The stable `data_owner_id` identifies business records and is
independent of the Logto user ID, so a future identity change only requires
changing `owner_subject`.

## Configure Logto

1. Create a **Single-page application**, and record its App ID and Endpoint.
2. Register the dashboard's exact redirect URI: `https://neobox.example.com/auth/callback`.
3. Register its exact post-sign-out redirect URI: `https://neobox.example.com/sign-in`.
   For local development use `http://localhost:5173` as the dashboard origin.
   Configure allowed origins in Logto for the dashboard origin if required.
4. Create an API resource, for example `https://neobox.example.com/api`, with
   an `access` permission. Give your Logto user a role granting that permission.
5. Copy your user's **User ID**, not their username or email, to `owner_subject`.

Configure the backend (no SPA client secret or Management API credentials):

```yaml
auth:
  logto:
    endpoint: "https://identity.example.com"
    app_id: "<SPA App ID>"
    resource: "https://neobox.example.com/api"
    owner_subject: "<Logto User ID>"
    data_owner_id: "personal"
    scopes: ["access"]
    adopt_existing_data: false
```

`endpoint` must be HTTPS; HTTP is accepted only for loopback development.
Startup fails if configuration or OIDC discovery is invalid. The public
`GET /api/auth/config` response contains only the SPA Endpoint, App ID, resource
and scopes; owner and adoption settings remain on the backend. The frontend
uses the official Logto Browser SDK to manage authorization code / PKCE,
state, tokens and refresh. Both ConnectRPC and snapshot downloads request the
API resource's access token through the SDK. ID tokens are not sent to the API.

The backend checks signature, issuer, resource audience, expiry, configured
permissions and the allowed user ID. Organization tokens are refused. A wrong
identity or missing permission returns 403; an invalid token returns 401.
Profiles and sign-in methods are managed in Logto. Neo Box's Profile page is
read-only, and its old user-management page has been removed.

## Keep all existing business data under the owner

1. Stop all old Neo Box instances and make a backup of PostgreSQL and blob
   storage. Keep the existing `crypto.encryption_key`, DB configuration and
   blob storage configuration. Stored connection and notification secrets still
   need that encryption key.
2. Fill in the Logto configuration above. Keep `data_owner_id: "personal"`, or
   deliberately choose another stable owner before the first migration.
3. Set `adopt_existing_data: true` for the first startup. This explicitly adopts
   **every existing business owner's data** into the configured owner.
4. Start the new version. Adoption completes before notification, backup and
   sync workers start, and before authenticated routes become available.
5. Sign in and verify connections, snapshots, attachment restores, policies,
   usage and notification channels. Set `adopt_existing_data: false` afterward.

With adoption disabled, startup refuses to proceed if business rows belong to
another owner. It does not silently start with an empty dashboard.

The migration keeps connection IDs, provider settings, encrypted credentials,
snapshot IDs and content keys, restore history, backup policies, Wasabi data,
Workspace tenants, operation history, notification channels and alerts.
Historical actor information in operation logs remains as recorded.

NocoDB attachment keys contain the old owner ID. Adoption copies every legacy
attachment to the new owner's path and checks its SHA-256 and size before
committing SQL changes. Copies are streamed through temporary files, not kept
in memory. Equal hashes share one file metadata record after consolidation;
all snapshot references remain. Existing snapshot content keys remain unchanged.
Original blobs are retained. Allow storage and temporary disk capacity for the
copies; the migration needs both read and write access to the existing store.

Alert keys include their old owner during adoption so alerts with matching
per-user dedupe keys are all retained. SQL changes commit in one transaction;
if any attachment is absent/corrupt or a write fails, startup stops without
changing ownership. Original content remains available for retry. A failed SQL
commit can leave extra copies at the destination, which are safe to overwrite
on retry. Completed adoption is idempotent.

No authentication table is dropped automatically. Old user information is not
used or copied into the new identity. Once the cutover and backups are verified,
those legacy authentication rows can be removed separately. Rolling back the
application after consolidation requires restoring the database backup; keep
the original blobs until that rollback window has closed.

References: [Logto Browser SDK](https://docs.logto.io/quick-starts/vanilla-js),
[Logto Gin API protection](https://docs.logto.io/api-protection/go/gin).
