# ADR 0014: System-level Telegram Bot for read-only Connection viewing

Status: accepted (2026-11-27)

## Context

Users want to inspect their Neo Box Connections and check health status from
Telegram without opening the web dashboard. Neo Box already has a per-user
outbound Telegram notification channel (`internal/notify/telegram.go`), which
delivers one-way alerts to private chats, groups, or channels using each user's
own bot token.

We had to decide:

1. **Bot ownership**: reuse per-user notification bots or run one system-level
   bot.
2. **Update delivery**: long polling (`getUpdates`) or webhook (`setWebhook`)
   for multi-pod deployments.
3. **Scope**: read-only Connection viewing vs mutations (rename, delete,
   credential entry) and alert integration.

## Decision

- **One system-level bot, linked per user via one-time deep links.**
  - Configured once in `config.yaml` (`telegram.bot_token`). Users link their
    own Telegram account from **Settings → Telegram Bot**, which issues a
    10-minute, single-use token (only its sha256 digest is stored in
    `telegram_link_tokens`) and a deep link
    `https://t.me/<bot>?start=<token>`.
  - Only `private` chats are accepted; group and channel messages are ignored
    so Connection metadata never leaks to other group members.
  - Each `telegram_user_id` binds to at most one Neo Box user (`telegram_bindings`),
    and every incoming command or callback re-checks that the linked Neo Box
    user is not disabled.
  - Existing per-user `NotificationChannel` bots remain independent; alert
    delivery is not coupled to this bot yet.

- **Webhook by default, completely stateless across pods.**
  - `telegram.mode` defaults to `"webhook"` and requires an explicit shared
    `telegram.webhook_url` and `telegram.webhook_secret` so every pod verifies
    `X-Telegram-Bot-Api-Secret-Token` identically in constant time.
  - `POST /api/telegram/webhook` is listed in `isPublicPath` (no Bearer
    session) and dispatches validated updates to a bounded background worker
    pool so Telegram always receives a fast `200 OK`.
  - All inline keyboard callbacks are 100% stateless and encoded within
    Telegram's 64-byte `callback_data` limit (`cl:<p>:<page>`,
    `cv:<uuid>:<p>:<page>`, `cs`, `ub:y`/`ub:n`). Any pod can handle any press
    with zero shared memory or Redis.
  - Optional `telegram.mode: "polling"` is kept for local development without
    a public HTTPS endpoint.

- **Read-only Connection views only; secrets are never touched.**
  - Commands: `/connections` (`/list`) with provider filter and pagination,
    inline Connection detail cards showing health status and non-secret
    provider config, `/status` health overview, `/unbind`, and `/help`.
  - The bot never calls `connection.Service.Open` on secrets, never decrypts
    `SecretCiphertext`, and never accepts credentials over Telegram chat
    (where messages are stored in Telegram's cloud history).

## Consequences

- Multi-pod deployments work out of the box once `telegram.webhook_url` and
  `telegram.webhook_secret` are set in shared config; PostgreSQL holds both
  bindings and single-use link tokens.
- Adding a new Provider only requires adding its short filter code and
  non-secret summary formatter in `internal/tgbot/`.
- Future mutations (such as `TestConnection` or Provider quick actions) or
  outbound alert buttons can layer onto `internal/tgbot` and `internal/telegram`
  without changing the binding schema or multi-pod webhook contract.
