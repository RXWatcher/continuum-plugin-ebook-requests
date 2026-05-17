package catalog_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/books":
			_, _ = w.Write([]byte(`{"items":[{"id":"md5-a","title":"A","formats":["epub"]}],"total":1}`))
		case "/api/v1/books/md5-a":
			_, _ = w.Write([]byte(`{"id":"md5-a","title":"A","formats":["epub"],"files":[{"format":"epub","file_size":1024}]}`))
		case "/api/v1/external_search":
			_, _ = w.Write([]byte(`{"items":[{"source_id":"md5-x","source":"anna","title":"X"}]}`))
		case "/api/v1/downloads/job-1":
			_, _ = w.Write([]byte(`{"id":"job-1","status":"downloading"}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func newRouter(c *ebookdb.Client) *chi.Mux {
	r := chi.NewRouter()
	catalog.NewHandler(c).Mount(r)
	return r
}

func TestList_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog?limit=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
}

func TestDetail_IncludesFiles(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/catalog/md5-a", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var d catalog.EbookDetail
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.ID != "md5-a" || len(d.Files) != 1 || d.Files[0].MimeType != "application/epub+zip" {
		t.Errorf("d = %+v", d)
	}
}

// Upstream EbookDB requires X-API-Key. A 302 to the upstream URL would be
// followed by a browser/streamer that can't send that header -> 401, so
// covers must be stream-proxied with the key, not redirected.
func TestCover_StreamProxiesWithAPIKey(t *testing.T) {
	var gotKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/md5-7/cover/large" {
			t.Errorf("upstream path = %s", r.URL.Path)
		}
		gotKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cover"))
	}))
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/cover/md5-7/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
	if gotKey != "k" {
		t.Errorf("upstream X-API-Key = %q, want k", gotKey)
	}
	if w.Body.String() != "cover" || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("body=%q ct=%q", w.Body.String(), w.Header().Get("Content-Type"))
	}
}

// md5 flows from the URL into the upstream request path; a value with
// path/query metacharacters must be percent-escaped (SSRF / path traversal).
func TestCover_EscapesMD5(t *testing.T) {
	var gotPath, gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte("x"))
	}))
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/cover/a%3Fz/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if gotPath != "/api/v1/books/a?z/cover/large" || gotQuery != "" {
		t.Errorf("upstream path=%q query=%q (md5 not escaped)", gotPath, gotQuery)
	}
}

// File must stream-proxy with the API key and forward the client Range so
// reader/Kindle seek/resume gets a 206 instead of silently re-downloading.
func TestFile_StreamProxiesAndForwardsRange(t *testing.T) {
	var gotKey, gotRange string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/books/md5-7/files/epub" {
			t.Errorf("upstream path = %s", r.URL.Path)
		}
		gotKey = r.Header.Get("X-API-Key")
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("PK04"))
	}))
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/file/md5-7?format=epub", nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if gotKey != "k" {
		t.Errorf("upstream X-API-Key = %q, want k", gotKey)
	}
	if gotRange != "bytes=0-3" {
		t.Errorf("upstream Range = %q, want bytes=0-3", gotRange)
	}
	if w.Code != http.StatusPartialContent {
		t.Errorf("code = %d, want 206", w.Code)
	}
	if w.Header().Get("Content-Range") != "bytes 0-3/100" || w.Body.String() != "PK04" {
		t.Errorf("Content-Range=%q body=%q", w.Header().Get("Content-Range"), w.Body.String())
	}
}

func TestExternalSearch_Returns200(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("POST", "/external_search", strings.NewReader(`{"q":"weir"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code = %d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestSnapshot(t *testing.T) {
	up := upstream(t)
	defer up.Close()
	c := ebookdb.NewClient(up.URL, "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/requests/job-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "downloading" {
		t.Errorf("status = %v", body["status"])
	}
}
