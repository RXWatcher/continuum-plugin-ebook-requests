# continuum-plugin-ebookdb

Continuum plugin: thin adapter exposing an EbookDB (Anna's-Archive-style
external-fetch) instance to the `continuum.ebooks` portal via the
`ebook_backend.v1` capability.

See `/opt/worktrees/continuum-rh/docs/superpowers/specs/2026-05-11-ebooks-portal-and-backends-design.md`.

## Build & test

```bash
go build ./cmd/continuum-plugin-ebookdb
go test ./...   # requires Postgres for store tests
```

## Operator runbook

### Postgres pre-flight

```sql
CREATE ROLE plugin_ebookdb WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA ebookdb AUTHORIZATION plugin_ebookdb;
GRANT CONNECT ON DATABASE continuum TO plugin_ebookdb;
```

### Configuration (admin UI)

| Key | Required | Notes |
|-----|----------|-------|
| `database_url` | yes | `postgres://plugin_ebookdb:<pwd>@host/continuum?search_path=ebookdb` |
| `base_url` | yes | EbookDB instance base URL. |
| `api_key` | yes | X-API-Key for the EbookDB instance. |

### Capabilities exposed

* `http_routes.v1` — `ebook_backend.v1` REST surface (no `requests/{external_id}` snapshot endpoint: EbookDB has no per-request monitoring concept)
* `event_publisher.v1` — emits `request_acknowledged`, `request_status_changed`, `request_fulfilled`, `request_failed`
* `event_consumer.v1` — subscribes to `plugin.continuum.ebooks.request_submitted`
* `scheduled_task.v1` — `download_reconciler` (1m) polls upstream for non-terminal requests

### Capability differences vs bookwarehouse-ebook

* No `auto_monitor` flag — EbookDB does not maintain a long-lived monitoring
  queue; each request is a one-shot external fetch.
* `formats` typically includes 9 formats (the broader Anna's-Archive set):
  epub, pdf, mobi, azw3, fb2, lit, lrf, pdb, prc.
* `features` is `[external_search]` only.
