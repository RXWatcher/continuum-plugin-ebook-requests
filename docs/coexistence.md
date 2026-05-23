# Coexistence with other ebook providers

`silo.ebook-requests` is *one* possible request/download provider
for `silo.ebooks`. The other one shipped today is
[`silo-plugin-bookwarehouse-ebook`](https://github.com/RXWatcher/silo-plugin-bookwarehouse-ebook).
Operators can run more than one installed at the same time, but for any
given request **exactly one** provider must handle it.

## How routing actually works

The portal (`silo.ebooks`) publishes
`plugin.silo.ebooks.request_submitted` with a target plugin ID on
the payload. This plugin's consumer checks three keys, in order, for the
target:

1. `target_plugin_id`
2. `target_provider_plugin_id`
3. `provider_plugin_id`

The first one present (after `strings.TrimSpace`) is the target. If the
target equals `silo.ebook-requests` (our plugin ID, taken from the
manifest at `Configure` time), the consumer processes the event.
Otherwise the consumer **acks and drops** — the event is gone as far as
this plugin is concerned, and another plugin's consumer will pick it up.

If the payload contains *none* of the three keys, the consumer also acks
and drops. This is conservative; an event with no target shouldn't reach
us, but if it does we don't claim it.

## Selecting the provider in the portal

In the Ebooks admin settings, select the installation ID of the provider
you want. The portal stamps that ID into the event payload. If the wrong
installation ID is selected:

- After a reinstall, the old installation ID is gone but the portal
  still has it stored — the event has no matching consumer and the
  request hangs in the portal until the user resubmits or you fix the
  setting.
- After installing a second provider, leaving the old one selected
  routes all requests to the old one. The new one looks idle even though
  it's configured correctly.

This is the single most common operator mistake. If a request never
reaches `forwarded_request`, check the portal's selected provider first.

## Running two providers simultaneously

It's safe. Both will be subscribed to the same event topic, and the
target-plugin-id filter ensures only one acts. The other one's consumer
ack-and-drops the event.

What this does **not** give you:

- **Failover.** If the targeted provider is down, the event isn't
  re-routed to another provider. The portal needs to know to re-emit
  with a different target, which today it doesn't do automatically.
- **Per-format routing.** The portal selects the provider per-request,
  not per-format. There's no built-in "use BookWarehouse for PDFs and
  ebook-requests for EPUBs".

## Telltale signs of a routing mismatch

- Portal logs show `request_submitted` being published.
- Plugin logs show **nothing** for that `request_id`. (Specifically, the
  consumer ack-and-drops without logging, so absence of a log is the
  signal — there is no "ignored event" log line.)
- `forwarded_request` has no row for that `request_id`.
- Other provider's logs / DB do have it.

If you suspect this and the other provider isn't installed, the bug is in
the portal payload — log the payload at the portal side and inspect
`target_plugin_id`.

## Migrating from the old plugin ID

This plugin used to ship as `silo.annas-archive-downloader`. The new
ID is `silo.ebook-requests`. If you're migrating:

1. Install the new plugin and configure it.
2. Switch the Ebooks provider selection to the new installation ID.
3. Let in-flight rows from the old plugin drain (or admin Mark failed
   them).
4. Uninstall the old plugin.

The two plugins do not share a database schema; `forwarded_request` rows
in the old schema are not visible to the new one. There is no automatic
migration tool — in practice the in-flight queue is small enough to
drain manually.
