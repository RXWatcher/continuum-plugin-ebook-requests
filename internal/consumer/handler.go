// Package consumer implements the event_consumer.v1 handler for
// request_submitted events. It requires a source_id (Anna's Archive md5) in
// the payload.
package consumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/RXWatcher/silo-plugin-ebook-requests/internal/ebookdb"
	"github.com/RXWatcher/silo-plugin-ebook-requests/internal/store"
)

type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store    *store.Store
	Pub      Publisher
	EBK      *ebookdb.Client
	PluginID string
}

type Handler struct {
	pluginv1.UnimplementedEventConsumerServer
	depsFn func() *Deps
	logger hclog.Logger
}

// New constructs the handler. logger may be nil; a null logger is used so
// the handler is safe to use in tests.
func New(depsFn func() *Deps, logger hclog.Logger) *Handler {
	if logger == nil {
		logger = hclog.NewNullLogger()
	}
	return &Handler{depsFn: depsFn, logger: logger}
}

func (h *Handler) HandleEvent(ctx context.Context, req *pluginv1.HandleEventRequest) (*pluginv1.HandleEventResponse, error) {
	if req.GetEventName() != "plugin.silo.ebooks.request_submitted" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	if req.GetPayload() == nil {
		return &pluginv1.HandleEventResponse{}, nil
	}
	if h.depsFn == nil {
		return nil, fmt.Errorf("plugin not configured yet")
	}
	d := h.depsFn()
	if d == nil {
		// Capability servers serve before Configure runs. Nack so the host
		// redelivers once configured instead of acking and dropping the
		// request permanently.
		return nil, fmt.Errorf("plugin not configured yet")
	}
	if d.Store == nil || d.EBK == nil {
		return nil, fmt.Errorf("plugin dependencies not configured")
	}
	p := req.GetPayload().AsMap()
	target, targeted := targetPluginIDFromPayload(p)
	if !targeted || target != d.PluginID {
		return &pluginv1.HandleEventResponse{}, nil
	}
	requestID := requestIDFromPayload(p)
	if requestID == "" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	title, _ := p["title"].(string)
	isbn, _ := p["isbn"].(string)
	formatPref, _ := p["format_pref"].(string)
	authors := stringSliceFromPayload(p, "authors")

	// Must persist before kicking off the upstream download: if this row is
	// lost the reconciler never polls it and the request is permanently lost
	// (worse, the upstream job would be orphaned). Nack instead of starting
	// untracked work; the terminal guard in UpsertForwardedRequest makes the
	// inevitable redelivery idempotent.
	if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, Status: "submitted", UpdatedAt: time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("persist submitted %s: %w", requestID, err)
	}

	// The upstream searches Anna's Archive from metadata (no md5-direct
	// path), requiring an ISBN or a title. With neither there is nothing to
	// search — a permanent client error, so ack (don't poison-loop) and
	// publish request_failed.
	if title == "" && isbn == "" {
		reason := "title or isbn required to request a download"
		if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: reason, UpdatedAt: time.Now(),
		}); err != nil {
			h.logger.Warn("upsert forwarded_request (missing metadata)",
				"request_id", requestID, "err", err)
		}
		publish(ctx, d.Pub, "request_failed", map[string]any{
			"request_id": requestID, "requestId": requestID,
			"provider_plugin_id": d.PluginID, "reason": reason,
		})
		return &pluginv1.HandleEventResponse{}, nil
	}

	// Bound the upstream call so a slow/hung downloader can't pin the event
	// consumer goroutine. The plugin host's own event deadline is generous;
	// 10s here matches the reconciler per-row budget and keeps user-facing
	// "submitted but no acknowledged" gaps short.
	addCtx, addCancel := context.WithTimeout(ctx, 10*time.Second)
	resp, err := d.EBK.AddMonitoring(addCtx, ebookdb.MonitoringRequest{
		Title: title, Authors: authors, ISBN: isbn, FormatPref: formatPref,
	})
	addCancel()
	if err != nil {
		if uerr := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: err.Error(), UpdatedAt: time.Now(),
		}); uerr != nil {
			// Couldn't even record the failure — nack so it's retried rather
			// than left stuck non-terminal (the row is still "submitted",
			// which the reconciler skips forever for want of an external_id).
			return nil, fmt.Errorf("persist failed %s: %w (upstream: %v)", requestID, uerr, err)
		}
		publish(ctx, d.Pub, "request_failed", map[string]any{
			"request_id": requestID, "requestId": requestID,
			"provider_plugin_id": d.PluginID, "reason": err.Error(),
		})
		return &pluginv1.HandleEventResponse{}, nil
	}
	if uerr := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, ExternalID: resp.ID, Status: "acknowledged", UpdatedAt: time.Now(),
	}); uerr != nil {
		// Must persist the external_id: without it the reconciler skips this
		// row forever (it requires a non-empty external_id), so the started
		// download is never polled and the request hangs permanently. Nack;
		// the terminal guard makes redelivery idempotent. (Re-running
		// AddMonitoring is the accepted tradeoff vs. a permanently lost
		// request.)
		return nil, fmt.Errorf("persist acknowledged %s: %w", requestID, uerr)
	}
	publish(ctx, d.Pub, "request_acknowledged", map[string]any{
		"request_id": requestID, "requestId": requestID,
		"external_id": resp.ID, "provider_plugin_id": d.PluginID,
	})
	return &pluginv1.HandleEventResponse{}, nil
}

func publish(ctx context.Context, pub Publisher, name string, payload map[string]any) {
	if pub == nil {
		return
	}
	pub.Publish(ctx, name, payload)
}

func targetPluginIDFromPayload(p map[string]any) (string, bool) {
	for _, key := range []string{"target_plugin_id", "target_provider_plugin_id", "provider_plugin_id"} {
		if target, ok := trimmedStringValue(p, key); ok {
			return target, true
		}
	}
	return "", false
}

func trimmedStringValue(p map[string]any, key string) (string, bool) {
	v, ok := p[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", true
	}
	return strings.TrimSpace(s), true
}

func requestIDFromPayload(p map[string]any) string {
	if id, _ := p["request_id"].(string); id != "" {
		return id
	}
	id, _ := p["requestId"].(string)
	return id
}

// stringSliceFromPayload reads a []string from a structpb-decoded payload
// (JSON arrays decode to []any).
func stringSliceFromPayload(p map[string]any, key string) []string {
	v, ok := p[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
