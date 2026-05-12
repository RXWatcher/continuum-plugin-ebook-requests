// Package server constructs the chi-based HTTP handler.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-ebookdb/internal/ebookdb"
)

type Deps struct {
	EbookDBClient *ebookdb.Client
}

type Server struct {
	deps Deps
}

func New(d Deps) *Server { return &Server{deps: d} }

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/capabilities", s.handleCapabilities)
		if s.deps.EbookDBClient != nil {
			catalog.NewHandler(s.deps.EbookDBClient).Mount(r)
		}
	})
	return r
}
