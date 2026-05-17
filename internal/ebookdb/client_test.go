package ebookdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

// A broken/hostile upstream can return a huge error body. It must not be
// inlined whole into the error string (it propagates into logs / responses).
func TestClient_Get_TruncatesErrorBody(t *testing.T) {
	big := strings.Repeat("x", 50000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	_, err := c.Get(context.Background(), "/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 1024 {
		t.Errorf("error not truncated: %d bytes", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status: %q", err.Error())
	}
}

func TestClient_SendsAPIKeyHeader(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "secret-key")
	if _, err := c.Get(context.Background(), "/api/v1/ping"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("X-API-Key = %q", gotKey)
	}
}

func TestClient_ListBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"md5-a","title":"A"}],"total":1}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	out, _ := c.ListBooks(context.Background(), ebookdb.ListParams{Limit: 10})
	if len(out.Items) != 1 || out.Items[0].ID != "md5-a" {
		t.Errorf("got %+v", out)
	}
}

func TestClient_GetBook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"md5-a","title":"A","files":[{"format":"epub","file_size":1000}]}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	d, _ := c.GetBook(context.Background(), "md5-a")
	if d.ID != "md5-a" || len(d.Files) != 1 {
		t.Errorf("got %+v", d)
	}
}

func TestClient_ExternalSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/external_search" || r.Method != "POST" {
			t.Errorf("expected POST /api/v1/external_search; got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["q"] != "weir" {
			t.Errorf("q = %v", body["q"])
		}
		_, _ = w.Write([]byte(`{"items":[{"source_id":"md5-x","source":"anna","title":"X"}]}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	hits, _ := c.ExternalSearch(context.Background(), "weir", 5)
	if len(hits) != 1 || hits[0].SourceID != "md5-x" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestClient_StartDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/downloads/start" || r.Method != "POST" {
			t.Errorf("path/method = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"job-1","status":"queued"}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	resp, _ := c.StartDownload(context.Background(), "md5-x", "epub")
	if resp.ID != "job-1" {
		t.Errorf("got %+v", resp)
	}
}

func TestClient_GetDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"job-1","status":"imported","book_id":"md5-x"}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	resp, _ := c.GetDownload(context.Background(), "job-1")
	if resp.Status != "imported" || resp.BookID != "md5-x" {
		t.Errorf("got %+v", resp)
	}
}
