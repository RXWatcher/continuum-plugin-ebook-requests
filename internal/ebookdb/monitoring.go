package ebookdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// MonitoringRequest is the payload for the upstream POST /api/v1/monitoring/add.
// The upstream requires either an ISBN or a search_result, so we always send a
// search_result built from the portal metadata (valid even without an ISBN)
// and additionally include the ISBN at the top level when present.
type MonitoringRequest struct {
	Title      string
	Authors    []string
	ISBN       string
	FormatPref string
}

// MonitoringResponse is the normalized upstream response. POST
// /api/v1/monitoring/add returns {request_id,status}; GET
// /api/v1/monitoring/{id} returns {id,status,book_id,...}.
type MonitoringResponse struct {
	ID     string
	Status string
	BookID string
}

// AddMonitoring requests a download/monitor. The upstream searches Anna's
// Archive itself from the supplied metadata (it has no md5-direct endpoint).
func (c *Client) AddMonitoring(ctx context.Context, r MonitoringRequest) (MonitoringResponse, error) {
	format := r.FormatPref
	if format == "" {
		format = "epub"
	}
	searchResult := map[string]any{
		"title":  r.Title,
		"source": "continuum",
	}
	if len(r.Authors) > 0 {
		searchResult["authors"] = r.Authors
	}
	if r.ISBN != "" {
		searchResult["isbn13"] = r.ISBN
	}
	payload := map[string]any{
		"search_result":    searchResult,
		"preferred_format": format,
	}
	if r.ISBN != "" {
		payload["isbn"] = r.ISBN
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return MonitoringResponse{}, fmt.Errorf("encode: %w", err)
	}
	respBody, err := c.PostJSON(ctx, "/api/v1/monitoring/add", body)
	if err != nil {
		return MonitoringResponse{}, err
	}
	return decodeMonitoring(respBody, "monitoring/add")
}

// GetMonitoring returns the current status of a monitoring/download request.
func (c *Client) GetMonitoring(ctx context.Context, requestID string) (MonitoringResponse, error) {
	respBody, err := c.Get(ctx, "/api/v1/monitoring/"+url.PathEscape(requestID))
	if err != nil {
		return MonitoringResponse{}, err
	}
	return decodeMonitoring(respBody, "monitoring snapshot")
}

func decodeMonitoring(b []byte, what string) (MonitoringResponse, error) {
	var raw struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
		Status    string `json:"status"`
		BookID    string `json:"book_id"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return MonitoringResponse{}, fmt.Errorf("decode %s: %w", what, err)
	}
	id := raw.RequestID
	if id == "" {
		id = raw.ID
	}
	// json.Unmarshal does not error on a 2xx body that simply lacks the
	// expected fields ({}, {"error":"not found"}, ...). Returning an empty
	// response with a nil error would make the reconciler treat the poll as
	// successful, hold the row's status forever, and clear any sticky
	// error_text — silently masking a lost/forgotten upstream request. A
	// valid monitoring response always carries an id or a status.
	if id == "" && raw.Status == "" {
		return MonitoringResponse{}, fmt.Errorf("decode %s: invalid monitoring response: %s", what, truncForError(b))
	}
	return MonitoringResponse{ID: id, Status: raw.Status, BookID: raw.BookID}, nil
}
