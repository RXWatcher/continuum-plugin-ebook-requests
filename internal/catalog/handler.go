package catalog

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

type Handler struct {
	client *ebookdb.Client
}

func NewHandler(c *ebookdb.Client) *Handler { return &Handler{client: c} }

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
		p := ebookdb.ListParams{
			Cursor: r.URL.Query().Get("cursor"),
			Sort:   r.URL.Query().Get("sort"),
			Order:  r.URL.Query().Get("order"),
		}
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				p.Limit = n
			}
		}
		out, err := h.client.ListBooks(r.Context(), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeEnvelope(w, out)
	}
}

func (h *Handler) Search() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		out, err := h.client.ListBooks(r.Context(), ebookdb.ListParams{Query: q})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
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
			http.Error(w, err.Error(), http.StatusBadGateway)
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

func (h *Handler) Cover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		md5 := chi.URLParam(r, "book_id")
		size := chi.URLParam(r, "size")
		http.Redirect(w, r, h.client.CoverURL(md5, size), http.StatusFound)
	}
}

func (h *Handler) File() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		md5 := chi.URLParam(r, "book_id")
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "epub"
		}
		http.Redirect(w, r, h.client.FileURL(md5, format), http.StatusFound)
	}
}

func (h *Handler) ExternalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Q     string `json:"q"`
			Limit int    `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Q == "" {
			http.Error(w, "q required", http.StatusBadRequest)
			return
		}
		hits, err := h.client.ExternalSearch(r.Context(), body.Q, body.Limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
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
		snap, err := h.client.GetDownload(r.Context(), eid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
