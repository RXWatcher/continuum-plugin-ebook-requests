// Package reconciler periodically polls upstream EbookDB for status of
// non-terminal forwarded_request rows and emits status events.
package reconciler

import (
	"context"
	"time"

	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/store"
)

type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store *store.Store
	Pub   Publisher
	EBK   *ebookdb.Client
}

type Reconciler struct {
	deps Deps
}

func New(d Deps) *Reconciler { return &Reconciler{deps: d} }

func (r *Reconciler) Tick(ctx context.Context) error {
	rows, err := r.deps.Store.ListNonTerminal(ctx, 200)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ExternalID == "" {
			continue
		}
		snap, err := r.deps.EBK.GetDownload(ctx, row.ExternalID)
		if err != nil {
			_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID: row.RequestID, ExternalID: row.ExternalID,
				Status: row.Status, LastPolled: time.Now(),
				ErrorText: err.Error(), UpdatedAt: time.Now(),
			})
			continue
		}
		newStatus := translateStatus(snap.Status)
		if newStatus == row.Status {
			_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID: row.RequestID, ExternalID: row.ExternalID,
				Status: row.Status, LastPolled: time.Now(), UpdatedAt: time.Now(),
			})
			continue
		}
		_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: row.RequestID, ExternalID: row.ExternalID,
			Status: newStatus, LastPolled: time.Now(), UpdatedAt: time.Now(),
		})
		switch newStatus {
		case "imported":
			r.deps.Pub.Publish(ctx, "request_fulfilled", map[string]any{
				"request_id":        row.RequestID,
				"external_id":       row.ExternalID,
				"fulfilled_book_id": snap.BookID,
			})
		case "failed":
			r.deps.Pub.Publish(ctx, "request_failed", map[string]any{
				"request_id":  row.RequestID,
				"external_id": row.ExternalID,
				"reason":      "upstream marked failed",
			})
		default:
			r.deps.Pub.Publish(ctx, "request_status_changed", map[string]any{
				"request_id":  row.RequestID,
				"external_id": row.ExternalID,
				"status":      newStatus,
			})
		}
	}
	return nil
}

func translateStatus(ebkStatus string) string {
	switch ebkStatus {
	case "queued":
		return "acknowledged"
	case "downloading":
		return "downloading"
	case "imported", "completed":
		return "imported"
	case "failed", "error":
		return "failed"
	}
	return "acknowledged"
}
