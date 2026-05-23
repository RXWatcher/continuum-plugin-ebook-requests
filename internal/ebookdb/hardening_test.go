package ebookdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RXWatcher/silo-plugin-ebook-requests/internal/ebookdb"
)

// The upstream API key must not be forwarded to a different host when the
// upstream (a content proxy) issues a cross-host redirect.
func TestClient_APIKeyStrippedOnCrossHostRedirect(t *testing.T) {
	var gotKeyOnAttacker string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyOnAttacker = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))
	defer attacker.Close()

	var keptKeySameHost bool
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redir-cross":
			http.Redirect(w, r, attacker.URL+"/", http.StatusFound)
		case "/redir-same":
			if r.Header.Get("X-API-Key") == "secret" {
				keptKeySameHost = true
			}
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		default:
			http.Redirect(w, r, upstream.URL+"/redir-same", http.StatusFound)
		}
	}))
	defer upstream.Close()

	c := ebookdb.NewClient(upstream.URL, "secret")
	// Cross-host redirect: key must be stripped at the attacker host.
	if _, err := c.Get(context.Background(), "/redir-cross"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotKeyOnAttacker != "" {
		t.Fatalf("API key leaked to cross-host redirect target: %q", gotKeyOnAttacker)
	}
	// Same-host redirect: key must still be present (functionality intact).
	if _, err := c.Get(context.Background(), "/start"); err != nil {
		t.Fatalf("get same-host: %v", err)
	}
	if !keptKeySameHost {
		t.Fatal("API key wrongly stripped on a same-host redirect")
	}
}

// A 2xx body missing id+status must be an error, not a silent empty
// response (which the reconciler would treat as a successful poll and use
// to clear sticky error_text on a lost request).
func TestGetMonitoring_RejectsEmptyResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"error":"not found"}`, `{"unrelated":1}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := ebookdb.NewClient(srv.URL, "k")
		if _, err := c.GetMonitoring(context.Background(), "req-1"); err == nil {
			t.Fatalf("body %q: expected error, got nil", body)
		}
		srv.Close()
	}

	// A response with only a status is still valid.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"downloading"}`))
	}))
	defer ok.Close()
	c := ebookdb.NewClient(ok.URL, "k")
	got, err := c.GetMonitoring(context.Background(), "req-1")
	if err != nil || got.Status != "downloading" {
		t.Fatalf("valid status-only response rejected: got=%+v err=%v", got, err)
	}
}
