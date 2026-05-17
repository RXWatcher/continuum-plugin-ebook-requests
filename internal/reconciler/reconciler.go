// Package reconciler periodically polls upstream EbookDB for status of
// non-terminal forwarded_request rows and emits status events.
package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/store"
)

// tickTimeout caps a full Tick invocation. The scheduler fires this task on
// a 1-minute cron; capping below that prevents the next tick from arriving
// while we're still working and avoids starving other scheduled tasks if
// the upstream EbookDB hangs.
const tickTimeout = 45 * time.Second

// perRowTimeout caps each upstream lookup. We process up to 200 rows per
// tick; 1s per row × 200 + slack fits comfortably inside tickTimeout.
const perRowTimeout = 10 * time.Second

type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store    *store.Store
	Pub      Publisher
	EBK      *ebookdb.Client
	PluginID string
}

type Reconciler struct {
	deps Deps

	// tickMu guards against overlapping Tick calls. If a previous Tick is
	// still running when the scheduler fires the next one, the new call
	// returns immediately instead of doubling up on upstream calls and DB
	// writes. The SDK scheduler is generally serial, but a slow upstream +
	// clock skew can occasionally trigger overlap.
	tickMu sync.Mutex
}

func New(d Deps) *Reconciler { return &Reconciler{deps: d} }

func (r *Reconciler) Tick(ctx context.Context) error {
	if !r.tickMu.TryLock() {
		// Previous tick still running. Drop this one rather than queuing
		// extra work behind it.
		return nil
	}
	defer r.tickMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	rows, err := r.deps.Store.ListNonTerminal(ctx, 200)
	if err != nil {
		return err
	}
	// firstErr captures the first non-nil error from per-row work. We keep
	// processing remaining rows so one dead row doesn't starve the others,
	// but return the error at the end so the SDK records a failed tick and
	// operators can see why.
	var firstErr error
	for _, row := range rows {
		// Tick budget exhausted (or cancelled): stop now. Continuing would
		// make every remaining GetMonitoring/upsert fail with "context
		// deadline exceeded" and record that as a per-row upstream error
		// across all the rows we didn't get to.
		if ctx.Err() != nil {
			break
		}
		if row.ExternalID == "" {
			continue
		}
		rowCtx, rowCancel := context.WithTimeout(ctx, perRowTimeout)
		snap, err := r.deps.EBK.GetMonitoring(rowCtx, row.ExternalID)
		rowCancel()
		if err != nil {
			if uerr := r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID: row.RequestID, ExternalID: row.ExternalID,
				Status: row.Status, LastPolled: time.Now(),
				ErrorText: err.Error(), UpdatedAt: time.Now(),
			}); uerr != nil && firstErr == nil {
				firstErr = fmt.Errorf("upsert (after upstream err): %w", uerr)
			}
			continue
		}
		newStatus := translateStatus(snap.Status)
		// "" => unknown upstream status: hold the current status. Either way
		// the poll succeeded, so MarkPolled stamps last_polled and clears any
		// sticky error_text from a previous transient failure.
		if newStatus == "" || newStatus == row.Status {
			if uerr := r.deps.Store.MarkPolled(ctx, row.RequestID, row.ExternalID, row.Status, time.Now()); uerr != nil && firstErr == nil {
				firstErr = fmt.Errorf("mark polled (no transition): %w", uerr)
			}
			continue
		}
		if uerr := r.deps.Store.MarkPolled(ctx, row.RequestID, row.ExternalID, newStatus, time.Now()); uerr != nil && firstErr == nil {
			firstErr = fmt.Errorf("mark polled (status change): %w", uerr)
		}
		switch newStatus {
		case "imported":
			r.deps.Pub.Publish(ctx, "request_fulfilled", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"fulfilled_book_id":  snap.BookID,
				"provider_plugin_id": r.deps.PluginID,
			})
		case "failed":
			r.deps.Pub.Publish(ctx, "request_failed", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"provider_plugin_id": r.deps.PluginID,
				"reason":             "upstream marked failed",
			})
		default:
			r.deps.Pub.Publish(ctx, "request_status_changed", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"provider_plugin_id": r.deps.PluginID,
				"status":             newStatus,
			})
		}
	}
	return firstErr
}

// translateStatus maps the upstream monitoring status (MonitoredBook.Status:
// monitored | searching | searching_now | found | found_pending | grabbed |
// downloading | completed | failed | not_found) to the portal-facing status.
func translateStatus(ebkStatus string) string {
	switch ebkStatus {
	case "monitored", "searching", "searching_now":
		return "searching"
	case "found", "found_pending":
		return "found"
	case "grabbed", "downloading":
		return "downloading"
	case "completed", "imported":
		return "imported"
	case "failed", "not_found", "error":
		return "failed"
	}
	// Unknown/unmapped upstream status: signal "no transition" so the caller
	// holds the current status. Previously this returned "acknowledged",
	// which regressed an in-flight request (e.g. downloading -> acknowledged)
	// and spammed a status-changed event on every poll.
	return ""
}
