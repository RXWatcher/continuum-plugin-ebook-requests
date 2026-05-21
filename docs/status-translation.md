# Status translation

The upstream EbookDB and the Continuum portal use different vocabularies
for "where is this request right now". This plugin owns the mapping. If
you ever see a status mismatch between the upstream UI and the portal,
this is where the translation lives.

## The mapping

Source: `internal/reconciler/reconciler.go:translateStatus`.

| Upstream status (`MonitoredBook.Status`) | Portal status written to `forwarded_request.status` | Event emitted on transition |
| --- | --- | --- |
| `monitored` | `searching` | `request_status_changed` |
| `searching` | `searching` | `request_status_changed` |
| `searching_now` | `searching` | `request_status_changed` |
| `found` | `found` | `request_status_changed` |
| `found_pending` | `found` | `request_status_changed` |
| `grabbed` | `downloading` | `request_status_changed` |
| `downloading` | `downloading` | `request_status_changed` |
| `completed` | `imported` (**terminal**) | `request_fulfilled` |
| `imported` | `imported` (**terminal**) | `request_fulfilled` |
| `failed` | `failed` (**terminal**) | `request_failed` |
| `not_found` | `failed` (**terminal**) | `request_failed` |
| `error` | `failed` (**terminal**) | `request_failed` |
| anything else | *(no transition — row keeps current status)* | *(no event)* |

`acknowledged` and `submitted` are local-only — the upstream never reports
them. They're set by the consumer before the first poll.

## The "unknown upstream status" rule

When the upstream returns a status the table doesn't cover, the reconciler
treats it as "no transition" — it keeps `forwarded_request.status` whatever
it currently is, stamps `last_polled`, clears `error_text`, and emits no
event. This was a deliberate fix for a previous bug where unknown statuses
fell back to `acknowledged`, which regressed in-flight requests
(`downloading` → `acknowledged`) on every poll and spammed
`request_status_changed`.

Practical consequence: if the upstream ever introduces a new status the
plugin doesn't know about, the request will appear to freeze (stop
emitting status updates) but will still progress to `imported` / `failed`
when the upstream eventually reaches a known state. Update
`translateStatus` to extend the table.

## How transitions are detected

`reconciler.Tick` reads each non-terminal row, calls upstream, runs the
result through `translateStatus`, then:

- If `newStatus == ""` (unknown upstream status) **or** `newStatus ==
  row.Status`: call `MarkPolled` with the existing status. This stamps
  `last_polled`, clears any sticky `error_text`, and emits no event.
- Otherwise: call `MarkPolled` with `newStatus` (which honours the terminal
  guard), and publish exactly one event — `request_fulfilled`,
  `request_failed`, or `request_status_changed`.

Transitions are detected by comparing strings, not timestamps — there is
no monotonic version per row. If two ticks observe the same status they
produce zero events; if a tick is missed entirely (e.g. backoff window),
the next successful poll catches up by direct comparison.

## What `continuum.ebooks` does with these

`continuum.ebooks` consumes these events and updates the user-visible
request. From the user's perspective the visible states are roughly:

- `submitted` / `acknowledged` → "Searching"
- `searching` / `found` / `downloading` → "Downloading"
- `imported` → "Available" (with `fulfilled_book_id` linking to the book)
- `failed` → "Failed" (with `reason`)

Exact rendering is `continuum.ebooks`' concern. This plugin only commits
to the four emitted events and to the schema of their payloads.
