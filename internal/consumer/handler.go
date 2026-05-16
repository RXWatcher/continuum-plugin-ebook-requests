// Package consumer implements the event_consumer.v1 handler for
// request_submitted events. It requires a source_id (Anna's Archive md5) in
// the payload.
package consumer

import (
	"context"
	"time"

	"github.com/hashicorp/go-hclog"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/store"
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
	if req.GetEventName() != "plugin.continuum.ebooks.request_submitted" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	if req.GetPayload() == nil {
		return &pluginv1.HandleEventResponse{}, nil
	}
	d := h.depsFn()
	if d == nil {
		return &pluginv1.HandleEventResponse{}, nil
	}
	p := req.GetPayload().AsMap()
	if target := targetPluginIDFromPayload(p); target != d.PluginID {
		return &pluginv1.HandleEventResponse{}, nil
	}
	requestID := requestIDFromPayload(p)
	if requestID == "" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	sourceID, _ := p["source_id"].(string)
	formatPref, _ := p["format_pref"].(string)

	if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, Status: "submitted", UpdatedAt: time.Now(),
	}); err != nil {
		h.logger.Warn("upsert forwarded_request (initial)", "request_id", requestID, "err", err)
	}

	if sourceID == "" {
		reason := "source_id required for Anna's Archive downloader"
		if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: reason, UpdatedAt: time.Now(),
		}); err != nil {
			h.logger.Warn("upsert forwarded_request (missing source_id)",
				"request_id", requestID, "err", err)
		}
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id": requestID, "requestId": requestID,
			"provider_plugin_id": d.PluginID, "reason": reason,
		})
		return &pluginv1.HandleEventResponse{}, nil
	}

	resp, err := d.EBK.StartDownload(ctx, sourceID, formatPref)
	if err != nil {
		if uerr := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: err.Error(), UpdatedAt: time.Now(),
		}); uerr != nil {
			h.logger.Warn("upsert forwarded_request (after StartDownload err)",
				"request_id", requestID, "upstream_err", err, "db_err", uerr)
		}
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id": requestID, "requestId": requestID,
			"provider_plugin_id": d.PluginID, "reason": err.Error(),
		})
		return &pluginv1.HandleEventResponse{}, nil
	}
	if uerr := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, ExternalID: resp.ID, Status: "acknowledged", UpdatedAt: time.Now(),
	}); uerr != nil {
		// Upstream already accepted; logging is enough — the reconciler will
		// heal the row on the next tick. Returning an error here would cause
		// the host to retry the event, duplicate-adding upstream.
		h.logger.Warn("upsert forwarded_request (after acknowledged)",
			"request_id", requestID, "external_id", resp.ID, "db_err", uerr)
	}
	d.Pub.Publish(ctx, "request_acknowledged", map[string]any{
		"request_id": requestID, "requestId": requestID,
		"external_id": resp.ID, "provider_plugin_id": d.PluginID,
	})
	return &pluginv1.HandleEventResponse{}, nil
}

func targetPluginIDFromPayload(p map[string]any) string {
	for _, key := range []string{"target_plugin_id", "target_provider_plugin_id", "provider_plugin_id"} {
		if v, _ := p[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func requestIDFromPayload(p map[string]any) string {
	if id, _ := p["request_id"].(string); id != "" {
		return id
	}
	id, _ := p["requestId"].(string)
	return id
}
