package catalog

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

// maxCatalogLimit caps the page size forwarded upstream. limit is
// attacker-controlled and was passed through verbatim — limit=999999999
// drove a giant upstream fetch that then overran the 10 MiB read cap and
// failed to JSON-decode, turning into endpoint denial.
const maxCatalogLimit = 100

// maxSearchBodyBytes bounds the /external_search request body (a search
// query is never legitimately large).
const maxSearchBodyBytes = 64 << 10

type Handler struct {
	client *ebookdb.Client
}

func NewHandler(c *ebookdb.Client) *Handler { return &Handler{client: c} }

// parseListParams reads, validates and clamps the catalog query parameters.
// sort/order are allowlisted (they are forwarded as upstream control
// parameters) and limit is clamped; cursor is opaque and passed through.
func parseListParams(r *http.Request) ebookdb.ListParams {
	q := r.URL.Query()
	p := ebookdb.ListParams{Cursor: q.Get("cursor")}
	switch s := strings.ToLower(q.Get("sort")); s {
	case "title", "year", "added", "rating", "relevance":
		p.Sort = s
	}
	switch o := strings.ToLower(q.Get("order")); o {
	case "asc", "desc":
		p.Order = o
	}
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		if n > maxCatalogLimit {
			n = maxCatalogLimit
		}
		p.Limit = n
	}
	return p
}

// upstreamError logs the real error (it can embed the internal upstream
// base URL via the transport *url.Error) and returns a generic 502 to the
// client.
func upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("ebookdb upstream error",
		"method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

// Mount installs the ebook_backend.v1 contract routes on the chi router.
// EbookDB doesn't support browse endpoints (no aggregated authors/series/etc.)
// so those return 404 — portal honors capabilities[].features instead.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/catalog", h.List())
	r.Get("/catalog/search", h.Search())
	r.Get("/catalog/{id}", h.Detail())
	r.Get("/cover/{book_id}/{size}", h.Cover())
	r.Get("/file/{book_id}", h.File())
	r.Post("/external_search", h.ExternalSearch())
	r.Get("/requests/{external_id}", h.RequestSnapshot())
}

func (h *Handler) List() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := h.client.ListBooks(r.Context(), parseListParams(r))
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		writeEnvelope(w, out)
	}
}

func (h *Handler) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := parseListParams(r)
		p.Query = r.URL.Query().Get("q")
		out, err := h.client.ListBooks(r.Context(), p)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		writeEnvelope(w, out)
	}
}

func (h *Handler) Detail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		d, err := h.client.GetBook(r.Context(), id)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ToDetail(d))
	}
}

func writeEnvelope(w http.ResponseWriter, p ebookdb.Paged[ebookdb.Book]) {
	out := PageEnvelope[EbookSummary]{
		NextCursor: p.NextCursor,
		Total:      p.Total,
		Items:      make([]EbookSummary, len(p.Items)),
	}
	for i, b := range p.Items {
		out.Items[i] = ToSummary(b)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// proxyStream copies an upstream streamed response (status, selected headers,
// body) to the client. The upstream requires X-API-Key, so a 302 would be
// followed without the header and 401 — we must stream-proxy.
func proxyStream(w http.ResponseWriter, resp *http.Response, headers []string) {
	defer resp.Body.Close()
	for _, k := range headers {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h *Handler) Cover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		md5 := chi.URLParam(r, "book_id")
		size := chi.URLParam(r, "size")
		path := "/api/v1/books/" + url.PathEscape(md5) + "/cover/" + url.PathEscape(size)
		resp, err := h.client.GetStream(r.Context(), path)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		proxyStream(w, resp, []string{"Content-Type", "Content-Length", "ETag", "Cache-Control", "Last-Modified"})
	}
}

func (h *Handler) File() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		md5 := chi.URLParam(r, "book_id")
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "epub"
		}
		path := "/api/v1/books/" + url.PathEscape(md5) + "/files/" + url.PathEscape(format)
		// Forward Range so reader/Kindle seek/resume gets a 206 instead of
		// silently re-downloading the whole file.
		resp, err := h.client.GetStreamWithRange(r.Context(), path, r.Header.Get("Range"))
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		proxyStream(w, resp, []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Range", "ETag", "Cache-Control", "Last-Modified", "Accept-Ranges"})
	}
}

func (h *Handler) ExternalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Q     string `json:"q"`
			Limit int    `json:"limit"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, maxSearchBodyBytes)).Decode(&body)
		if body.Q == "" {
			http.Error(w, "q required", http.StatusBadRequest)
			return
		}
		if body.Limit < 0 {
			body.Limit = 0 // upstream default
		} else if body.Limit > maxCatalogLimit {
			body.Limit = maxCatalogLimit
		}
		hits, err := h.client.ExternalSearch(r.Context(), body.Q, body.Limit)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": hits})
	}
}

func (h *Handler) RequestSnapshot() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eid := chi.URLParam(r, "external_id")
		if eid == "" {
			http.Error(w, "external_id required", http.StatusBadRequest)
			return
		}
		snap, err := h.client.GetMonitoring(r.Context(), eid)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
