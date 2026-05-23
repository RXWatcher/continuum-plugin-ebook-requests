# Admin API and UI

Everything under `/api/v1/*` is authenticated (the host gates this — the
plugin does not implement its own auth). The admin SPA at `/admin` is the
intended consumer; this doc covers the same surface so you can hit it
from `curl` or a script.

## Public + provider routes

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/health` | Liveness ping. Returns `{"ok": true}`. No upstream or DB check. |
| GET | `/api/v1/capabilities` | Static capability metadata (formats, features, `max_concurrent_downloads`, `supports_range_requests`). |
| GET | `/api/v1/catalog` | Catalog search / metadata, proxies to upstream. See `internal/catalog/handler.go`. |
| POST | `/api/v1/external_search` | External (Anna's-Archive-style) search, proxies to upstream. |

`/api/v1/catalog` and `/api/v1/external_search` are mounted only when the
plugin has an `EbookDBClient`, i.e. after `Configure` has provided
`base_url` + `api_key`. Before that the routes return 404, not 503 —
clients see "not configured" indirectly via `/api/v1/admin/diagnostics`.

## Admin routes

All `/api/v1/admin/*` routes assume an authenticated admin. They return
JSON.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/admin/diagnostics` | Readiness snapshot: `configured`, `base_url`, upstream Ping result, DB Pool.Ping result, `RequestStats`. Cap of 5s per call. |
| GET | `/api/v1/admin/config` | Returns the persisted app_config row with `api_key` blanked. Useful before a PATCH so you can confirm what's currently saved. |
| PATCH | `/api/v1/admin/config` | Updates app config. Empty `api_key` in the body is treated as "keep existing" — this is how the UI avoids round-tripping the secret. |
| GET | `/api/v1/admin/test-search?q=...` | Live test search via the upstream. Defaults `q` to `foundation`. Returns 5 hits. 8s ctx budget. |
| GET | `/api/v1/admin/requests?status=&q=&limit=&page=` | Paginated queue. `status` filters exact; `q` is ILIKE on `request_id`/`external_id`. `limit` clamps to 1–200, default 50. |
| GET | `/api/v1/admin/requests/stuck` | Non-terminal rows whose `last_polled` is older than 24h (or whose `created_at` is older than 24h with `last_polled IS NULL`). Limit 50. |
| POST | `/api/v1/admin/requests/{id}/retry` | Resets a row to `acknowledged`, clears `error_text` and `last_polled`. 404 if no row, or if the row has no `external_id` (nothing to poll). |
| POST | `/api/v1/admin/requests/{id}/mark-failed` | Body `{"reason": "..."}` (optional). Drives the row to terminal `failed` with `error_text=reason`. **Does not emit `request_failed`** — the portal will not learn about this. Use it only to clear hopeless rows from the queue. |
| GET | `/api/v1/admin/reconciler/status` | Last tick: `lastRunAt`, `lastDurationMs`, `rowsProcessed`, `skipped`, `lastError`, plus `stuckCount` (computed by `ListStuck` here, same threshold as `/stuck`). |
| POST | `/api/v1/admin/reconciler/run` | Run a tick immediately on a fresh background context (60s cap). Returns `{ok:true}` or `{ok:false, error: ...}`. Honours the 429 backoff window — if the reconciler is parked, you get `{ok:true}` with no work done. |

## Admin SPA (`/admin`)

A single self-contained HTML/CSS/JS page rendered by `handleAdminHome`.
Tabs:

- **Readiness** — calls `/admin/diagnostics`, `/admin/reconciler/status`,
  and (only when stuckCount > 0) `/admin/requests/stuck`. The
  reconciler **Run now** button posts `/admin/reconciler/run` and refreshes.
- **Config** — `GET`/`PATCH /admin/config`. Empty API key field means
  "leave existing".
- **Search lab** — `/admin/test-search?q=`. Dumps raw upstream hits.
- **Request queue** — `/admin/requests` with status/search/pagination.
  Each row exposes **Retry** and **Mark failed** buttons that hit the
  per-row endpoints.
- **Guardrails** — static documentation of the safety mechanisms (terminal
  guard, 429 backoff, redirect stripping). Read-only.

The SPA paths are relative (`./api/v1/...`) so it works under any host
mount prefix.

## Quick examples

Diagnostics from the host (assuming the plugin is mounted under
`/plugins/silo.ebook-requests/`):

```bash
curl -s -H "Cookie: $admin_session" \
  https://silo.example.com/plugins/silo.ebook-requests/api/v1/admin/diagnostics | jq
```

Force a reconciler run after an upstream outage:

```bash
curl -s -XPOST -H "Cookie: $admin_session" \
  https://silo.example.com/plugins/silo.ebook-requests/api/v1/admin/reconciler/run
```

List stuck rows and retry the first one:

```bash
ids=$(curl -s ... /api/v1/admin/requests/stuck | jq -r '.rows[].requestId')
for id in $ids; do
  curl -s -XPOST ... /api/v1/admin/requests/$id/retry
done
```

Mark a row failed with a reason:

```bash
curl -s -XPOST -H 'Content-Type: application/json' \
  -d '{"reason":"upstream lost the job"}' \
  ... /api/v1/admin/requests/$id/mark-failed
```
