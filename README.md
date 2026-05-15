# continuum-plugin-annas-archive-downloader

Thin adapter exposing an Anna's Archive download service to the [`continuum.ebooks`](../continuum-plugin-ebooks/) portal via the `ebook_backend.v1` capability. Each ebook request is a **one-shot fetch** — no long-lived monitoring queue.

## Capabilities

| Capability | Notes |
|---|---|
| `ebook_backend.v1` (`default`) | External-fetch ebook source. |
| `http_routes.v1` (`backend`) | The `ebook_backend.v1` REST surface for Anna's Archive download requests. |
| `event_consumer.v1` (`request_handler`) | Subscribes to `plugin.continuum.ebooks.request_submitted`; forwards each request to the Anna's Archive downloader. |
| `scheduled_task.v1` (`reconciler`) | Cron `*/1 * * * *`. Polls upstream for status changes on non-terminal downloads. |

Emits to the bus: `request_acknowledged`, `request_status_changed`, `request_fulfilled`, `request_failed`.

## Configuration

| Key | Required | Description |
|---|---|---|
| `database_url` | yes | DSN for the `ebookdb` schema. |
| `base_url` | yes | Anna's Archive downloader base URL. |
| `api_key` | yes | `X-API-Key` for upstream calls. |

## Dependencies

- Postgres role + `ebookdb` schema.
- An external Anna's Archive downloader service.

## Install

```sql
CREATE ROLE plugin_ebookdb WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA ebookdb AUTHORIZATION plugin_ebookdb;
GRANT CONNECT ON DATABASE continuum TO plugin_ebookdb;
```

## Build & test

```bash
go build ./cmd/continuum-plugin-annas-archive-downloader
go test ./...    # requires Postgres for store tests
```

## Status

v0.1.0. Functional.
