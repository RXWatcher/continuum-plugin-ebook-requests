# Debugging runbook

Symptom-driven. Look for the section that matches what the operator is
seeing and jump to the resolution.

## Setup checklist (read this first)

Before debugging anything in here, confirm the basics from the README are
in place:

1. `database_url`, `base_url`, `api_key` all set.
2. The plugin database role exists with the correct schema authorization.
3. In `silo.ebooks` admin settings, this installation is selected as
   the request/download provider. **This is the most common reason a
   request never reaches the plugin** — see [coexistence.md](coexistence.md).
4. `/api/v1/admin/diagnostics` shows `configured: true`, `database.ok:
   true`, `upstream.ok: true`.

If any of those is false, fix that first.

## "I submit a request, nothing happens"

Symptom: the portal shows the request as submitted but no row appears in
`forwarded_request` for that `request_id`.

Causes (most to least common):

1. **Wrong target plugin ID.** The consumer filters
   `request_submitted` events by `target_plugin_id` /
   `target_provider_plugin_id` / `provider_plugin_id`. If the portal is
   targeting a different installation (e.g. you reinstalled the plugin
   and the portal still points at the old installation ID), the event is
   acked and dropped silently. **Check the Ebooks admin settings.**
2. **Plugin not configured.** Capability servers serve before `Configure`
   runs. In that state the consumer nacks. After `Configure` succeeds the
   host redelivers. If `Configure` never succeeds (DB unreachable, bad
   DSN) the event sits unacked. Check plugin logs at startup for the
   manifest load / migrate / pgxpool sequence.
3. **Event not actually published.** Check `silo.ebooks` logs for
   `plugin.silo.ebooks.request_submitted`.

## "Row is stuck on `submitted`"

A row in `submitted` means the consumer inserted the row but didn't get
as far as `AddMonitoring`. This should be a sub-second window. If you see
it for longer:

- The consumer goroutine crashed between the insert and the upstream call
  (very rare). Check plugin logs for panics around the timestamp.
- The plugin was killed mid-handler before the host could nack. The
  inserted row is now orphaned. **Manual cleanup:** delete the row and
  ask the portal to resubmit, **or** mark it `failed` with admin Mark
  failed.

## "Row is stuck on `acknowledged`"

`acknowledged` means the upstream took the job; the reconciler should be
polling it once a minute.

Walk the reconciler:

1. `/api/v1/admin/reconciler/status` — is `lastRunAt` within the last
   minute or two? If not, the scheduled task isn't running. Check the
   host scheduler.
2. Is `lastError` set?
   - `backoff: upstream rate-limited, …` — see [upstream-ebookdb.md](upstream-ebookdb.md). Wait it out.
   - `list non-terminal: ...` — DB is gone. Fix Postgres.
   - per-row upstream error — the row's specific call is failing; look at
     `error_text` on that row.
3. Is `rowsProcessed` zero? If the only non-terminal rows are
   `submitted` (no `external_id`), the reconciler skips them — that's a
   different bug from the consumer side.
4. Hit **Run now** in the admin UI (or `POST /api/v1/admin/reconciler/run`).
   If it fixes the row, the cron was missing fires; if it doesn't, the
   row's `external_id` is wrong upstream.

## "Status flaps between two values"

Symptom: a row alternates between `searching` and `acknowledged`, or
between two other states, on every tick. Burst of
`request_status_changed` events.

Cause: upstream is reporting a status the plugin doesn't know about.
Earlier code mapped unknown statuses to `acknowledged`, which caused
exactly this. Current code returns `""` from `translateStatus` and holds
the existing status, so flapping should no longer happen.

If you still see it, the upstream is returning **different** known
statuses on consecutive polls (e.g. flicking between `grabbed` and
`searching` because of an upstream race). Inspect upstream logs; the
plugin is faithfully reporting what it sees.

## "Reconciler says rate-limited but the upstream looks fine"

The reconciler parks for at least 60s on any 429, and up to 10 minutes if
the upstream sent `Retry-After`. The window is process-local and not
persisted, so a plugin restart clears it.

If the upstream is fine but the reconciler thinks it's rate-limited, the
backoff is probably from a transient burst that has since cleared. Wait,
or restart the plugin, or **Run now** after the window expires.

## "error_text on a row never clears"

`MarkPolled` clears `error_text` (`SET error_text = NULL`). So if it
stays stuck:

- The row is still failing to poll — `error_text` reflects the *latest*
  failure. Check the message itself.
- Or the reconciler is parked in backoff and the row isn't being polled
  at all.
- Or the row is terminal (`imported` / `failed`). Terminal rows are
  excluded from `ListNonTerminal`, so they don't poll, so their
  `error_text` is whatever was last written to them. This is expected
  for `failed`.

## "Two providers, requests going to the wrong one"

See [coexistence.md](coexistence.md). The portal decides which provider
handles the request; this plugin only acts when the target plugin ID
matches its own.

## "Logs are full of the same upstream error"

The reconciler dedupes per-row error_text — if a row keeps producing the
same `error_text` on consecutive ticks the UPDATE is skipped and nothing
is logged from the upsert path. Repetitive error spam is therefore most
likely coming from a different code path:

- The HTTP routes (`/admin/test-search`, catalog routes) — separate
  per-request errors, no dedupe.
- The consumer — fires once per redelivered event.

Find the source by checking which log line is repeating. If it's
`mark polled (after upstream err)`, that's the deduped path and it should
*not* be spammy; something is wrong with the dedupe key (most likely the
error string varies slightly per call, e.g. embeds a timestamp).

## "How do I see what's actually happening on a tick?"

The reconciler doesn't log per-row by default. To trace one tick:

1. Hit `POST /api/v1/admin/reconciler/run`.
2. Inspect `/api/v1/admin/reconciler/status` for `rowsProcessed`,
   `lastDurationMs`, `lastError`.
3. Inspect the `forwarded_request` table directly — `last_polled` should
   have moved on every non-terminal row, `error_text` should have either
   updated or cleared.

For deeper tracing, the per-row work is in
`internal/reconciler/reconciler.go:Tick`. The plugin uses `hclog`; the
host decides log level.

## Verification after a change

1. Restart or reload the plugin installation.
2. Open `/admin` and confirm Readiness is green.
3. Submit a low-risk test request (an ISBN that the upstream definitely
   has).
4. Watch the row in the Request queue tab; it should move
   `submitted` → `acknowledged` within a second, then through
   `searching`/`downloading` over the next minutes, then to `imported`.
5. Confirm `silo.ebooks` shows the corresponding book.
