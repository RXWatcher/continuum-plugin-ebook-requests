# Anna's Archive Downloader Plugin

`continuum.annas-archive-downloader` is a Continuum ebook download provider. It
connects the Ebooks portal to an upstream Anna's Archive style downloader
service and handles one-shot acquisition requests. It is intentionally not a
presentation library source: it does not expose shelves, catalog pages, or owned
library browsing.

## What It Does

- Receives ebook request events from `continuum.ebooks`.
- Forwards each request to an upstream downloader API.
- Tracks non-terminal download jobs with a scheduled reconciler.
- Publishes request status events back to Continuum.
- Exposes an authenticated backend HTTP API for search, request forwarding, and
  status checks.

## Capabilities

| Capability | ID | Purpose |
|---|---|---|
| `http_routes.v1` | `backend` | Authenticated `/api/v1/*` downloader API. |
| `event_consumer.v1` | `request_handler` | Subscribes to `plugin.continuum.ebooks.request_submitted`. |
| `ebook_backend.v1` | `default` | Advertises a download-provider role to the Ebooks portal. |
| `scheduled_task.v1` | `reconciler` | Polls upstream download status every minute. |

The `ebook_backend.v1` metadata advertises:

- `ebook_roles`: `download_provider`
- `supports_catalog`: `false`
- `supports_requests`: `true`
- `supports_auto_monitoring`: `false`

## Event Flow

1. A user submits an ebook request in the Ebooks portal.
2. `continuum.ebooks` emits `plugin.continuum.ebooks.request_submitted`.
3. This plugin ignores requests targeted at other providers.
4. Matching requests are sent to the configured downloader service.
5. The request is acknowledged, failed, or reconciled until fulfilled.

Published event suffixes:

- `request_acknowledged`
- `request_status_changed`
- `request_fulfilled`
- `request_failed`

The Continuum host prefixes these with the plugin ID, for example
`plugin.continuum.annas-archive-downloader.request_fulfilled`.

## Configuration

| Key | Required | Description |
|---|---|---|
| `database_url` | yes | Postgres DSN for the dedicated downloader schema. |
| `base_url` | yes | Base URL for the upstream downloader service. |
| `api_key` | yes | API key sent to the upstream service as `X-API-Key`. |
| `default_cover_size` | no | Cover size requested from upstream where supported. |
| `external_source_priority` | no | JSON array of source/indexer names passed to external search. |

Example `database_url`:

```text
postgres://plugin_annas_archive_downloader:password@postgres:5432/continuum?search_path=annas_archive_downloader&sslmode=disable
```

## Database Setup

Create a dedicated role and schema before installing the plugin:

```sql
CREATE ROLE plugin_annas_archive_downloader WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA annas_archive_downloader AUTHORIZATION plugin_annas_archive_downloader;
GRANT CONNECT ON DATABASE continuum TO plugin_annas_archive_downloader;
```

The plugin runs its embedded migrations against the configured schema.

## Upstream Requirements

The upstream downloader service must provide compatible endpoints for:

- external search
- request submission
- job status lookup
- file/cover retrieval where supported

The plugin assumes the upstream API is reachable from the Continuum plugin
process and protected by the configured API key.

## Build And Test

```bash
go test ./...
go build -buildvcs=false -o continuum-plugin-annas-archive-downloader ./cmd/continuum-plugin-annas-archive-downloader
```

Store tests require a reachable Postgres database matching the configured test
DSN behavior.

## Operational Notes

- Use this provider for direct download workflows, not for library browsing.
- Requests without an exact upstream source ID may need manual review upstream.
- The reconciler is best-effort and should be safe to run repeatedly.
- Keep API keys secret; they are plugin configuration secrets, not user-facing
  values.

## Repository Status

This is a first-party Continuum plugin owned by the Continuum project.
