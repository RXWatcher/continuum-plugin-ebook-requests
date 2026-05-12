// Package consumer implements the event_consumer.v1 handler for
// request_submitted events. Unlike bookwarehouse-ebook, it requires
// a source_id (Anna's-Archive md5) in the payload.
package consumer

import (
	"context"
	"time"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/store"
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
}

func New(depsFn func() *Deps) *Handler { return &Handler{depsFn: depsFn} }

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
	if target, _ := p["target_plugin_id"].(string); target != d.PluginID {
		return &pluginv1.HandleEventResponse{}, nil
	}
	requestID, _ := p["request_id"].(string)
	if requestID == "" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	sourceID, _ := p["source_id"].(string)
	formatPref, _ := p["format_pref"].(string)

	_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, Status: "submitted", UpdatedAt: time.Now(),
	})

	if sourceID == "" {
		reason := "source_id required for EbookDB backend"
		_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: reason, UpdatedAt: time.Now(),
		})
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id": requestID, "reason": reason,
		})
		return &pluginv1.HandleEventResponse{}, nil
	}

	resp, err := d.EBK.StartDownload(ctx, sourceID, formatPref)
	if err != nil {
		_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID: requestID, Status: "failed", ErrorText: err.Error(), UpdatedAt: time.Now(),
		})
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id": requestID, "reason": err.Error(),
		})
		return &pluginv1.HandleEventResponse{}, nil
	}
	_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID: requestID, ExternalID: resp.ID, Status: "acknowledged", UpdatedAt: time.Now(),
	})
	d.Pub.Publish(ctx, "request_acknowledged", map[string]any{
		"request_id": requestID, "external_id": resp.ID,
	})
	return &pluginv1.HandleEventResponse{}, nil
}
