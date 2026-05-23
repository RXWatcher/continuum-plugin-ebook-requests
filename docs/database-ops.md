# Database operations

The plugin owns one Postgres schema (typically `ebook_requests`) with two
tables:

- `forwarded_request` — one row per portal `request_id`. The main state.
- `app_config` — single-row JSON blob for per-installation config (the
  admin **Config** tab writes here; `Configure` reads `database_url` from
  the host and the rest from this table).

Plus `schema_migrations` for the embedded golang-migrate runner.

## Pool sizing

The pgx pool's `MaxConns` is bumped to at least **16** at startup
(`cmd/silo-plugin-ebook-requests/main.go`). The pgx default scales
with `GOMAXPROCS` and can be as low as 4, which starves the search API
against the reconciler. 16 is generous without saturating a shared
Postgres.

Override via the DSN: `?pool_max_conns=32` etc. Anything ≥16 is honoured;
the floor is only applied if you set something smaller (or leave it at
the pgx default).

If you run dozens of plugin installations against the same Postgres,
total connection count adds up — Postgres' default `max_connections=100`
is easy to hit. Either raise `max_connections` or use PgBouncer.

## Terminal guard semantics

Both `UpsertForwardedRequest` and `MarkPolled` contain the same `CASE`
guard:

```sql
status = CASE
           WHEN forwarded_request.status IN ('imported','failed')
           THEN forwarded_request.status
           ELSE EXCLUDED.status  -- (or $3 for MarkPolled)
         END
```

This guarantees:

- A re-delivered `request_submitted` after the request was already
  fulfilled does not regress the status. The redelivered event still
  upserts `(external_id, last_polled, error_text)`, but `status`
  stays `imported` / `failed`.
- A reconciler tick that somehow sees a fresh row mid-flight (rare race
  during an admin **Mark failed**) does not undo the manual terminal
  state.
- Admin **Retry** explicitly sidesteps the guard by writing
  `status = 'acknowledged'` directly via `RetryRequest` (a plain
  UPDATE that does **not** include the guard). This is intentional —
  retry is the one operator-driven path that resurrects a terminal row.

If you ever need to reset a row by hand (e.g. for testing), use the
`RetryRequest` SQL pattern: a direct UPDATE with no CASE expression.

## Useful SQL snippets

Quick queue overview:

```sql
SELECT status, COUNT(*) FROM forwarded_request GROUP BY status ORDER BY 2 DESC;
```

Find rows that have errored recently:

```sql
SELECT request_id, status, error_text, last_polled
FROM forwarded_request
WHERE error_text IS NOT NULL AND error_text <> ''
ORDER BY updated_at DESC LIMIT 50;
```

Rows the reconciler is currently considering (matches
`ListNonTerminal(200)` exactly):

```sql
SELECT request_id, external_id, status, last_polled
FROM forwarded_request
WHERE status NOT IN ('imported','failed')
ORDER BY COALESCE(last_polled, '0001-01-01'::timestamptz) ASC, request_id ASC
LIMIT 200;
```

Stuck rows (matches `ListStuck(24h, ...)`):

```sql
SELECT request_id, status, last_polled, created_at
FROM forwarded_request
WHERE status NOT IN ('imported','failed')
  AND (
    (last_polled IS NOT NULL AND last_polled < now() - interval '24 hours')
    OR (last_polled IS NULL AND created_at < now() - interval '24 hours')
  );
```

Resurrect a terminal row by hand (use sparingly; the admin **Retry**
endpoint only works on non-terminal rows that have an `external_id`):

```sql
UPDATE forwarded_request
SET status = 'acknowledged', error_text = NULL, last_polled = NULL, updated_at = now()
WHERE request_id = '<id>';
```

Reset the entire queue for a clean test run (destructive):

```sql
TRUNCATE forwarded_request;
```

This loses every in-flight request. The upstream still has its jobs;
those become orphans the reconciler will never poll. Use only in dev.

## Migrations

The runner lives in `internal/migrate`. Migrations are embedded into the
binary from `internal/migrate/files/`. They run at every successful
`Configure`. Failures abort `Configure` and the pool is closed.

To add a migration:

1. Add `0003_<name>.up.sql` and `0003_<name>.down.sql` under
   `internal/migrate/files/`.
2. Rebuild — the `//go:embed files/*.sql` directive picks them up.

Current files (as of writing):

- `0001_init.up.sql` / `.down.sql` — creates `forwarded_request`.
- `0002_app_config.up.sql` / `.down.sql` — creates `app_config` and
  imports legacy from the in-process config.

The first time `Configure` runs against an existing install,
`ImportLegacyAppConfig` copies the host-provided config values into
`app_config` so subsequent admin edits go through the DB rather than the
host config.

## Operational guidelines

- Use a dedicated role and schema. The `database_url` should set
  `search_path` to the plugin schema.
- Don't grant the role write access to `public` or to other plugin
  schemas. The plugin only needs its own schema.
- Back up `forwarded_request` along with the rest of the Silo DB;
  it's the source of truth for the request queue. The upstream's
  monitoring jobs are not a substitute — they don't carry the portal's
  `request_id`.
