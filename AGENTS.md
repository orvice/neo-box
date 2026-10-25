# AGENTS.md

Guidance for coding agents working in this repository.

## Build & Run

```bash
# Backend (needs PostgreSQL on localhost:5432, see store.db in config.yaml)
cp .env.example .env && export $(grep -v '^#' .env | xargs)
go run ./cmd/neobox          # HTTP on :8080

# Frontend
cd front && npm install && npm run dev   # :5173, proxies /api and /ping to :8080

make build   # bin/neobox
make test    # go test ./...
make buf     # buf generate + protoc-go-inject-tag
make ent     # go generate ./internal/ent
make lint    # buf lint + buf format check + golangci-lint
```

After changing any `.proto`, run `make buf` and commit the generated Go
(`pkg/proto/`) and TypeScript (`front/src/gen/`) output. After changing an
ent schema (`internal/ent/schema/`), run `make ent` and commit
`internal/ent/`. Never hand-edit generated code.

Repository tests need `NEOBOX_TEST_POSTGRES_DSN` (e.g.
`postgres://neobox:neobox@localhost:5432/neobox?sslmode=disable`) and skip
without it; `internal/repo/internal/pgtest` gives each test its own schema.

## Architecture

Module: `go.orx.me/apps/neo-box`. Monorepo: Go backend at the root, React
dashboard in `front/`, protobuf contracts in `proto/`. The stack mirrors
`orvice/butter`: Butterfly (`butterfly.orx.me/core`) + Gin + ConnectRPC +
PostgreSQL via ent on the backend, Vite + React 19 + TanStack Router/Query +
shadcn/ui + Connect-Web on the frontend.

**Backend layers:**
- `cmd/neobox/main.go` — entry point. Builds routes, then hands Butterfly an
  `InitFunc` that runs `Handlers.Bootstrap` after YAML config is loaded.
- `internal/app/` — wiring. `routes.go` registers Connect handlers under
  `/api/<package>.<Service>/*`; `bootstrap.go` wraps the butterfly
  `store.db` connection named by `db_store` in an ent client, migrates the
  schema, adopts legacy business data when configured, then attaches repositories
  and starts background workers. Logto authenticates the single configured owner.
  Routes are registered before config/DB exist, so services receive repos
  through setters and the auth middleware reads the Logto authenticator from an
  `atomic.Pointer` published after bootstrap.
- `internal/config/` — `AppConfig`, the YAML startup config.
- `internal/application/` — ConnectRPC service implementations. Methods use
  native Connect signatures and satisfy `neoboxv1connect.XxxServiceHandler`.
  Build errors with `connect.NewError` or the `internal/transport/connectx`
  helpers.
- `internal/transport/connectx/` — error helpers and the snake_case JSON
  codec every handler installs via `connectx.HandlerOptions()`.
- `internal/handler/http/` — Gin handlers and middleware. `LogtoMiddleware`
  resolves HttpOnly session cookies and attaches the stable business owner to context.
  Public login/callback routes are under `/api/auth/`; session-cookie APIs
  enforce CSRF on writes and exact-Origin CORS. Unready auth returns 503.
- `internal/ent/` — ent schemas in `schema/`; everything else is generated.
- `internal/repo/` — repository interfaces with ent/PostgreSQL
  implementations in `postgres/` subpackages. Repositories return domain
  types; generated ent types do not leave these packages.
