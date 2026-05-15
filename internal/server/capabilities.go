package server

import (
	"encoding/json"
	"net/http"
)

type capabilitiesResponse struct {
	Formats                []string `json:"formats"`
	Features               []string `json:"features"`
	MaxConcurrentDownloads int      `json:"max_concurrent_downloads"`
	SupportsRangeRequests  bool     `json:"supports_range_requests"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	resp := capabilitiesResponse{
		Formats:                []string{"epub", "pdf", "mobi", "azw3", "azw", "djvu", "fb2", "cbz", "cbr"},
		Features:               []string{"download_provider", "external_search", "request_snapshot", "admin_diagnostics", "provider_test_search", "supports_comics"},
		MaxConcurrentDownloads: 2,
		SupportsRangeRequests:  false,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
