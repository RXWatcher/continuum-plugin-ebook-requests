# Upstream EbookDB

The plugin talks to a single operator-managed HTTP service for all search,
metadata, cover, file streaming, and the AddMonitoring / GetMonitoring
calls that drive request fulfilment. This service is referred to in code
as **EbookDB**; in catalog text it is called the **Anna's-Archive-style
downloader**. They are the same thing.

## Connection

| Setting | Source | Notes |
| --- | --- | --- |
| `base_url` | plugin config | Validated on `Configure`: must parse as `http`/`https`, must have a host, no trailing slash is enforced by the client (`strings.TrimRight`). |
| `api_key` | plugin config | Sent as `X-API-Key: <key>` on every request. Redacted in logs (`runtime.Config` implements `slog.LogValuer` and `fmt.Stringer`). |
| HTTP timeout | hardcoded | 30s default per request (see `internal/ebookdb/client.go:defaultTimeout`). Bound separately by the caller's context — the consumer uses 10s, the reconciler uses 10s per row. |

`base_url` is validated, but reachability is not checked on `Configure` —
the plugin will accept a config that points at an unreachable host. The
admin **Readiness** tab calls `Ping` (which tries `/api/v1/health` then
`/health`) so you can see the reachability check on demand.

## Per-host header stripping on redirects

`X-API-Key` is a custom header. Go's stdlib only strips
`Authorization`/`Cookie`/`WWW-Authenticate` on a cross-host redirect, so by
default the client would happily forward the API key to wherever a redirect
points. The upstream is a content proxy whose download URLs frequently
redirect to third-party hosts, so the client installs a `CheckRedirect`:

- Caps the redirect chain at 10.
- Deletes `X-API-Key` on any redirect where `req.URL.Host != via[0].URL.Host`.

This matters mostly for `GetStream` / `GetStreamWithRange` (cover and file
proxying), but it applies to every call.

## 10 MiB response cap

`maxResponseBytes = 10 << 20`. Every JSON response (`Get`, `PostJSON`) is
read through `io.LimitReader`. This is a memory-exhaustion guard, not a
real limit on upstream payload — search and detail responses are well
under it. If the upstream ever does return more than 10 MiB of JSON the
read is silently truncated and JSON decoding will fail with a position
near the cap; treat that as a bug in the upstream.

For binary streams (`GetStream*`) there is no cap because the body is not
buffered — it's piped through to the caller.

Error bodies inlined into Go error strings are further truncated at 512
bytes (`errBodySnippet`) so log lines and admin error displays stay
readable.

## 429 + Retry-After behaviour

When upstream returns `429 Too Many Requests`:

1. The client wraps the response in `*ebookdb.RateLimitError` with the
   parsed `Retry-After` (either a delta-seconds integer or an HTTP date —
   negative values clamp to 0).
2. The reconciler checks for it with `ebookdb.IsRateLimited(err)` after
   every per-row call. On hit:
   - Calls `setBackoff(retryAfter)`. Default if no header is **60 seconds**.
     Cap is **10 minutes**.
   - Records the rate-limit error_text on the offending row (subject to
     dedupe).
   - **Breaks out of the row loop** — does not process the rest of the
     batch. Continuing would just generate more 429s.
3. The next tick checks `backoffRemaining()` before doing anything. If the
   window has not elapsed, the tick returns immediately with
   `Status{Skipped: true, LastError: "backoff: upstream rate-limited, …"}`.

The cap exists so a misbehaving upstream (e.g. returns 429 with
`Retry-After: 86400`) cannot pin the reconciler for a day.

The consumer (event handler) does *not* honour the backoff window. If a
`request_submitted` arrives during a backoff, the consumer still tries
`AddMonitoring` and will likely get another 429 — which is then returned
to the user as a `request_failed` event. This is a deliberate tradeoff:
the consumer's job is fast (one upstream call per event) and there's no
ongoing work to throttle.

## What the plugin actually calls

These are the upstream endpoints the plugin depends on. Path shapes are
mirrored from the TypeScript reference client (`lib/ebookdb/client.ts`).

| Plugin op | Method | Path | Used by |
| --- | --- | --- | --- |
| Health | GET | `/api/v1/health` (falls back to `/health`) | Admin diagnostics. |
| AddMonitoring | POST | (see `internal/ebookdb/monitoring.go`) | Consumer, on `request_submitted`. |
| GetMonitoring | GET | (see `internal/ebookdb/monitoring.go`) | Reconciler, per non-terminal row. |
| ExternalSearch | POST | (see `internal/ebookdb/external_search.go`) | Admin test-search, portal search. |
| Catalog / cover / file proxy | GET (stream) | (see `internal/ebookdb/catalog.go` + `internal/catalog/handler.go`) | Portal catalog and download routes. |

Check the corresponding `*.go` files for the exact request/response shape —
they're the source of truth and they're short.

## Common upstream failure patterns

- **`upstream 401`** in error_text: `api_key` is wrong, has whitespace
  pasted into it, or was rotated upstream. Re-enter under the **Config**
  tab; the admin form intentionally never echoes the existing key (sent as
  `""` to "keep current").
- **`upstream 404`** during `GetMonitoring`: the upstream forgot a job
  (DB reset, schema migration). The row is stuck — admin **Mark failed**
  is the right cleanup.
- **`upstream 5xx`** persistently: upstream is down. Reconciler keeps
  retrying once a minute, dedupes the error_text so the DB stays quiet.
  Nothing to do here besides fix the upstream.
- **`dial tcp ... i/o timeout`** in error_text: `base_url` is reachable
  from your browser but not from the plugin process. Check Docker network
  / firewall; never test reachability from the operator laptop.
- **`upstream 429`** then nothing for ~1 minute: working as designed. See
  above.
- **TLS errors** (`x509: certificate signed by unknown authority`):
  upstream is using a private CA. The plugin uses the system trust store
  of the container it runs in. Either install the CA into the container
  image or terminate TLS at a reverse proxy in front of the upstream.

## Tuning ideas (not currently exposed)

- HTTP timeout is hardcoded at 30s. Per-call deadlines (10s consumer, 10s
  per reconciler row, 45s per tick) are the practical bounds. If you find
  yourself wanting to tune these, edit
  `internal/ebookdb/client.go:defaultTimeout` and
  `internal/reconciler/reconciler.go:tickTimeout,perRowTimeout`.
- The 200-row reconciler batch size is hardcoded in
  `reconciler.Tick: ListNonTerminal(ctx, 200)`. Increase only if you have
  a backlog and headroom — 200 rows × 1s upstream latency already runs
  most of the way through the 45s tick budget.
