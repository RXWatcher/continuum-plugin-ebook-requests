# Ebook Requests Operator Docs

Plugin ID: `continuum.ebook-requests`
Previous ID: `continuum.annas-archive-downloader`

These docs are for operators running the plugin. The [README](../README.md)
covers what the plugin is, its capabilities, configuration keys, and the
event surface. The pages here go deeper on the things you only need when
something is wrong or unusual.

| Topic | When you need it |
| --- | --- |
| [Request lifecycle](request-lifecycle.md) | Understanding what state a stuck row is actually in. |
| [Upstream EbookDB](upstream-ebookdb.md) | `base_url` / API key problems, redirects, 10 MiB cap, 429 backoff. |
| [Status translation](status-translation.md) | "Upstream says X, the portal shows Y." Reading the mapping table. |
| [Admin API and UI](admin-api.md) | Every `/api/v1/admin/*` endpoint, plus the `/admin` SPA tabs. |
| [Debugging runbook](debugging-runbook.md) | Diagnose by symptom (stuck `submitted`, 429 storm, status flapping, etc.). |
| [Coexistence with other providers](coexistence.md) | Running this plugin alongside `bookwarehouse-ebook` or others. |
| [Database operations](database-ops.md) | Pool sizing, terminal guard semantics, retry/force-fail, manual SQL. |

If you are setting the plugin up for the first time, start with the README,
then read [request-lifecycle.md](request-lifecycle.md) and
[upstream-ebookdb.md](upstream-ebookdb.md) before submitting a test request.
