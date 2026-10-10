# neo-box

neo-box is a personal hub for managing and viewing resources held in
third-party services. It is built on the [Butterfly](https://butterfly.orx.me)
framework: a Go backend exposing ConnectRPC APIs, a React dashboard, and
protobuf contracts generated with buf, all in one monorepo.

## What It Does

- **User center**: password and OAuth (GitHub, Google) sign-in, sessions,
  self-service profile and password, and admin user management.
- **Connections**: link accounts at third-party services (Providers) and
  manage them in one place. Credentials are verified before saving, stored
  encrypted, and never returned; each connection shows whether it currently
  works.
- **NocoDB Base snapshots**: open-source NocoDB has no Base backup, so neo-box
  captures a Base's schema, records, links, views, webhooks and (by default)
  attachment files into point-in-time snapshots. You can take them manually
  or on a cron schedule with retention, browse them in the dashboard,
  download them as gzip JSON, and restore one into a new Base on any of your
  NocoDB connections. Webhooks are restored turned off: enable them in
  NocoDB when ready; non-URL notifications need the target's integrations.
  Original record IDs and view shares are not restored; see
  [ADR 0005](docs/adr/0005-nocodb-snapshot-format-v2.md).
- **Wasabi usage and cost** (read-only): daily storage, deleted storage
  still billed under the 90-day minimum, egress, and API calls for a Wasabi
  account and each of its buckets, synced from the Wasabi Stats API, plus an
  estimated charge for the current billing cycle. See [Wasabi](#wasabi).
- **Cloudflare Zones, DNS, Workers and Pages**: the Zones, Workers and Pages
  projects an API token can see in one Cloudflare account, read live, with
  every address of each Worker and project; DNS records can be added,
  changed, deleted and proxied, and every change made from Neo Box is
  logged. See [Cloudflare](#cloudflare).
- Stores users, sessions, and metadata in PostgreSQL, and snapshot content in
  S3-compatible object storage.

## Getting Started

### Prerequisites

- Go 1.26.5+
- Node.js 22+ and npm (dashboard)
- PostgreSQL
- S3-compatible object storage for snapshot content (optional for local
  development; a local directory is used instead)
- [buf CLI](https://buf.build/) and `protoc-go-inject-tag` when regenerating
  protobuf code

### Configure

neo-box reads a single YAML file. Point Butterfly at it with environment
variables:

```bash
cp .env.example .env   # BUTTERFLY_CONFIG_TYPE=file, BUTTERFLY_CONFIG_FILE_PATH=./config.yaml
```

The repository's [config.yaml](config.yaml) works for local development. For a
deployment, start from the full sample in [Configuration](#configuration)
below.

### Run

```bash
# PostgreSQL (matches store.db.main in config.yaml)
docker run -d --name neobox-pg -p 5432:5432 \
  -e POSTGRES_USER=neobox -e POSTGRES_PASSWORD=neobox -e POSTGRES_DB=neobox \
  postgres:17-alpine

# Backend: migrates the schema and creates the initial admin
# (admin / change-me) on first start
export $(grep -v '^#' .env | xargs)
go run ./cmd/neobox

# Dashboard
cd front && npm install && npm run dev
```

Verify the backend is up:

```bash
curl http://127.0.0.1:8080/ping
# {"message":"pong"}
```

Open http://localhost:5173 and sign in. API requests (everything under
`/api/`) require `Authorization: Bearer <session token>`.

## Configuration

A complete sample with every supported key. Credentials, hosts, and bucket
names are placeholders; comments note the default where a key has one. The
file is plain YAML: `${VAR}` placeholders are **not** expanded, so render
secrets into the file (e.g. from a Kubernetes Secret) rather than referencing
environment variables.

```yaml
# ── Logging (Butterfly core) ────────────────────────────────────────────
log:
  level: info          # debug | info | warn | error
  format: text         # text | json (json recommended in production)
  add_source: false    # include source file:line in each entry

# ── Authentication ─────────────────────────────────────────────────────
auth:
  # Created on first start when the users table is empty. Change the
  # password right after the first sign-in.
  initial_admin_username: "admin"
  initial_admin_password: "change-me"   # required on first start
  session_ttl: 168h                     # bearer session lifetime (7 days)
  # Local development only: when true, requests are treated as admin before
  # the auth store is wired. Never enable in production.
  allow_unauthenticated: false
  # OAuth sign-in. A provider appears on the sign-in page only when both
  # client_id and client_secret are set. redirect_url must be the dashboard's
  # /auth/oauth/callback/<provider> route and must match the OAuth app.
  oauth_providers:
    github:
      client_id: "Iv1.xxxxxxxxxxxxxxxx"
      client_secret: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
      redirect_url: "https://neobox.example.com/auth/oauth/callback/github"
      scopes: ["read:user", "user:email"]   # default when omitted
      display_name: "GitHub"                # default when omitted
    google:
      client_id: "xxxxxxxx.apps.googleusercontent.com"
      client_secret: "GOCSPX-xxxxxxxxxxxxxxxx"
      redirect_url: "https://neobox.example.com/auth/oauth/callback/google"
      scopes: ["openid", "email", "profile"]  # default when omitted
      display_name: "Google"

# ── PostgreSQL ─────────────────────────────────────────────────────────
# Users, sessions, OAuth state, connections, NocoDB policies, and snapshot
# metadata. Name of a connection under store.db below; it must use driver
# postgres. Tables are created and migrated automatically on startup.
db_store: "main"                # default main

# ── Credential encryption ──────────────────────────────────────────────
# AES key protecting stored connection credentials (e.g. NocoDB API tokens).
# 16/24/32 bytes, raw, hex, or base64, e.g. `openssl rand -hex 32`.
# Required to create connections. Changing it makes stored credentials
# unreadable, so keep it stable and back it up.
crypto:
  encryption_key: "<64 hex chars>"

# ── Snapshot content storage ───────────────────────────────────────────
storage:
  # Name of a client under store.s3 below. When empty, content is written
  # to local_dir instead (development only).
  s3_store: "snapshots"
  key_prefix: "neobox"          # objects: <prefix>/nocodb/<user>/<conn>/<base>/<id>.json.gz
  local_dir: "./data/blobs"     # used only when s3_store is empty

store:
  # SQL connections (Butterfly core). Each key registers a named connection.
  db:
    main:
      driver: postgres
      host: "postgres"
      port: 5432
      user: "neobox"
      # Inserted into the connection URL unescaped: avoid @ : / ? # % in it.
      password: "..."
      db_name: "neobox"
      ssl_mode: "require"       # default disable
  # S3 clients (Butterfly core). Each key registers a named client; the
  # bucket should be private.
  s3:
    snapshots:
      # Host, or a full URL. Without a scheme, use_ssl picks https/http.
      # Omit endpoint for AWS S3.
      endpoint: "s3.us-east-1.amazonaws.com"
      access_key_id: "AKIA..."      # or: ak
      secret_access_key: "..."      # or: sk
      session_token: ""             # optional (temporary credentials)
      region: "us-east-1"           # default us-east-1
      bucket: "neobox-snapshots"
      use_ssl: true
      use_path_style: false         # true for MinIO and most self-hosted S3
    # MinIO example:
    # minio:
    #   endpoint: "minio:9000"
    #   ak: "minioadmin"
    #   sk: "minioadmin"
    #   bucket: "neobox"
    #   use_ssl: false
    #   use_path_style: true
    # Cloudflare R2 example:
    # r2:
    #   endpoint: "https://<account-id>.r2.cloudflarestorage.com"
    #   ak: "..."
    #   sk: "..."
    #   region: "auto"
    #   bucket: "neobox"
    #   use_path_style: true

# ── Optional tuning ────────────────────────────────────────────────────
# Server-wide knobs with working defaults; leave them out unless needed.
# Everything about a connection (URLs, keys, a NocoDB instance's request
# rate, Wasabi pricing) is set per connection in the dashboard.
# nocodb:
#   workers: 1              # snapshots running concurrently
#   page_size: 200          # records per page when reading tables
#   snapshot_timeout: 2h    # a run exceeding this is marked failed
#   restore_timeout: 6h     # a restore exceeding this is marked failed
# wasabi:
#   stats_endpoint: "https://stats.wasabisys.com"
#   s3_endpoint: ""            # development only: one host for all S3 calls
# cloudflare:
#   api_endpoint: "https://api.cloudflare.com/client/v4"
# notify:
#   dashboard_url: "https://neobox.example.com"  # alerts link here; empty = no links
#   telegram_endpoint: "https://api.telegram.org"
```

Cron schedules are evaluated in the server's time zone. The container image is
UTC, so prefix expressions with a zone when needed, e.g.
`CRON_TZ=Asia/Shanghai 0 3 * * *`.

### Notifications

Neo Box raises an alert when a scheduled snapshot or a restore fails, when
a connection stops working (for example a revoked token or a failing Wasabi
sync), and when a Wasabi account goes over its budget or egress or deletes
look costly. Each problem is reported once. Alerts are listed under
**Settings → Notifications** and sent to your Telegram channels:

1. Message `@BotFather` in Telegram with `/newbot` and copy the token.
2. Send your bot a message (or add it to a group and mention it).
3. Open `https://api.telegram.org/bot<token>/getUpdates` and copy `chat.id`
   (group IDs are negative).
4. Add the channel under **Settings → Notifications** and press **Test**.

Set `notify.dashboard_url` so messages link back to the dashboard.

### Wasabi

A Wasabi connection reads the account's daily utilization from the
[Stats API](https://docs.wasabi.com/apidocs/wasabi-stats-api). The Stats API
receives the access key and secret key as-is in the `Authorization` header,
so use a dedicated, read-only sub-user rather than root keys:

1. In the Wasabi Console, open **Users** and create a user with
   programmatic (API) access only.
2. Attach the `WasabiAccountStatsAccess` policy. It covers both account and
   bucket figures; `WasabiBucketStatsAccess` is not needed.
3. Create an access key for that user and add it in Neo Box under
   **Connections → Add connection → Wasabi**. The key is checked with a
   Stats call before it is saved.

After a connection is added, Neo Box backfills the last 12 months in the
background, then syncs once a day at 02:30 UTC (Wasabi publishes the
previous day around 01:30 UTC). "Sync now" re-fetches the last 7 days. A
failed sync is picked up by the next daily run. The Stats API's bucket
endpoint is slow (9–25 s per page of 100 records in testing), so the
backfill of an account with many buckets can take a while.

### Bucket settings

After every sync Neo Box also reads each bucket's settings through the S3
API with the same key: versioning, Object Lock, Wasabi compliance,
lifecycle rules, bucket policy and ACL, access logging, replication, tags,
region and creation date. They are shown on each bucket's page and flagged
when risky (a public bucket, versioning without a rule that expires old
versions). Reading them needs a read-only policy on the key's sub-user in
addition to `WasabiAccountStatsAccess`; it grants no access to objects:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:ListAllMyBuckets",
        "s3:GetBucketLocation",
        "s3:GetBucketVersioning",
        "s3:GetBucketObjectLockConfiguration",
        "s3:GetBucketCompliance",
        "s3:GetLifecycleConfiguration",
        "s3:GetBucketPolicy",
        "s3:GetBucketAcl",
        "s3:GetBucketLogging",
        "s3:GetReplicationConfiguration",
        "s3:GetBucketTagging"
      ],
      "Resource": "arn:aws:s3:::*"
    }
  ]
}
```

Without it, usage and cost still work; the connection page says bucket
settings can't be read, and each setting the key can't read shows "Needs
permission". Event notifications, CORS and encryption are not readable
through Wasabi's API, and neither is the console's "Public Access
Override", so "public" covers the bucket policy and ACL only.

The cost estimate follows Wasabi's published formula: per day, active
storage (at least 1 TB) plus deleted storage, at the connection's price per
TB-month (default Pay-Go $7.99) / 30. With a billing-cycle anchor it covers
the current 30-day cycle; without one, the last 30 days. It is an estimate,
not your invoice (Wasabi exposes no invoice API for standalone accounts),
and can be turned off per connection, e.g. for Reserved Capacity plans.
Egress is not priced; the page warns when it exceeds the stored volume.

With a budget set on the connection, the cost tile shows how much of it is
used, and Neo Box raises an alert when the estimate is on track to go over
it and again when it does. The tile also projects the period, and the next
cycle, on the trend of the last 30 days. After every sync Neo Box also
alerts when the period's egress exceeds the stored volume, and when a day's
deletes exceed a quarter of the storage (and 10 GB): objects deleted before
they are 90 days old stay billed until then. See
[Notifications](#notifications).

"Where the cost goes" splits the same period's charge into active storage,
deleted data still billed (objects deleted before they are 90 days old stay
billed until they would have reached 90 days), and the 1 TB minimum, then by
bucket. Each bucket and the account also show every figure the Stats API
reported for the newest day.

### Cloudflare

A Cloudflare connection is one account (its Account ID) plus an API token.
Find the Account ID in the dashboard's account home or its URL
([how](https://developers.cloudflare.com/fundamentals/account/find-account-and-zone-ids/)).
Create a custom token under **My Profile → API Tokens** (or an
account-owned token under **Manage Account → API Tokens**)
([how](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/)),
include the account in its resources, and grant only what you want to use:

| Permission | Unlocks |
|---|---|
| Zone → Zone → Read | Zones tab |
| Zone → DNS → Read | a Zone's DNS records |
| Zone → DNS → Edit | adding, changing, deleting and proxying records |
| Account → Workers Scripts → Read | Workers tab, workers.dev URLs, custom domains |
| Zone → Workers Routes → Read | Worker routes |
| Account → Cloudflare Pages → Read | Pages tab |

Saving checks, without changing anything, that Cloudflare accepts the token
and that the token reaches the account through at least one of these. A tab
whose permission is missing says so; the others keep working. Neo Box does
not need (and cannot read) the token's permission list, so it cannot tell
in advance whether DNS edits are allowed unless Cloudflare reports the
Zone as read-only; Cloudflare refuses changes the token may not make and
Neo Box shows why.

Everything is read from Cloudflare when a page opens and on **Refresh**;
nothing is synced or stored except the log of DNS changes made from Neo Box
(who, when in UTC, what was sent, the record before and after, and the
result). A Zone becomes read-only on the page once Cloudflare refuses a
change for lack of permission; **Refresh** after fixing the token. A change
whose answer never arrives (a timeout) is logged as "result unknown" and is
never sent again: check the record before retrying. Neo Box changes A, AAAA,
CNAME, TXT, MX, NS, SRV and CAA records and keeps whatever the form doesn't
show (comment, tags, settings); other types are listed read-only. Deleting
the connection deletes its log in Neo Box and nothing in Cloudflare.

### Environment variables

| Variable | Purpose |
|---|---|
| `BUTTERFLY_CONFIG_TYPE` | `file` to load config from a YAML file |
| `BUTTERFLY_CONFIG_FILE_PATH` | Path to the YAML config |
| `BUTTERFLY_NAMESPACE` | Namespace of the Consul config key `<namespace>/neobox` (default `apps`) |
| `BUTTERFLY_TRACING_PROVIDER` | `grpc` (default) or `http` OTLP exporter |
| `BUTTERFLY_TRACING_ENDPOINT` | OTLP collector, e.g. `otel-collector:4317` |
| `BUTTERFLY_TRACING_DISABLE` | `true` to turn tracing off |
| `PORT` | HTTP port (default `8080`) |
| `GIN_MODE` | Set to `release` in production |

Prometheus metrics are served on `:2223/metrics`.

## Deployment

CI publishes two images on every push to `main` (`:main`) and on `vX.Y.Z` tags
(`:X.Y.Z`, `:latest`):

| Image | Serves | Port |
|---|---|---|
| `ghcr.io/orvice/neo-box` | backend API | `8080` (+ `2223` metrics) |
| `ghcr.io/orvice/neo-box-front` | dashboard static files (nginx) | `80` |

Route `/api` and `/ping` to the backend and everything else to the dashboard,
for example with an Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: neobox
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "300"
spec:
  ingressClassName: nginx
  rules:
    - host: neobox.example.com
      http:
        paths:
          - { path: /api,  pathType: Prefix, backend: { service: { name: neobox,       port: { number: 8080 } } } }
          - { path: /ping, pathType: Exact,  backend: { service: { name: neobox,       port: { number: 8080 } } } }
          - { path: /,     pathType: Prefix, backend: { service: { name: neobox-front, port: { number: 80 } } } }
```

Backend notes:

- Run **one replica with `strategy: Recreate`**. Snapshot schedules and the
  job queue run in-process; overlapping pods would double-fire schedules, and
  a starting pod marks unfinished snapshots and restores failed (a failed
  restore leaves its partial Base in NocoDB).
- The database user needs rights to create and alter tables: the schema is
  migrated on every startup. Migration never drops tables, columns, or
  indexes.
- Mount the config file from a Secret and set `BUTTERFLY_CONFIG_TYPE=file`
  and `BUTTERFLY_CONFIG_FILE_PATH`.
- Use `GET /ping` for readiness and liveness probes. The dashboard image
  answers `GET /healthz`.
- Snapshots are staged in `/tmp` before upload. With a read-only root
  filesystem, mount an `emptyDir` there.

## Development

### Frontend

The dashboard lives in [front](front/). Keep `VITE_API_BASE_URL` empty and let
the Vite dev server proxy `/api` and `/ping` to the backend:

```bash
cd front
cp .env.example .env.local   # VITE_DEV_PROXY_TARGET=http://localhost:8080
npm install
npm run dev
```

Pointing `VITE_DEV_PROXY_TARGET` at a deployed backend is a handy way to debug
production with React Query devtools.

### Backend

```bash
go test ./...
make build   # bin/neobox
make buf     # buf generate + protoc-go-inject-tag
make ent     # regenerate internal/ent from internal/ent/schema
make lint    # buf lint + buf format check + golangci-lint
```

Commit regenerated code (`pkg/proto/`, `front/src/gen/`, `internal/ent/`) with
proto or schema changes.

Repository tests run against a real PostgreSQL, each in its own throwaway
schema, and are skipped unless `NEOBOX_TEST_POSTGRES_DSN` is set:

```bash
NEOBOX_TEST_POSTGRES_DSN='postgres://neobox:neobox@localhost:5432/neobox?sslmode=disable' go test ./...
```

The NocoDB restore round trip (`internal/snapshot`) builds a fixture Base,
snapshots it, restores it into a new Base and compares the two. It runs
against a real NocoDB, a local `nocodb/nocodb` container is enough, and is
skipped unless these are set:

```bash
NEOBOX_TEST_NOCODB_URL=http://localhost:8080 \
NEOBOX_TEST_NOCODB_TOKEN=<api token> \
NEOBOX_TEST_NOCODB_EMAIL=<the token owner's email> \
go test ./internal/snapshot -run TestLiveRestoreRoundTrip -v
```

## Layout

```
cmd/neobox/            entry point
internal/app/          route registration + bootstrap wiring
internal/application/  ConnectRPC service implementations
internal/connection/   connections across providers (secrets, verify, health)
internal/backup/       NocoDB snapshot and restore queues, cron scheduling, retention
internal/nocodb/       NocoDB REST client
internal/notify/       notification channels (Telegram), alert log and delivery
internal/snapshot/     snapshot document format, restore into a new Base
internal/wasabi/       Wasabi Stats API client, settings, cost estimate
internal/wasabisync/   Wasabi usage sync: backfill, daily run, provider
internal/ent/          ent schema (schema/) + generated client (do not edit)
internal/repo/         repositories (PostgreSQL via ent)
proto/neobox/v1/       protobuf API
pkg/proto/             generated Go (do not edit)
front/                 React dashboard (front/src/gen is generated)
```

## Documentation

- [AGENTS.md](AGENTS.md): architecture and conventions
- [CONTEXT.md](CONTEXT.md): domain language
- [docs/adr/](docs/adr/): architecture decision records
