# Logto sign-in and existing data

Neo Box uses Logto as its only identity provider. It no longer registers the
local password login, direct-provider OAuth or user-management handlers.
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

1. Create a **Traditional web** application and record its Endpoint, App ID
   and App Secret. The secret is used exclusively by the Go backend.
2. Register `https://neobox.example.com/api/auth/callback` as the redirect URI.
3. Register `https://neobox.example.com/sign-in` as the post-sign-out redirect URI.
4. Copy your user's **User ID**, not their username or email, to `owner_subject`.
   This BFF authenticates its single owner directly; it does not require a
   separate API Resource or browser-issued API tokens.

```yaml
auth:
  logto:
    endpoint: "https://identity.example.com"
    app_id: "<Traditional web App ID>"
    app_secret: "<App Secret>"
    public_url: "https://neobox.example.com"
    owner_subject: "<Logto User ID>"
    data_owner_id: "personal"
    session_ttl: 168h
    adopt_existing_data: false
```

Serve the frontend and backend under the `public_url` origin: route `/api/`
and `/ping` to Go, and other paths to the frontend. Keep `VITE_API_BASE_URL`
empty. This is also the repository's existing nginx/reverse-proxy layout.
For local development use `public_url: "http://localhost:5173"`; Vite proxies
`/api/` to Go. Register `http://localhost:5173/api/auth/callback` and
`http://localhost:5173/sign-in` in the Traditional web application.

`endpoint` and `public_url` require HTTPS; HTTP is accepted only on loopback.
`public_url` is a fixed origin, without path, query or fragment. It controls
callbacks, post-login navigation and the allowed browser Origin. Preserve the
existing `crypto.encryption_key`; it is now also required to seal OIDC state
and tokens. Put the App Secret in the deployment's private backend configuration.
Startup fails closed if configuration, discovery or encrypted storage is invalid.

## Server-side login and sessions

The backend uses OIDC discovery, `go-oidc` and `oauth2` for the authorization
code flow with PKCE. `GET /api/auth/login` stores the random state, nonce,
PKCE verifier and confined return path on the server, then redirects to Logto.
The `/api/auth/callback` handler atomically consumes the flow, checks its cookie
and state, exchanges the code using the App Secret and PKCE verifier, and
verifies the ID token's signature, issuer, application audience, expiry, nonce
and allowed subject before creating a fresh session cookie.

Authentication data lives in the new `logto_sessions` table, independently of
legacy user/password/session records. Only SHA-256 hashes of random cookie
values are stored as IDs. Flow payloads, access/refresh/ID tokens and the display
profile are AES-GCM encrypted with `crypto.encryption_key`. Login flows expire
after ten minutes; authenticated sessions have an absolute `session_ttl`, seven
days by default. Expired rows are purged at startup and hourly.

Production cookies are host-only `__Host-neobox_session` and
`__Host-neobox_login`, with Path `/`, Secure, HttpOnly and SameSite=Lax.
Loopback HTTP development uses unprefixed cookies without Secure. Every sign-in
creates a fresh random authenticated cookie; pending cookies cannot access APIs.
Legacy bearer tokens and direct Logto bearer tokens do not authenticate requests.

`GET /api/auth/session` returns only display information and a per-session CSRF
token. The frontend sends credentialed Connect requests with `X-CSRF-Token`;
unsafe methods without the matching token are refused. Credentialed CORS permits
only the configured dashboard Origin. Snapshot downloads authenticate with the
same HttpOnly cookie. No Logto configuration or tokens are returned in frontend
JSON, and the browser no longer uses a Logto SDK or processes authorization codes.

The server refreshes tokens when their expiry is near. A PostgreSQL row lock
serializes refreshes for each session, including across application instances.
A revoked refresh grant deletes the session; a transient provider/storage failure
returns 503 and preserves it for retry. `POST /api/auth/logout` checks CSRF,
revokes the local session and clears its cookie without requiring a token refresh,
then redirects the browser directly to Logto's discovered end-session endpoint.
The OIDC redirect includes the protocol's ID-token hint; it is not exposed to
frontend JavaScript. Access and refresh tokens stay exclusively on the server.

Profiles and sign-in methods are managed in Logto. Neo Box's Profile page is
read-only. `AuthService/Me` still returns the authenticated owner; legacy local
login and user-management RPCs remain unimplemented. The frontend callback route
and the old public `/api/auth/config` endpoint have been removed.

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

References: [Logto Go integration](https://docs.logto.io/quick-starts/go),
[Logto core SDK convention](https://docs.logto.io/developers/sdk-conventions/core-sdk-convention).
