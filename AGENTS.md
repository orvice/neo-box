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
make lint    # buf lint + golangci-lint
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
  schema, seeds the initial admin, attaches repositories to services, and
  starts an hourly purge of expired sessions and OAuth states.
  Routes are registered before config/DB exist, so services receive repos
  through setters and the auth middleware reads the repo from an
  `atomic.Value`.
- `internal/config/` — `AppConfig`, the YAML startup config.
- `internal/application/` — ConnectRPC service implementations. Methods use
  native Connect signatures and satisfy `neoboxv1connect.XxxServiceHandler`.
  Build errors with `connect.NewError` or the `internal/transport/connectx`
  helpers.
- `internal/transport/connectx/` — error helpers and the snake_case JSON
  codec every handler installs via `connectx.HandlerOptions()`.
- `internal/handler/http/` — Gin handlers and middleware. `AuthMiddleware`
  resolves `Authorization: Bearer <token>` to a session (sha256 of the token
  is stored, never the token) and puts user + session on the context. Public
  paths are listed in `isPublicPath`.
- `internal/ent/` — ent schemas in `schema/`; everything else is generated.
- `internal/repo/` — repository interfaces with ent/PostgreSQL
  implementations in `postgres/` subpackages. Repositories return domain
  types; generated ent types do not leave these packages.
- `internal/connection/` — `Service` for Connections across Providers: seals
  secrets, has the Provider verify settings, records health, runs the
  Provider's `Cleanup` before delete. A Provider implements `Type`,
  `Verify`, `Cleanup` (NocoDB's lives in `internal/backup/provider.go`).
  Resources and scheduling stay per Provider (`docs/adr/0003`).
- `internal/auth/provider/` — OAuth login providers (GitHub, Google) behind a
  `Provider` interface and `Registry`. Unrelated to connection Providers.
- `internal/secretbox/` — AES-GCM for stored credentials (key from
  `crypto.encryption_key`).
- `internal/blobstore/` — object storage: S3 (butterfly `store.s3`, selected
  by `storage.s3_store`) or a local directory fallback for development.
- `internal/nocodb/` — NocoDB REST client (v2 base list, v3 meta/data),
  `xc-token` auth, rate limiting, 429/5xx retry.
- `internal/snapshot/` — builds and reads the Snapshot document (streaming
  gzip JSON; format in `format.go`, rationale in `docs/adr/0001`).
- `internal/backup/` — `Manager`: worker queue running snapshots, in-process
  cron for Backup Policies, retention, and one-snapshot-per-Base guarding.
- `internal/wasabi/` — Wasabi Stats API client (`Authorization: AK:SK`,
  pages until a short page, accepts both the paged-object and bare-array
  response shapes), connection settings, and `EstimateCost` (Wasabi's
  published per-day formula).
- `internal/wasabisync/` — `Manager`: one sync at a time; 12-month backfill in
  30-day chunks that resumes after failures, daily run at 02:30 UTC from the
  last synced day, `Refresh` for the last 7 days, catch-up at startup. Also
  the "wasabi" connection Provider (`Activate` queues the first sync).

**User center** (`proto/neobox/v1/auth.proto`, `AuthService`): password
login, OAuth login (`BeginOAuthFlow` → provider → `CompleteOAuthFlow`, CSRF
state single-use in `oauth_states`), `Me`, `Logout`, self-service
`UpdateProfile` / `ChangePassword`, and admin-only `ListUsers` /
`CreateUser` / `UpdateUserPassword` / `SetUserDisabled`. Roles are `admin`
and `user`. Sessions live in the `auth_sessions` table; expired rows are
ignored by lookups and purged hourly.

**Connections** (`proto/neobox/v1/connection.proto`, `ConnectionService`):
list / get / create / update / delete / test for every Provider. Provider
settings are typed oneofs (`NocoDBConnectionSettings` in, `...Config` out);
secrets are write-only. Scoped to the calling user.

**NocoDB backups** (`proto/neobox/v1/nocodb.proto`, `NocoDBService`): live
Base listing joined with policy + latest snapshot, `UpsertBackupPolicy`,
async `CreateSnapshot` (poll `GetSnapshot`), `ListSnapshotRecords` for
browsing, and `GET /api/nocodb/snapshots/:id/download` for the raw
`.json.gz`. `connection_id` must be the caller's NocoDB connection.

**Wasabi usage** (`proto/neobox/v1/wasabi.proto`, `WasabiService`):
overview (newest account totals, cost estimate, sync state), buckets with
their newest day (gone buckets flagged `deleted`), daily usage of the
account or a bucket, and "sync now". Days are UTC `YYYY-MM-DD` strings.
`connection_id` must be the caller's Wasabi connection. The live Stats API
test needs `NEOBOX_TEST_WASABI_ACCESS_KEY` / `NEOBOX_TEST_WASABI_SECRET_KEY`.

**Frontend** (`front/`): `src/api/transport.ts` is the Connect transport
(binary protobuf, Bearer interceptor, redirect to `/sign-in` on
Unauthenticated). `src/api/*.ts` wrap generated clients with React Query
hooks. `src/stores/auth-store.ts` (Zustand) holds token + user. Routes under
`src/routes/_authenticated/` require a token. Connections live under
`/connections` (`?provider=` filters); `src/features/connections/` holds the
Provider registry: `provider-info.ts` (key, label, icon; used by the sidebar)
and `providers.ts` (each Provider's form, card summary, and detail page from
its feature folder).

## Conventions

- Proto package `neobox.v1`, Go package alias `neoboxv1`. `buf lint`
  STANDARD rules apply: each RPC gets its own request/response message.
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

## Domain

See `CONTEXT.md` for the domain language and `docs/adr/` for decisions.

## Agent skills

### Issue tracker

Issues live in GitHub Issues (`orvice/neo-box`, via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical roles using default label strings (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