- `internal/connection/` — `Service` for Connections across Providers: seals
  secrets, has the Provider verify settings, records health, runs the
  Provider's `Cleanup` before delete. A Provider implements `Type`,
  `Verify`, `Cleanup` (NocoDB's lives in `internal/backup/provider.go`).
  Resources and scheduling stay per Provider (`docs/adr/0003`).
- `internal/auth/logto/` — server-side OIDC code flow with PKCE/state/nonce,
  ID-token verification, encrypted PostgreSQL sessions, serialized token refresh,
  and configured owner checks. For Logto setup or data adoption,
  read `docs/logto.md`. Legacy `internal/auth/provider/` and local auth service
  implementations remain unregistered.
- `internal/repo/ownership/` — startup consolidation of all business owners:
  copy and verify NocoDB attachments, then commit owner changes atomically.
  Original blobs and snapshot object keys are retained.
- `internal/secretbox/` — AES-GCM for stored credentials (key from
  `crypto.encryption_key`).
- `internal/blobstore/` — object storage: S3 (butterfly `store.s3`, selected
  by `storage.s3_store`) or a local directory fallback for development.
- `internal/nocodb/` — NocoDB REST client (v2 base list/create, v3
  meta/data reads and writes), `xc-token` auth, rate limiting, 429/5xx
  retry for reads and 429-only retry for writes.
- `internal/snapshot/` — builds and reads the Snapshot document (streaming
  gzip JSON; format in `format.go`, rationale in `docs/adr/0001` and
  `0005`), and `Restore` rebuilds one into a new Base (`restore*.go`,
  `docs/adr/0004`). Attachment files go through the `Files` interface
  (capture) and `RestoreOptions.OpenFile` (restore); views go through v2
  meta (`restore_views.go`).
  The live round-trip test needs `NEOBOX_TEST_NOCODB_URL`,
  `NEOBOX_TEST_NOCODB_TOKEN` and `NEOBOX_TEST_NOCODB_EMAIL` (the token
  owner's email); an OSS `nocodb/nocodb` container is enough.
- `internal/backup/` — `Manager`: worker queue running snapshots, in-process
  cron for Backup Policies, retention, and one-snapshot-per-Base guarding;
  a separate one-at-a-time queue running restores (`restore.go`). Clients
  of one connection share a rate limiter. `files.go` stores attachment
  files once per user by sha256 and deletes those no snapshot uses.
- `internal/wasabi/` — Wasabi Stats API client (`Authorization: AK:SK`,
  pages until a short page, accepts both the paged-object and bare-array
  response shapes), connection settings, and `EstimateCost` (Wasabi's
  published per-day formula).
- `internal/notify/` — `Service`: per-user notification channels (sealed
  secrets) and the alert log; `Notify` stores an alert once per dedupe key
  and a worker delivers it to every enabled channel. A `Sender` per channel
  type (`Telegram`, via the Bot API). Raisers depend on `notify.Notifier`:
  `connection.Service.RecordStatus` (health turned to error),
  `backup.Manager` (scheduled snapshot or restore failed), and
  `wasabisync.Manager` after each successful sync (`alerts.go`: budget,
  egress over storage, large deletes). The live test
  needs `NEOBOX_TEST_TELEGRAM_BOT_TOKEN` / `NEOBOX_TEST_TELEGRAM_CHAT_ID`.
- `internal/wasabi/configreader.go` — `ConfigReader`: bucket settings through
  the S3 API (aws-sdk-go-v2 per region, plus a SigV4-signed raw request for
  Wasabi's `?compliance`), per-setting errors (`access_denied`,
  `not_supported`); `BucketFindings` flags risky settings.
- `internal/cloudflare/` — Cloudflare API client (bearer token, one rate
  limiter per connection), connection settings, DNS record input and
  validation, and Tunnels (`tunnels.go`; `ingress.go` is the ingress model
  that keeps unknown config keys, and the hostname / path / service
  validation). Reads retry 429/5xx/network errors; writes retry only 429 and
  otherwise return `UncertainError`, never resent. `cftest/` is a stateful
  fake Cloudflare API (tokens with per-account permissions) for client and
  service tests.
- `internal/cloudflareacct/` — `Manager`: the "cloudflare" Provider
  (`Verify` accepts a token that reaches the Account through any one of
  account / zones / Workers / Pages reads; it never writes), live reads
  always checked against the connection's Account, DNS changes logged
  before they are sent (`dns.go`), and Worker / Pages addresses with
  per-source issues (`resources.go`), and cloudflared Tunnels
  (`tunnels.go`): list / create / delete / token / connection cleanup, and
  public hostnames edited by read-modify-write of the whole config under a
  per-Tunnel lock, with optional CNAME create/delete through the DNS path.
  Worker and Pages management (`worker_manage.go`, `pages_manage.go`):
  every change goes through `operate` (`operations.go`), which logs a
  pending `cloudflare_resource_operations` row first (no row, nothing sent),
  sends once, and finishes the row; no-op changes return `noChange()`
  unlogged; read-modify-write holds `lockResource` (`worker/<acc>/<name>`,
  `pages/<acc>/<project>`). Secret values never reach the log.
  A Zone's `permissions` describe the token owner, not the token, so they
  can only rule DNS edits out.
- `internal/cloudflare/email.go` — Email Routing client: Zone settings and the
  DNS it needs, rules, catch-all, and the Account's destination addresses
  (reads retry; writes never resent), plus the rule input checks
  (`ValidateEmailRuleInput`), `ReadOnlyReason` and `Same`. The fake lives in
  `cftest/email.go`.
- `internal/cloudflareacct/email.go` — Email Routing in the Manager: the live
  read (`EmailRouting`), enable (never disable), rule / catch-all / destination
  address changes. Every write goes through `operate` as an `email_routing`
  operation (`docs/adr/0011`); forwards need a verified address; rules sending
  to a Worker or owned by wrangler are refused; deleting an address checks the
  named Zone's rules and catch-all. Locks: `email/<acc>/<zone>` and
  `email/<acc>/addresses`.
- `internal/cloudflare/graphql.go` and `email_logs.go` — Email Routing logs
  through Cloudflare's GraphQL Analytics API (`POST /graphql`, dataset
  `emailRoutingAdaptive`). GraphQL answers in `{data, errors}` not the REST
  envelope, and reports a token without access as a 200 with an `authz`
  error, which `readGraphQL` turns into a 403 `APIError` so the usual
  classification applies; the query is a POST retried like a read.
  `EmailLogQuery.Validate` enforces the 31-day retention and the 500 limit.
  `Account.EmailLogs` (`cloudflareacct/email.go`) is a plain read: it needs
  Analytics Read, stores nothing and logs nothing (`docs/adr/0012`). The fake
  GraphQL lives in `cftest/graphql.go`.
- `internal/wasabisync/` — `Manager`: one sync at a time; 12-month backfill in
  30-day chunks that resumes after failures, daily run at 02:30 UTC from the
  last synced day, `Refresh` for the last 7 days, catch-up at startup. Also
  the "wasabi" connection Provider (`Activate` queues the first sync).

**Authentication** (`proto/neobox/v1/auth.proto`, `AuthService`): production
registers `LogtoAuthServiceServer`, which implements only `Me`; legacy auth and
user-management RPCs return Unimplemented. The configured Logto user ID (`sub`)
resolves to a stable business owner ID, without local user accounts. Server-side
OIDC sessions are stored separately in `logto_sessions`. All
existing business data can be adopted by that owner; old login data is unused.

**Connections** (`proto/neobox/v1/connection.proto`, `ConnectionService`):
list / get / create / update / delete / test for every Provider. Provider
settings are typed oneofs (`NocoDBConnectionSettings` in, `...Config` out);
secrets are write-only. Scoped to the calling user.

**NocoDB backups** (`proto/neobox/v1/nocodb.proto`, `NocoDBService`): live
Base listing joined with policy + latest snapshot, `UpsertBackupPolicy`,
async `CreateSnapshot` (poll `GetSnapshot`), `ListSnapshotRecords` for
browsing, and `GET /api/nocodb/snapshots/:id/download` for the raw
`.json.gz`. Async `RestoreSnapshot` (poll `GetRestore`; `ListRestores`)
rebuilds a succeeded snapshot into a new Base on any of the caller's NocoDB
connections; rows live in `nocodb_restores`. `connection_id` must be the
caller's NocoDB connection.

**Wasabi usage** (`proto/neobox/v1/wasabi.proto`, `WasabiService`):
overview (newest account totals, cost estimate, sync state), buckets with
their newest day (gone buckets flagged `deleted`), daily usage of the
account or a bucket (each bucket with its stored settings and findings),
the cost breakdown (`wasabi.CostBreakdown`: the
estimate's period by active / deleted / 1 TB minimum and by bucket), and
"sync now". Days are UTC `YYYY-MM-DD` strings.
`connection_id` must be the caller's Wasabi connection. The live Stats API
test needs `NEOBOX_TEST_WASABI_ACCESS_KEY` / `NEOBOX_TEST_WASABI_SECRET_KEY`.

**Cloudflare** (`proto/neobox/v1/cloudflare.proto`, `CloudflareService`):
Zones, DNS records, Workers, Pages projects and Tunnels read live (search
and paging cover the whole list), Tunnel create / delete / token /
connection cleanup and public hostname add / update / delete (with optional
DNS record; a DNS problem is reported beside the saved route, not an error),
DNS create / update (partial, keeps unshown fields) /
proxy switch / delete for A, AAAA, CNAME, TXT, MX, NS, SRV, CAA, and
`ListCloudflareDNSOperations` (rows in `cloudflare_dns_operations`).
Worker management (`GetCloudflareWorker`, workers.dev, custom domains,
routes, cron triggers, secrets, version rollback, delete) and Pages
management (project get / create / update / delete, deployments list /
retry / rollback / delete, domains, env vars) all return
`CloudflareChangeResult` (`changed`, `operation`, `operation_log_error`),
Email Routing (GetCloudflareEmailRouting, ListCloudflareEmailAddresses,
EnableCloudflareEmailRouting, rule create / update / delete,
SetCloudflareEmailCatchAll, destination address create / delete) also returns
CloudflareChangeResult; there is no disable,
and `ListCloudflareResourceOperations` lists their log. `ListCloudflareEmailLogs`
reads a Zone's Email Routing events (31 days, needs Analytics Read) and
changes nothing. No code upload.
`connection_id` must be the caller's Cloudflare connection. A missing
permission is PermissionDenied, a rejected token FailedPrecondition (never
Unauthenticated, which signs the user out), an unconfirmed write Unknown.

**Notifications** (`proto/neobox/v1/notification.proto`,
`NotificationService`): channel list / create / update / delete / test
(settings are a typed oneof, secrets write-only) and `ListAlerts`. Scoped
to the calling user. The dashboard page is Settings → Notifications.

**Frontend** (`front/`): `src/api/transport.ts` uses credentialed Connect requests
(binary protobuf) with the session's `X-CSRF-Token` and redirects to `/sign-in`
on Unauthenticated. `src/api/*.ts` wrap generated clients with React Query hooks.
`src/lib/logto.ts` navigates to backend login and submits CSRF-protected logout;
`GET /api/auth/session` supplies display info and CSRF. Logto tokens and callback
handling remain on the backend. Snapshot downloads use the same cookie.
`src/stores/auth-store.ts` holds verified sign-in state and display profile.
Authenticated routes await session restore. Profile is read-only. Combined
frontend/backend deployments use the `auth.logto.public_url` origin. Connections live under
`/connections` (`?provider=` filters); `src/features/connections/` holds the
Provider registry: `provider-info.ts` (key, label, icon; used by the sidebar)
and `providers.ts` (each Provider's form, card summary, and detail page from
its feature folder). Cloudflare's logic lives in `src/features/cloudflare/`
`addresses.ts`, `dns.ts`, `access.ts`, `tunnels.ts`, `email.ts`,
`email-logs.ts` and `manage.ts` (input checks, status and log wording), each
with a Vitest test; Email Routing UI is in `email-routing-tab.tsx`
(`email-rules.tsx`, `email-addresses.tsx`), and the zone page's Email logs tab
is `email-logs-tab.tsx`. `worker-page.tsx`
and `pages-project-page.tsx` are the management pages; they share
`manage-dialogs.tsx` (`FormDialog`, `ConfirmChangeDialog`).

## Conventions

- Proto package `neobox.v1`, Go package alias `neoboxv1`. `buf lint`
  STANDARD rules apply: each RPC gets its own request/response message.
- Name enums without an acronym run (`CloudflareRecordAccess`, not
  `CloudflareDNSAccess`): protobuf-es strips value prefixes only when the
  name converts back to the prefix buf requires.
- Storage has its own ent schema; don't persist proto messages directly or
  add storage tags to `.proto` files.
- Every new Connect service: implement in `internal/application`, register in
  `internal/app/routes.go`, add a typed client + hooks in `front/src/api/`.
- A new Provider: add its settings/config messages to the oneofs in
  `connection.proto` (and a case in `internal/application/connection_service.go`),
  implement `connection.Provider` and `Register` it in bootstrap, and add
  entries to `provider-info.ts` and `providers.ts` in the frontend.
- Frontend uses npm (`package-lock.json` is tracked); prettier config in
  `front/.prettierrc`.
- Charts use shadcn `chart` (recharts). Series colors come from the dataviz
  reference palette (categorical slot 1 blue, slot 2 orange), with separate
  light and dark steps validated on the card surfaces (`#ffffff`,
  `#020919`). Every chart has a table view, a crosshair tooltip, solid
  hairline grid, and 2px lines with ~10% fills. Byte axes use `byteTicks`
  for round base-2 ticks. See `front/src/features/wasabi/usage-charts.tsx`.

## Domain

See `CONTEXT.md` for the domain language and `docs/adr/` for decisions.

## Agent skills

### Issue tracker

Issues live in GitHub Issues (`orvice/neo-box`, via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical roles using default label strings (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
