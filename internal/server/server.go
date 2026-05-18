// Package server constructs the chi-based HTTP handler.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/runtime"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/store"
)

type Deps struct {
	EbookDBClient *ebookdb.Client
	Store         *store.Store
	Config        runtime.Config
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/admin", s.handleAdminHome)
	r.Get("/admin/", s.handleAdminHome)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		r.Get("/admin/diagnostics", s.handleDiagnostics)
		r.Get("/admin/test-search", s.handleTestSearch)
		if s.deps.EbookDBClient != nil {
			catalog.NewHandler(s.deps.EbookDBClient).Mount(r)
		}
	})
	return r
}

func (s *Server) handleAdminHome(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Anna's Archive Downloader</title></head>
<body style="font-family:system-ui,sans-serif;margin:32px;line-height:1.5;background:#111;color:#eee">
<h1>Anna's Archive Downloader</h1>
<p>Ebook download provider for Anna's Archive style search, request forwarding, and reconciliation.</p>
<ul>
<li><a style="color:#8ab4f8" href="./api/v1/admin/diagnostics">Diagnostics</a></li>
<li><a style="color:#8ab4f8" href="./api/v1/admin/test-search">Test search</a></li>
</ul>
</body></html>`))
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	upstreamOK := false
	upstreamMessage := "not configured"
	if s.deps.EbookDBClient != nil {
		if err := s.deps.EbookDBClient.Ping(ctx); err != nil {
			upstreamMessage = err.Error()
		} else {
			upstreamOK = true
			upstreamMessage = "upstream reachable"
		}
	}
	dbOK := false
	dbMessage := "not configured"
	var stats any = map[string]any{}
	if s.deps.Store != nil {
		if err := s.deps.Store.Pool().Ping(ctx); err != nil {
			dbMessage = err.Error()
		} else {
			dbOK = true
			dbMessage = "database reachable"
		}
		if requestStats, err := s.deps.Store.RequestStats(ctx); err == nil {
			stats = requestStats
		}
	}
	writeJSON(w, 200, map[string]any{
		"plugin_id":  "continuum.annas-archive-downloader",
		"role":       "download_provider",
		"configured": s.deps.Config.Configured(),
		"base_url":   s.deps.Config.BaseURL,
		"features":   []string{"external_search", "request_snapshot", "admin_diagnostics", "provider_test_search"},
		"upstream": map[string]any{
			"ok":      upstreamOK,
			"message": upstreamMessage,
		},
		"database": map[string]any{
			"ok":      dbOK,
			"message": dbMessage,
		},
		"requests": stats,
	})
}

func (s *Server) handleTestSearch(w http.ResponseWriter, r *http.Request) {
	if s.deps.EbookDBClient == nil {
		writeJSON(w, 503, map[string]any{"ok": false, "message": "not configured"})
		return
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "foundation"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	hits, err := s.deps.EbookDBClient.ExternalSearch(ctx, query, 5)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": err.Error(), "items": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "search completed", "items": hits})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
