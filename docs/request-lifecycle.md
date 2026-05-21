# Request lifecycle

This is the end-to-end path a single ebook request takes, with the state
each component leaves behind so you can diagnose where a row is stuck.

The README has the short version. Read this one if a request looks wedged
and you need to know what to expect to see in the DB, in logs, and in the
event stream.

## Stages

```
  continuum.ebooks                    this plugin                       upstream EbookDB
  ----------------                    -----------                       ----------------
  user submits request
        │
        ▼
  publishes
  plugin.continuum.ebooks
  .request_submitted ──────► consumer.HandleEvent
                                  │
                                  ├─ target plugin ID ≠ ours? ack + drop
                                  ├─ missing request_id?     ack + drop
                                  ├─ insert row status='submitted'
                                  ├─ missing title+isbn?     row='failed' + publish request_failed + ack
                                  ├─ POST AddMonitoring (10s budget) ───►  monitored book created
                                  │                              ◄─────── { id, status }
                                  ├─ row='acknowledged', external_id set
                                  └─ publish request_acknowledged

                                scheduled_task (reconciler.Tick) every 1m
                                  │
                                  ├─ ListNonTerminal(limit=200)  ── select rows not in
                                  │                                   ('imported','failed')
                                  ├─ for each row with external_id:
                                  │      GetMonitoring (10s budget) ───► { status, book_id, ... }
                                  │      translateStatus → new portal status
                                  │      if changed: MarkPolled + publish
                                  │           imported  → request_fulfilled  (terminal)
                                  │           failed    → request_failed     (terminal)
                                  │           other     → request_status_changed
                                  └─ if 429 anywhere: setBackoff(Retry-After) and stop tick

  consumes request_*  ◄─────── publisher
```

## State transitions visible in `forwarded_request`

The plugin owns one row per `request_id`. The lifecycle of `status` is:

| Status | Set by | Meaning | Terminal? |
| --- | --- | --- | --- |
| `submitted` | consumer, before calling upstream | Row exists, upstream call has not finished. Should only ever be visible for sub-second windows; if you see it for minutes, the consumer crashed between the insert and the upstream call (very rare) or the upstream call hung and the event was nacked but redelivery is blocked. | No |
| `acknowledged` | consumer, after `AddMonitoring` returns | Upstream took the job, `external_id` is now set, reconciler will poll it. | No |
| `searching` | reconciler | Upstream says `monitored` / `searching` / `searching_now`. | No |
| `found` | reconciler | Upstream says `found` / `found_pending`. | No |
| `downloading` | reconciler | Upstream says `grabbed` / `downloading`. | No |
| `imported` | reconciler | Upstream says `completed` / `imported`. Emits `request_fulfilled`. | Yes |
| `failed` | consumer or reconciler | Either the consumer couldn't forward (validation, upstream error), or the reconciler saw `failed` / `not_found` / `error`, or an admin clicked Mark failed. Emits `request_failed` (except admin force-fail, which is just for cleanup). | Yes |

`error_text` is populated for transient upstream errors during polling and
cleared again on the first successful poll (`MarkPolled` sets it to `NULL`).
A row with a non-empty `error_text` is not necessarily stuck — it just last
failed to poll.

## Events the plugin emits

All names are suffixes; the host namespaces them under
`plugin.continuum.ebook-requests.<suffix>`.

| Event | When | Payload (camelCase variants are also included for compatibility) |
| --- | --- | --- |
| `request_acknowledged` | Consumer accepted the event and `AddMonitoring` returned an ID. | `request_id`, `external_id`, `provider_plugin_id` |
| `request_status_changed` | Reconciler observed a non-terminal transition. | `request_id`, `external_id`, `status`, `provider_plugin_id` |
| `request_fulfilled` | Reconciler observed terminal `completed` / `imported`. | `request_id`, `external_id`, `fulfilled_book_id`, `provider_plugin_id` |
| `request_failed` | Validation, upstream error during forward, or terminal `failed`/`not_found`/`error`. | `request_id`, `external_id` (if any), `reason`, `provider_plugin_id` |

Note: the admin **Mark failed** action only updates the DB row and does
**not** emit `request_failed`. Use it when you want to clear an unrecoverable
row from the queue without producing a user-visible event in the portal.

## Idempotency and at-least-once delivery

Event delivery is at-least-once. The plugin is hardened against duplicate
delivery in two places:

1. **Consumer:** `UpsertForwardedRequest` is keyed on `request_id`. A
   redelivery of `request_submitted` repeats `AddMonitoring`; the upstream
   is expected to dedupe by metadata. The new `external_id` (if different)
   replaces the old one in the row; the worst case is one orphaned upstream
   job.
2. **Store:** the UPSERT in `UpsertForwardedRequest` and the UPDATE in
   `MarkPolled` both contain the **terminal guard** — a `CASE` expression
   that refuses to move `status` away from `imported` or `failed`. A
   late-arriving redelivery cannot resurrect a completed request. See
   [database-ops.md](database-ops.md) for the SQL.

## When the consumer nacks vs. acks

The handler returns an error (nack, host redelivers) only when *not*
processing now would lose the request. Specifically:

- Plugin not yet configured (capability servers serve before `Configure`).
- `UpsertForwardedRequest` failed (DB outage). Starting the upstream job
  without a DB row would leak an orphan.
- `AddMonitoring` succeeded but persisting `acknowledged` + `external_id`
  failed. Without `external_id`, the reconciler skips the row forever.

It acks (success, never redelivered) when the failure is permanent:

- Event isn't `request_submitted`, or has no payload, or no `request_id`.
- Target plugin ID is not ours.
- Title and ISBN both empty (upstream needs one of them).
- `AddMonitoring` returned an error and we successfully recorded the row
  as `failed` and published `request_failed`.
