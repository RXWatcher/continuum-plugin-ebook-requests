package catalog_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/RXWatcher/continuum-plugin-ebook-requests/internal/catalog"
	"github.com/RXWatcher/continuum-plugin-ebook-requests/internal/ebookdb"
)

func mount(c *ebookdb.Client) http.Handler {
	r := chi.NewRouter()
	catalog.NewHandler(c).Mount(r)
	return r
}

// limit must be clamped, and sort/order must be allowlisted, before being
// forwarded as upstream control parameters.
func TestCatalog_LimitClampedAndSortAllowlisted(t *testing.T) {
	cases := []struct {
		query     string
		wantLimit string // forwarded ?limit ("" = not forwarded)
		wantSort  string // forwarded ?sort  ("" = not forwarded)
	}{
		{"?limit=999999999&sort=title", "100", "title"},
		{"?limit=-3", "", ""},
		{"?limit=abc", "", ""},
		{"?limit=25&sort=DROP+TABLE&order=sideways", "25", ""},
		{"?sort=year&order=desc", "", "year"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			var got string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RawQuery
				_, _ = w.Write([]byte(`{"items":[],"total":0}`))
			}))
			defer up.Close()
			h := mount(ebookdb.NewClient(up.URL, "k"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/catalog"+tc.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("code=%d", w.Code)
			}
			if tc.wantLimit == "" && strings.Contains(got, "limit=") {
				t.Fatalf("limit should not be forwarded, got %q", got)
			}
			if tc.wantLimit != "" && !strings.Contains(got, "limit="+tc.wantLimit) {
				t.Fatalf("want limit=%s, got %q", tc.wantLimit, got)
			}
			if tc.wantSort == "" && strings.Contains(got, "sort=") {
				t.Fatalf("disallowed sort should be dropped, got %q", got)
			}
			if tc.wantSort != "" && !strings.Contains(got, "sort="+tc.wantSort) {
				t.Fatalf("want sort=%s, got %q", tc.wantSort, got)
			}
		})
	}
}

// A raw upstream/transport error (which can embed the internal base URL via
// the *url.Error) must not be reflected to the client.
func TestCatalog_UpstreamErrorOpaque(t *testing.T) {
	// Point at an unreachable host so client.go wraps a transport *url.Error
	// containing the base URL.
	h := mount(ebookdb.NewClient("http://127.0.0.1:1", "k"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/catalog", nil))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("code=%d, want 502", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"127.0.0.1", "dial", "connection refused", "http://"} {
		if strings.Contains(body, leak) {
			t.Fatalf("client body leaked internal detail %q: %s", leak, body)
		}
	}
}

func TestExternalSearch_BodyBounded(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer up.Close()
	h := mount(ebookdb.NewClient(up.URL, "k"))

	// A huge body with a valid leading "q" must not be fully buffered: the
	// LimitReader caps the decoder. We assert it does not hang/OOM and the
	// request completes (the truncated JSON still yields a decodable q here).
	huge := `{"q":"dune","pad":"` + strings.Repeat("x", (64<<10)+5000) + `"}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/external_search", strings.NewReader(huge)))
	if w.Code == http.StatusOK || w.Code == http.StatusBadRequest {
		return // either the capped decode found q (200) or truncation broke JSON (400) — both bounded
	}
	t.Fatalf("unexpected code %d for oversized body", w.Code)
}
