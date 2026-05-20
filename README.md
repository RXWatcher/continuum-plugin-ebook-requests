# Ebook Requests for Continuum

`continuum.ebook-requests` is an ebook request/download provider for
the Continuum Ebooks portal. It connects Continuum to an operator-managed
Anna's Archive style downloader service, forwards approved ebook requests, and
tracks those jobs until they are fulfilled or failed.

This plugin is not an ebook library backend. It does not expose shelves,
catalog browsing, file streaming, OPDS, Kobo, or Kindle delivery. Install it
beside `continuum.ebooks` when you want the Ebooks request flow to use a
separate downloader service.

Use this plugin only with content you are legally allowed to access. The plugin
is a connector to your configured downloader; it does not provide or host
content itself.

## Detailed Operations Docs

- [Setup, debugging, and communication flows](docs/setup-debug-flows.md)

## Features

- Listens for `plugin.continuum.ebooks.request_submitted` events.
- Ignores requests targeted at other download providers.
- Searches the upstream downloader for external candidates.
- Forwards selected requests to the downloader API with `X-API-Key`
  authentication.
- Stores forwarded request metadata and reconciles non-terminal jobs.
- Publishes request acknowledgement, status, fulfillment, and failure events
  back to Continuum.
- Exposes authenticated `/api/v1/*` endpoints for search, request forwarding,
  status checks, and diagnostics.

## Configuration

| Key | Required | Description |
|---|---|---|
| `database_url` | yes | Postgres DSN for the `ebook_requests` schema. |
| `base_url` | yes | Upstream downloader service base URL, no trailing slash. |
| `api_key` | yes | API key sent to the upstream service as `X-API-Key`. |
| `default_cover_size` | no | Cover size requested from upstream when supported. |
| `external_source_priority` | no | JSON array of preferred source/indexer names passed to upstream search. |

Example DSN:

```text
postgres://plugin_ebook_requests:password@postgres:5432/continuum?search_path=ebook_requests&sslmode=disable
```

## Database Setup

```sql
CREATE ROLE plugin_ebook_requests WITH LOGIN PASSWORD '<chosen>';
CREATE SCHEMA ebook_requests AUTHORIZATION plugin_ebook_requests;
GRANT CONNECT ON DATABASE continuum TO plugin_ebook_requests;
```

## Event Flow

1. A user submits an ebook request in `continuum.ebooks`.
2. The Ebooks portal emits `request_submitted` for the selected download
   provider.
3. This plugin searches or forwards the request to the configured downloader.
4. The downloader job is acknowledged, polled, and eventually marked fulfilled
   or failed.

Outbound event suffixes:

- `request_acknowledged`
- `request_status_changed`
- `request_fulfilled`
- `request_failed`

## Build And Test

```bash
go test ./...
go build -buildvcs=false -o continuum-plugin-ebook-requests ./cmd/continuum-plugin-ebook-requests
```
