# ADR 0003: Generic Connections; resources and scheduling stay per Provider

Status: accepted (2026-09-25)

## Context

NocoDB was the only Provider, and its connections were NocoDB-specific:
their own table (`nocodb_connections`), five RPCs on `NocoDBService`, and
their own page. Wasabi (#5) is about to become the second Provider, and more
will follow. We had to decide how much of a Provider integration becomes
shared infrastructure.

We considered three levels of abstraction:

1. **Connection only.** Every Provider shares owner, name, encrypted
   credentials, health, verification, and deletion.
2. **Plus a generic job framework.** One scheduler with schedule and run
   tables, which NocoDB's snapshot queue would move onto.
3. **Plus a generic resource and metric model.** A `resources` table with
   JSONB attributes, time-series metrics, and generic UI components.

## Decision

- **The Connection is generic.**
  - One `connections` table with `provider`, `name`, `config` (JSONB,
    non-secret), `secret_ciphertext`, `status`, `status_message` and
    `status_checked_at`.
  - `internal/connection.Service` seals and opens secrets, asks the Provider
    to verify settings, records health, and runs the Provider's `Cleanup`
    before deleting a connection. Deletion is hard.
  - `ConnectionService` is the API. Provider settings are typed `oneof`
    messages, and secrets are write-only.
  - A Provider implements three methods: `Type`, `Verify` and `Cleanup`.
  - Adding a Provider means adding a oneof branch and a provider
    implementation.
- **Resources, metrics and scheduling stay with each Provider.**
  - NocoDB keeps its own tables for Backup Policies and Snapshots, and its
    own `backup.Manager` for scheduling.
  - Wasabi will add its own usage table and its own daily sync.
  - The only generic thing a Provider reports back is the health of the
    connection.
- **Health**
  - Health is set on create, update and "Test".
  - A Provider's background work may also set it. NocoDB sets it after
    snapshot runs: ok on success, error on 401/403. Other failures do not
    change it.
- **UI**
  - The dashboard is connection-centric: one `/connections` page, with
    Provider sub-items in the sidebar as filters.
  - Provider pages hang off `/connections/$connectionId`.
  - The frontend keeps a small Provider registry. `ProviderInfo` holds what
    the shell needs; `ProviderViews` holds the form, the card summary and the
    detail page.

## Why not the generic job framework or resource model

What Providers own differs too much. NocoDB has Bases, one-at-a-time
snapshot queues, retention, and blobs. Wasabi has buckets and daily usage
numbers. Future Providers may have VMs, domains or subscriptions.

A shared resource table would end up as untyped JSONB, with the real logic
still living in each Provider. Each new Provider would then have to fit a
model shaped by the earlier ones.

A shared scheduler would have to cover both NocoDB's per-Base cron with
dedupe and retention, and Wasabi's single daily pull. That buys little, and
it means rewriting a working, tested manager.

Credentials, ownership and health, by contrast, really are the same for
every Provider. We will revisit this once a third Provider shows what else is
genuinely shared.

## Consequences

- Breaking API change: `NocoDBService`'s connection RPCs and `NocoDBConnection`
  are gone. There was no data to migrate.
- Every NocoDB RPC that takes a `connection_id` checks that the connection is
  the caller's and that its provider is `nocodb`. Otherwise it answers
  NotFound.
- A cross-Provider overview (for example on the dashboard) will have to be
  built from each Provider's own data. There is no shared table to query.
