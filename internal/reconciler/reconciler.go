// Package reconciler periodically polls upstream EbookDB for status of
// non-terminal forwarded_request rows and emits status events.
package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RXWatcher/silo-plugin-ebook-requests/internal/ebookdb"
	"github.com/RXWatcher/silo-plugin-ebook-requests/internal/store"
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

	// status snapshots are written under statusMu after every Tick. The
	// admin UI reads them to render "Last run: 2m ago" + last error so
	// operators can confirm the cron is alive without checking plugin logs.
	statusMu sync.RWMutex
	status   Status

	// lastErrorText is the deduplication key for per-row upstream errors.
	// If the next Tick records the same error_text on the same request_id,
	// we skip the UPDATE (the row already has it) to keep the DB quiet when
	// the upstream is down for hours. Cleared on success.
	lastErrorMu sync.Mutex
	lastError   map[string]string

	// backoffUntil parks the whole reconciler when the upstream returns 429.
	// Across the next ticks we skip the work entirely (no row-level calls,
	// no DB updates) until the Retry-After window has passed. Set by Tick
	// on a *ebookdb.RateLimitError, read by Tick at the top.
	backoffMu    sync.Mutex
	backoffUntil time.Time
}

// Status snapshots the most recent Tick outcome. RowsProcessed is the row
// count ListNonTerminal returned at the start of the Tick (i.e. the work
// the Tick attempted, not how many succeeded).
type Status struct {
	LastRunAt     time.Time
	LastDuration  time.Duration
	RowsProcessed int
	LastError     string
	// Skipped indicates a Tick fired while another was already running and
	// was dropped to avoid doubling up. Diagnostic for when poll cadence is
	// shorter than per-tick latency.
	Skipped bool
}

func New(d Deps) *Reconciler {
	return &Reconciler{deps: d, lastError: map[string]string{}}
}

// LastStatus returns the most recent Tick outcome. Zero Status before the
// first Tick fires.
func (r *Reconciler) LastStatus() Status {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

func (r *Reconciler) setStatus(s Status) {
	r.statusMu.Lock()
	r.status = s
	r.statusMu.Unlock()
}

// recordErrorChanged returns true if the per-row error_text differs from the
// last value the reconciler observed for that row, so the caller knows
// whether to persist it. Empty text always returns true so a fresh failure
// surfaces. Clearing happens via clearError on a successful poll.
func (r *Reconciler) recordErrorChanged(requestID, errText string) bool {
	r.lastErrorMu.Lock()
	defer r.lastErrorMu.Unlock()
	prev, ok := r.lastError[requestID]
	if ok && prev == errText {
		return false
	}
	r.lastError[requestID] = errText
	return true
}

func (r *Reconciler) clearError(requestID string) {
	r.lastErrorMu.Lock()
	delete(r.lastError, requestID)
	r.lastErrorMu.Unlock()
}

// backoffRemaining returns how much longer the reconciler is parked, or 0 if
// it isn't. Truthy result causes the next Tick to no-op.
func (r *Reconciler) backoffRemaining() time.Duration {
	r.backoffMu.Lock()
	defer r.backoffMu.Unlock()
	if r.backoffUntil.IsZero() {
		return 0
	}
	d := time.Until(r.backoffUntil)
	if d <= 0 {
		r.backoffUntil = time.Time{}
		return 0
	}
	return d
}

// setBackoff parks the reconciler for at least d. Defaults to 60s when
// upstream omitted Retry-After. Caps at 10 minutes so a misbehaving upstream
// can't pin us forever.
func (r *Reconciler) setBackoff(d time.Duration) {
	if d <= 0 {
		d = 60 * time.Second
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	r.backoffMu.Lock()
	r.backoffUntil = time.Now().Add(d)
	r.backoffMu.Unlock()
}

func (r *Reconciler) Tick(ctx context.Context) error {
	if !r.tickMu.TryLock() {
		// Previous tick still running. Drop this one rather than queuing
		// extra work behind it.
		r.setStatus(Status{
			LastRunAt:    time.Now(),
			LastDuration: 0,
			Skipped:      true,
		})
		return nil
	}
	defer r.tickMu.Unlock()

	if remain := r.backoffRemaining(); remain > 0 {
		// Upstream most recently returned 429; do nothing until the
		// Retry-After window has passed.
		r.setStatus(Status{
			LastRunAt:    time.Now(),
			LastDuration: 0,
			Skipped:      true,
			LastError:    fmt.Sprintf("backoff: upstream rate-limited, %s remaining", remain.Round(time.Second)),
		})
		return nil
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	rows, err := r.deps.Store.ListNonTerminal(ctx, 200)
	if err != nil {
		r.setStatus(Status{
			LastRunAt:    time.Now(),
			LastDuration: time.Since(start),
			LastError:    err.Error(),
		})
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
			// Upstream told us to back off: park the whole reconciler so
			// the next tick is a no-op, record the failure on this row,
			// and break out of the loop (continuing would just produce
			// more 429s).
			if rl, ok := ebookdb.IsRateLimited(err); ok {
				r.setBackoff(rl.RetryAfter)
				if r.recordErrorChanged(row.RequestID, err.Error()) {
					_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
						RequestID: row.RequestID, ExternalID: row.ExternalID,
						Status: row.Status, LastPolled: time.Now(),
						ErrorText: err.Error(), UpdatedAt: time.Now(),
					})
				}
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			// Dedupe: if the same row keeps producing the same error_text
			// across consecutive ticks (upstream is down for hours), skip
			// the UPDATE the second time onward. Keeps the DB quiet and
			// avoids per-tick log spam. last_polled still moves forward via
			// the upsert path the first time the error changes.
			if !r.recordErrorChanged(row.RequestID, err.Error()) {
				continue
			}
			if uerr := r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID: row.RequestID, ExternalID: row.ExternalID,
				Status: row.Status, LastPolled: time.Now(),
				ErrorText: err.Error(), UpdatedAt: time.Now(),
			}); uerr != nil && firstErr == nil {
				firstErr = fmt.Errorf("upsert (after upstream err): %w", uerr)
			}
			continue
		}
		r.clearError(row.RequestID)
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
			r.clearError(row.RequestID)
			r.deps.Pub.Publish(ctx, "request_fulfilled", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"fulfilled_book_id":  snap.BookID,
				"provider_plugin_id": r.deps.PluginID,
			})
		case "failed":
			r.clearError(row.RequestID)
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
	s := Status{
		LastRunAt:     time.Now(),
		LastDuration:  time.Since(start),
		RowsProcessed: len(rows),
	}
	if firstErr != nil {
		s.LastError = firstErr.Error()
	}
	r.setStatus(s)
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
