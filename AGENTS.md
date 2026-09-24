# AGENTS.md

Guidance for coding agents working in this repository.

## Build & Run

```bash
# Backend (needs MongoDB on localhost:27017)
cp .env.example .env && export $(grep -v '^#' .env | xargs)
go run ./cmd/neobox          # HTTP on :8080

# Frontend
cd front && npm install && npm run dev   # :5173, proxies /api and /ping to :8080

make build   # bin/neobox
make test    # go test ./...
make buf     # buf generate + protoc-go-inject-tag
make lint    # buf lint + golangci-lint
```

After changing any `.proto`, run `make buf` and commit the generated Go
(`pkg/proto/`) and TypeScript (`front/src/gen/`) output. Never hand-edit
generated code.

## Architecture

Module: `go.orx.me/apps/neo-box`. Monorepo: Go backend at the root, React
dashboard in `front/`, protobuf contracts in `proto/`. The stack mirrors
`orvice/butter`: Butterfly (`butterfly.orx.me/core`) + Gin + ConnectRPC +
MongoDB on the backend, Vite + React 19 + TanStack Router/Query + shadcn/ui +
Connect-Web on the frontend.

**Backend layers:**
- `cmd/neobox/main.go` — entry point. Builds routes, then hands Butterfly an
  `InitFunc` that runs `Handlers.Bootstrap` after YAML config is loaded.
- `internal/app/` — wiring. `routes.go` registers Connect handlers under
  `/api/<package>.<Service>/*`; `bootstrap.go` connects MongoDB, ensures
  indexes, seeds the initial admin, and attaches repositories to services.
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
- `internal/repo/` — repository interfaces with MongoDB implementations in
  `mongo/` subpackages.
- `internal/auth/provider/` — OAuth login providers (GitHub, Google) behind a
  `Provider` interface and `Registry`.

**User center** (`proto/neobox/v1/auth.proto`, `AuthService`): password
login, OAuth login (`BeginOAuthFlow` → provider → `CompleteOAuthFlow`, CSRF
state single-use in `oauth_states`), `Me`, `Logout`, self-service
`UpdateProfile` / `ChangePassword`, and admin-only `ListUsers` /
`CreateUser` / `UpdateUserPassword` / `SetUserDisabled`. Roles are `admin`
and `user`. Sessions live in the `auth_sessions` collection with a TTL index.

**Frontend** (`front/`): `src/api/transport.ts` is the Connect transport
(binary protobuf, Bearer interceptor, redirect to `/sign-in` on
Unauthenticated). `src/api/*.ts` wrap generated clients with React Query
hooks. `src/stores/auth-store.ts` (Zustand) holds token + user. Routes under
`src/routes/_authenticated/` require a token.

## Conventions

- Proto package `neobox.v1`, Go package alias `neoboxv1`. `buf lint`
  STANDARD rules apply: each RPC gets its own request/response message.
- Add `// @gotags: bson:"..."` comments on proto fields that are stored
  directly; `make buf` injects them.
- Every new Connect service: implement in `internal/application`, register in
  `internal/app/routes.go`, add a typed client + hooks in `front/src/api/`.
- Frontend uses npm (`package-lock.json` is tracked); prettier config in
  `front/.prettierrc`.

## Domain

See `CONTEXT.md` for the domain language.
