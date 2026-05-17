package ebookdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ExternalSearch calls the upstream GET /api/v1/search/external (metadata
// search across OpenLibrary / Google Books / ISBNdb). Results carry no
// Anna's-Archive md5 — downloads are requested by metadata/ISBN — so the
// hit's stable identifier is its ISBN-13.
func (c *Client) ExternalSearch(ctx context.Context, query string, limit int) ([]ExternalSearchHit, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	respBody, err := c.Get(ctx, "/api/v1/search/external?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var env struct {
		Results []struct {
			Title         string   `json:"title"`
			Authors       []string `json:"authors"`
			Language      *string  `json:"language"`
			CoverURL      *string  `json:"cover_url"`
			ISBN13        *string  `json:"isbn13"`
			PublishedDate *string  `json:"published_date"`
			Source        string   `json:"source"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("decode external_search: %w", err)
	}
	hits := make([]ExternalSearchHit, 0, len(env.Results))
	for _, r := range env.Results {
		h := ExternalSearchHit{Title: r.Title, Authors: r.Authors, Source: r.Source}
		if r.ISBN13 != nil {
			h.SourceID = *r.ISBN13
		}
		if r.Language != nil {
			h.Language = *r.Language
		}
		if r.CoverURL != nil {
			h.CoverURL = *r.CoverURL
		}
		if r.PublishedDate != nil && len(*r.PublishedDate) >= 4 {
			if y, err := strconv.Atoi((*r.PublishedDate)[:4]); err == nil {
				h.Year = y
			}
		}
		hits = append(hits, h)
	}
	return hits, nil
}
