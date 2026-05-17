package reconciler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/reconciler"
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

func newReconcilerForTest(t *testing.T, upResp string) (*reconciler.Reconciler, *fakePub, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	pub := &fakePub{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(upResp))
	}))
	t.Cleanup(up.Close)
	ebk := ebookdb.NewClient(up.URL, "k")
	r := reconciler.New(reconciler.Deps{Store: st, Pub: pub, EBK: ebk})
	return r, pub, st
}

func TestReconciler_StatusChange(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"job-1","status":"downloading"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "acknowledged",
		LastPolled: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_status_changed" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
}

func TestReconciler_Imported(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"job-1","status":"imported","book_id":"md5-x"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "downloading", UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_fulfilled" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
	if pub.pubs[0].Payload["fulfilled_book_id"] != "md5-x" {
		t.Errorf("fulfilled_book_id = %v", pub.pubs[0].Payload["fulfilled_book_id"])
	}
}

func TestReconciler_Failed(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"job-1","status":"failed"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "downloading", UpdatedAt: time.Now(),
	})
	_ = r.Tick(context.Background())
	if len(pub.pubs) != 1 || pub.pubs[0].Name != "request_failed" {
		t.Errorf("got pubs = %v", pub.pubs)
	}
}

// An unknown/unmapped upstream status must NOT regress the request to
// "acknowledged" and must NOT emit a status-changed event. Hold the current
// status and just record that we polled.
func TestReconciler_UnknownStatus_HoldsNoEvent(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"job-1","status":"paused"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "downloading",
		LastPolled: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(pub.pubs) != 0 {
		t.Errorf("unknown status must not emit an event; got %v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.Status != "downloading" {
		t.Errorf("status regressed to %q; want held at downloading", row.Status)
	}
	if !row.LastPolled.After(time.Now().Add(-time.Minute)) {
		t.Errorf("last_polled not advanced: %v", row.LastPolled)
	}
}

// A transient upstream failure sets error_text. Once polling succeeds again
// it must be cleared, otherwise it sticks forever and RequestStats.WithErrors
// over-counts permanently.
func TestReconciler_SuccessfulPoll_ClearsStickyError(t *testing.T) {
	r, _, st := newReconcilerForTest(t, `{"id":"job-1","status":"downloading"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "downloading",
		ErrorText: "boom: upstream 503", LastPolled: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	})
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.ErrorText != "" {
		t.Errorf("error_text should be cleared after a successful poll; got %q", row.ErrorText)
	}
	stats, _ := st.RequestStats(context.Background())
	if stats.WithErrors != 0 {
		t.Errorf("WithErrors should be 0 after recovery; got %d", stats.WithErrors)
	}
}

// A cancelled context must short-circuit: no events, no DB writes.
func TestReconciler_CancelledContext_NoProcessing(t *testing.T) {
	r, pub, st := newReconcilerForTest(t, `{"id":"job-1","status":"imported"}`)
	_ = st.UpsertForwardedRequest(context.Background(), store.ForwardedRequest{
		RequestID: "req-1", ExternalID: "job-1", Status: "downloading", UpdatedAt: time.Now(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.Tick(ctx)
	if len(pub.pubs) != 0 {
		t.Errorf("cancelled context must not publish; got %v", pub.pubs)
	}
	row, _ := st.GetForwardedRequest(context.Background(), "req-1")
	if row.Status != "downloading" {
		t.Errorf("cancelled context must not write; status = %q", row.Status)
	}
}
