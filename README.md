# Ebook Requests for Silo

`silo.ebook-requests` is a request/download provider for the [`silo.ebooks`](https://github.com/RXWatcher/silo-plugin-ebooks) portal. It receives approved ebook requests, forwards them to an operator-managed Anna's-Archive-style downloader service, and reconciles job state until each request is fulfilled or failed.

This plugin is a connector, not a library backend. It does not expose shelves, OPDS, Kobo, or Kindle delivery. Use it only with content you are legally allowed to access.

(Previously distributed as `silo-plugin-annas-archive-downloader`.)

## Category

Lives under **Books / Ebooks** in the Silo catalog.

## Capabilities

| Type | ID | Purpose |
| --- | --- | --- |
| `http_routes.v1` | `backend` | Authenticated `/api/v1/*` API for search, request forwarding, status checks, and diagnostics, plus an admin UI under `/admin`. |
| `event_consumer.v1` | `request_handler` | Subscribes to `plugin.silo.ebooks.request_submitted` and forwards targeted requests to the upstream downloader. |
| `ebook_backend.v1` | `default` | Declares the installation as an ebook `download_provider` (`supports_requests=true`, `supports_catalog=false`, `supports_auto_monitoring=false`). |
| `scheduled_task.v1` | `reconciler` | Runs every minute (`*/1 * * * *`) to poll non-terminal downloads on the upstream service and publish status transitions. |

## Dependencies

- Consumed by [`silo.ebooks`](https://github.com/RXWatcher/silo-plugin-ebooks), which publishes `plugin.silo.ebooks.request_submitted` and consumes the acknowledgement / status / fulfillment / failure events emitted here.
- Acts as an alternate request/download provider to [`silo-plugin-bookwarehouse-ebook`](https://github.com/RXWatcher/silo-plugin-bookwarehouse-ebook). Only one provider handles a given request; the portal selects the target via `target_plugin_id` / `target_provider_plugin_id` / `provider_plugin_id` on the event payload, and this plugin ignores events targeted elsewhere.
- For local-filesystem libraries pair with [`silo-plugin-local-ebooks`](https://github.com/RXWatcher/silo-plugin-local-ebooks).
- Host: [`github.com/ContinuumApp/silo`](https://github.com/ContinuumApp/silo).
- SDK: [`github.com/ContinuumApp/continuum-plugin-sdk`](https://github.com/ContinuumApp/continuum-plugin-sdk).

## External services

- **Anna's-Archive-style downloader (EbookDB).** An operator-managed HTTP service that performs the actual searches and downloads. The plugin talks to it over `base_url` using `X-API-Key` authentication and a 30s default timeout. Body reads are capped at 10 MiB and `429 Too Many Requests` responses (with optional `Retry-After`) pause the reconciler globally for up to ten minutes.
- **Postgres.** A dedicated schema (typically `ebook_requests`) used to persist forwarded request rows and migrations. The pgx pool is sized to at least 16 connections to keep the search API and reconciler from starving each other.

## Request lifecycle

1. A user submits an ebook request in `silo.ebooks`.
2. The portal publishes `plugin.silo.ebooks.request_submitted` with the target provider's plugin ID and request metadata (`request_id`, `title`, `authors`, `isbn`, `format_pref`, ...).
3. This plugin filters by target plugin ID, persists a `submitted` row, then calls `AddMonitoring` on the upstream downloader (a 10s bounded call). The upstream needs at least a title or an ISBN; requests with neither are recorded as `failed` and the user is notified.
4. On success the row is upserted with the upstream `external_id` and status `acknowledged`, and `request_acknowledged` is published. On upstream error the row is marked `failed` and `request_failed` is published. Persistence failures nack the event so the host redelivers; a terminal-status guard in the store keeps redelivery idempotent.
5. The `reconciler` scheduled task polls non-terminal rows once per minute and translates upstream monitoring states (`monitored` / `searching` / `searching_now` → `searching`, `found` / `found_pending` → `found`, `grabbed` / `downloading` → `downloading`, `completed` / `imported` → `imported`, `failed` / `not_found` / `error` → `failed`). Transitions emit `request_status_changed`, terminal states emit `request_fulfilled` (with `fulfilled_book_id`) or `request_failed`.
6. `silo.ebooks` consumes those events and updates the user-facing request.

## Configuration

| Key | Required | Description |
| --- | --- | --- |
| `database_url` | yes | Postgres DSN for the plugin schema. Pool sized to ≥16 connections; override via `?pool_max_conns=N`. |
| `base_url` | yes | Upstream downloader base URL (http/https, no trailing slash). Validated on `Configure`. |
| `api_key` | yes | Sent to the upstream as `X-API-Key`. Stripped automatically on cross-host redirects. |
| `default_cover_size` | no | Cover size hint forwarded to upstream search. |
| `external_source_priority` | no | JSON array of preferred source/indexer names passed to upstream search. |

Example DSN:

```text
postgres://plugin_ebook_requests:password@postgres:5432/silo?search_path=ebook_requests&sslmode=disable
```

Database bootstrap:

```sql
CREATE ROLE plugin_ebook_requests WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA ebook_requests AUTHORIZATION plugin_ebook_requests;
GRANT CONNECT ON DATABASE silo TO plugin_ebook_requests;
```

Secrets (`database_url`, `api_key`) are redacted from logs by the `runtime.Config` `slog.LogValuer` / `fmt.Stringer` implementations.

## Event subscriptions

Subscribed:

- `plugin.silo.ebooks.request_submitted` — filtered by target plugin ID; events for other providers are acked and dropped.

Published (suffixes; the host namespaces them under this plugin's ID):

- `request_acknowledged` — upstream accepted the job; includes `external_id`.
- `request_status_changed` — non-terminal transition observed by the reconciler; includes `status`.
- `request_fulfilled` — upstream reached `completed` / `imported`; includes `fulfilled_book_id`.
- `request_failed` — validation, upstream, or terminal failure; includes `reason`.

## Detailed docs

- [Setup, debugging, and communication flows](docs/setup-debug-flows.md)

## Build and release

Local build and tests:

```bash
make build
make test
```

CI builds linux-amd64 binaries on push to main via the reusable workflow in [RXWatcher/silo-plugin-repository](https://github.com/RXWatcher/silo-plugin-repository) and publishes them to the catalog at [`./binaries/`](https://github.com/RXWatcher/silo-plugin-repository/tree/main/binaries).
