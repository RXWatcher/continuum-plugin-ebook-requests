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
