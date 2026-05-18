package consumer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/consumer"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/store"
)

type fakePub struct {
	mu   sync.Mutex
	pubs []struct {
		Name    string
		Payload map[string]any
	}
}

func (f *fakePub) Publish(_ context.Context, name string, payload map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pubs = append(f.pubs, struct {
		Name    string
		Payload map[string]any
	}{name, payload})
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb: %v", err)
	}
	return s
}

func newConsumerForTest(t *testing.T, upstream *httptest.Server) (*consumer.Handler, *fakePub, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	pub := &fakePub{}
	ebk := ebookdb.NewClient(upstream.URL, "k")
	deps := &consumer.Deps{
		Store: st, Pub: pub, EBK: ebk,
		PluginID: "continuum.annas-archive-downloader",
	}
	h := consumer.New(func() *consumer.Deps { return deps }, nil)
	return h, pub, st
}

func TestConsumer_HappyPath_EmitsAcknowledged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"job-1","status":"queued"}`))
	}))
	defer up.Close()
	h, pub, st := newConsumerForTest(t, up)
	_, _ = h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-1",
			"target_plugin_id": "continuum.annas-archive-downloader",
			"title":            "X",
			"source_id":        "md5-x",
			"format_pref":      "epub",
		}),
	})
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_acknowledged" {
		t.Errorf("emitted = %+v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "r-1")
	if row.Status != "acknowledged" || row.ExternalID != "job-1" {
		t.Errorf("row = %+v", row)
	}
}

// The upstream searches Anna's from metadata; with neither title nor isbn
// there is nothing to search — fail fast without calling upstream.
func TestConsumer_MissingMetadata_EmitsFailed(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called")
	}))
	defer up.Close()
	h, pub, st := newConsumerForTest(t, up)
	_, _ = h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-2",
			"target_plugin_id": "continuum.annas-archive-downloader",
			// no title, no isbn
		}),
	})
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_failed" {
		t.Errorf("emitted = %+v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "r-2")
	if row.Status != "failed" {
		t.Errorf("row = %+v", row)
	}
}

func TestConsumer_SkipsTargetMismatch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream should not be called")
	}))
	defer up.Close()
	h, pub, _ := newConsumerForTest(t, up)
	_, _ = h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-3",
			"target_plugin_id": "continuum.other-ebook-provider",
			"title":            "X",
		}),
	})
	if len(pub.pubs) != 0 {
		t.Errorf("pubs = %+v", pub.pubs)
	}
}

func TestConsumer_SkipsMalformedOrConflictingTargets(t *testing.T) {
	for _, payload := range []map[string]any{
		{"request_id": "r-blank", "target_plugin_id": " ", "title": "X"},
		{"request_id": "r-numeric", "target_plugin_id": float64(1), "title": "X"},
		{
			"request_id":                "r-conflict",
			"target_plugin_id":          "continuum.other-ebook-provider",
			"target_provider_plugin_id": "continuum.annas-archive-downloader",
			"title":                     "X",
		},
	} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("upstream should not be called for payload %+v", payload)
		}))
		h, pub, _ := newConsumerForTest(t, up)
		_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
			EventName: "plugin.continuum.ebooks.request_submitted",
			Payload:   mustStruct(t, payload),
		})
		up.Close()
		if err != nil {
			t.Fatalf("HandleEvent: %v", err)
		}
		if len(pub.pubs) != 0 {
			t.Fatalf("publisher should not be called for %+v; got %+v", payload, pub.pubs)
		}
	}
}

func TestConsumer_NilPublisherDoesNotPanic(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"job-1","status":"queued"}`))
	}))
	defer up.Close()
	st := newTestStore(t)
	deps := &consumer.Deps{
		Store: st, Pub: nil, EBK: ebookdb.NewClient(up.URL, "k"),
		PluginID: "continuum.annas-archive-downloader",
	}
	h := consumer.New(func() *consumer.Deps { return deps }, nil)
	_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-nil-pub",
			"target_plugin_id": "continuum.annas-archive-downloader",
			"title":            "X",
		}),
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
}

// Capability servers serve before Configure runs. If depsFn returns nil the
// handler must nack (return an error) so the host redelivers once configured,
// instead of acking the event and dropping the request permanently.
func TestConsumer_NotConfigured_Nacks(t *testing.T) {
	h := consumer.New(func() *consumer.Deps { return nil }, nil)
	resp, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-cfg",
			"target_plugin_id": "continuum.annas-archive-downloader",
			"source_id":        "md5-x",
		}),
	})
	if err == nil {
		t.Fatal("not-configured must return an error so the host redelivers")
	}
	if resp != nil {
		t.Errorf("response must be nil on nack; got %+v", resp)
	}
}

func TestConsumer_NilDepsFn_Nacks(t *testing.T) {
	h := consumer.New(nil, nil)
	_, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "plugin.continuum.ebooks.request_submitted",
		Payload: mustStruct(t, map[string]any{
			"request_id":       "r-cfg",
			"target_plugin_id": "continuum.annas-archive-downloader",
			"title":            "X",
		}),
	})
	if err == nil {
		t.Fatal("nil depsFn must nack so the host redelivers")
	}
}

// A foreign / wrong-event message is not ours: ack (no error) and drop so the
// host does not redeliver another plugin's event to us forever.
func TestConsumer_NonTargetEvent_Acks(t *testing.T) {
	h := consumer.New(func() *consumer.Deps { return nil }, nil)
	if _, err := h.HandleEvent(context.Background(), &pluginv1.HandleEventRequest{
		EventName: "some.other.event",
	}); err != nil {
		t.Fatalf("foreign event must be acked, not nacked; got err=%v", err)
	}
}
