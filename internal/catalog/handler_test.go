package catalog_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/ebookdb"
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

func TestCover_Redirects302(t *testing.T) {
	c := ebookdb.NewClient("https://up.example", "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/cover/md5-7/large", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestFile_Redirects302(t *testing.T) {
	c := ebookdb.NewClient("https://up.example", "k")
	r := newRouter(c)
	req := httptest.NewRequest("GET", "/file/md5-7?format=epub", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
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
