package ebookdb

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExternalSearch hits EbookDB's wrapper around Anna's Archive.
func (c *Client) ExternalSearch(ctx context.Context, query string, limit int) ([]ExternalSearchHit, error) {
	body, err := json.Marshal(map[string]any{"q": query, "limit": limit})
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	respBody, err := c.PostJSON(ctx, "/api/v1/external_search", body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Items []ExternalSearchHit `json:"items"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("decode external_search: %w", err)
	}
	return env.Items, nil
}
