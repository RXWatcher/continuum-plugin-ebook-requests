# Ebook Requests Setup, Debugging, And Flows

Plugin ID: `continuum.ebook-requests`
Version documented: `0.1.0`

## Purpose

ebook download/request provider for continuum.ebooks; forwards approved ebook requests
to an operator-managed downloader service and reconciles job state.

## Runtime Dependencies

- Continuum plugin host
- Postgres schema for this plugin
- continuum.ebooks when used from the portal
- An operator-managed Anna's Archive style downloader API reachable from the plugin runtime

## Setup Checklist

1. Install continuum.ebooks first if users will submit requests through the portal.
2. Create the plugin database role and schema shown in the README.
3. Configure database_url, base_url, api_key, and optional source priority/cover settings.
4. Install and enable the plugin from the catalog.
5. In Ebooks admin settings, select this installation as the request/download provider.
6. Submit a low-risk test request and confirm the request row moves from submitted to acknowledged or failed with a useful error.

## Configuration Reference

- `database_url`
- `base_url`
- `api_key`
- `default_cover_size`
- `external_source_priority`

Use the plugin manifest/admin form as the source of truth for field validation and defaults. Keep database credentials scoped to the plugin schema unless a plugin explicitly needs read access to Continuum core tables.

## Exposed Routes

- `* /api/v1/* [authenticated]`

## Capabilities

- `http_routes.v1 (backend) - Download-provider API for Anna's Archive style search, fetch, and request forwarding.`
- `event_consumer.v1 (request_handler) - Forwards ebook request_submitted events to Anna's Archive download jobs.`
- `ebook_backend.v1 (default) - Request/download provider backed by an Anna's Archive download service; not a presentation library source.`
- `scheduled_task.v1 (reconciler) - Polls the upstream Anna's Archive download service for status changes on non-terminal downloads.`

## Operational Flows

### Ebook request

1. User submits or admin approves an ebook request in continuum.ebooks.
2. Ebooks emits plugin.continuum.ebooks.request_submitted with target provider metadata.
3. This plugin ignores events for other providers, searches/forwards to the configured downloader, and stores the upstream job reference.
4. The scheduled reconciler polls non-terminal jobs and publishes status/final events back to Continuum.
5. continuum.ebooks consumes those events and updates the request visible to the user.

## How This Plugin Communicates

- Consumes ebook request events from continuum.ebooks.
- Publishes acknowledgement, status, fulfilled, and failed events for continuum.ebooks.
- Does not stream files directly to users; it reports fulfillment so the ebook portal/backend can finish the workflow.

## Debugging Runbook

- Run the diagnostics endpoint under /api/v1/* from an authenticated admin session.
- Check plugin logs for upstream HTTP status, request ID, and reconcile messages.
- Verify base_url is reachable from inside the Continuum container/network, not only from your browser.
- Confirm api_key is accepted by the upstream service and is not accidentally quoted in config.
- If requests stay submitted, confirm continuum.ebooks targets this plugin installation ID and that event delivery is enabled.

## Log And Health Checks

- Start with Continuum Admin -> Plugins and confirm the installation is enabled.
- Check the plugin process logs around startup for manifest loading, migration, and route registration.
- Check scheduled task logs when a workflow depends on polling or reconciliation.
- Confirm the plugin routes are reachable through Continuum using the access level shown above.
- For database-backed plugins, verify the configured role can connect, create/migrate tables in its schema, and read/write expected rows.

## Common Failure Patterns

- Wrong installation ID selected in a portal or router setting after reinstalling a plugin.
- Plugin database URL points at the public schema instead of the dedicated plugin schema.
- Reverse proxy forwards the SPA route but not `/api/*`, `/api/v1/*`, `/assets/*`, or provider-specific public routes.
- Network checks are run from the operator laptop instead of from the Continuum/plugin runtime network.
- Secrets are regenerated during restart, invalidating signed URLs, encrypted fields, or login state.

## Verification After Changes

1. Restart or reload the plugin installation.
2. Open the plugin route or admin page in Continuum.
3. Exercise the smallest workflow that crosses a plugin boundary.
4. Confirm both the source plugin and destination plugin record the same request/session/login identifier.
5. Leave the scheduled reconciler enough time to run, then confirm terminal state or a useful error.
