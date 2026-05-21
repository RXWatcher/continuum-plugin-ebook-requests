package ebookdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RXWatcher/continuum-plugin-ebook-requests/internal/ebookdb"
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

// Upstream external search is GET /api/v1/search/external returning
// {"results":[ExternalSearchResult]} (metadata; no Anna's md5). The hit's
// stable identifier is the ISBN-13.
func TestClient_ExternalSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search/external" || r.Method != "GET" {
			t.Errorf("expected GET /api/v1/search/external; got %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("q") != "weir" || r.URL.Query().Get("limit") != "5" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"X","authors":["Andy Weir"],"isbn13":"9780593135204","language":"en","published_date":"2021-05-04","cover_url":"http://c/x","source":"openlibrary"}],"total":1}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	hits, err := c.ExternalSearch(context.Background(), "weir", 5)
	if err != nil {
		t.Fatalf("ExternalSearch: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "X" || hits[0].SourceID != "9780593135204" || hits[0].Year != 2021 {
		t.Errorf("hits = %+v", hits)
	}
}

// Requesting a download is POST /api/v1/monitoring/add with a search_result
// (so the upstream's "isbn or search_result" requirement is always met) plus
// preferred_format; the upstream returns {request_id,status}.
func TestClient_AddMonitoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/add" || r.Method != "POST" {
			t.Errorf("path/method = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			SearchResult struct {
				Title   string   `json:"title"`
				Authors []string `json:"authors"`
				ISBN13  string   `json:"isbn13"`
			} `json:"search_result"`
			PreferredFormat string `json:"preferred_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SearchResult.Title != "Project Hail Mary" || body.PreferredFormat != "epub" {
			t.Errorf("body = %+v", body)
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"request_id":"req-9","status":"searching","message":"queued"}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	resp, err := c.AddMonitoring(context.Background(), ebookdb.MonitoringRequest{
		Title: "Project Hail Mary", Authors: []string{"Andy Weir"},
		ISBN: "9780593135204", FormatPref: "epub",
	})
	if err != nil {
		t.Fatalf("AddMonitoring: %v", err)
	}
	if resp.ID != "req-9" || resp.Status != "searching" {
		t.Errorf("got %+v", resp)
	}
}

// Polling is GET /api/v1/monitoring/{id}; the response uses "id" (not
// request_id) and includes book_id when completed.
func TestClient_GetMonitoring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/monitoring/req-9" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"req-9","status":"completed","book_id":"bk-1","md5":"abc"}`))
	}))
	defer srv.Close()
	c := ebookdb.NewClient(srv.URL, "k")
	resp, err := c.GetMonitoring(context.Background(), "req-9")
	if err != nil {
		t.Fatalf("GetMonitoring: %v", err)
	}
	if resp.ID != "req-9" || resp.Status != "completed" || resp.BookID != "bk-1" {
		t.Errorf("got %+v", resp)
	}
}
