package ebookdb

import (
	"context"
	"encoding/json"
	"fmt"
)

// StartDownload queues a download for the given Anna's Archive md5.
func (c *Client) StartDownload(ctx context.Context, sourceID, formatPref string) (DownloadResponse, error) {
	body, err := json.Marshal(map[string]any{
		"source_id":   sourceID,
		"format_pref": formatPref,
	})
	if err != nil {
		return DownloadResponse{}, fmt.Errorf("encode: %w", err)
	}
	respBody, err := c.PostJSON(ctx, "/api/v1/downloads/start", body)
	if err != nil {
		return DownloadResponse{}, err
	}
	var out DownloadResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return DownloadResponse{}, fmt.Errorf("decode start_download: %w", err)
	}
	return out, nil
}

// GetDownload returns the status of an in-flight or completed download job.
func (c *Client) GetDownload(ctx context.Context, jobID string) (DownloadResponse, error) {
	respBody, err := c.Get(ctx, "/api/v1/downloads/"+jobID)
	if err != nil {
		return DownloadResponse{}, err
	}
	var out DownloadResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return DownloadResponse{}, fmt.Errorf("decode download snapshot: %w", err)
	}
	return out, nil
}
