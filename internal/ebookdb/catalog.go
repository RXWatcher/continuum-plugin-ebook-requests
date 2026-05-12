package ebookdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

type ListParams struct {
	Cursor string
	Limit  int
	Sort   string
	Order  string
	Query  string
}

func (c *Client) ListBooks(ctx context.Context, p ListParams) (Paged[Book], error) {
	q := url.Values{}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Sort != "" {
		q.Set("sort", p.Sort)
	}
	if p.Order != "" {
		q.Set("order", p.Order)
	}
	path := "/api/v1/books"
	if p.Query != "" {
		q.Set("q", p.Query)
		path = "/api/v1/books/search"
	}
	body, err := c.Get(ctx, path+"?"+q.Encode())
	if err != nil {
		return Paged[Book]{}, err
	}
	var out Paged[Book]
	if err := json.Unmarshal(body, &out); err != nil {
		return Paged[Book]{}, fmt.Errorf("decode books: %w", err)
	}
	return out, nil
}

func (c *Client) GetBook(ctx context.Context, md5 string) (BookDetail, error) {
	body, err := c.Get(ctx, "/api/v1/books/"+url.PathEscape(md5))
	if err != nil {
		return BookDetail{}, err
	}
	var out BookDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return BookDetail{}, fmt.Errorf("decode book: %w", err)
	}
	return out, nil
}

func (c *Client) FileURL(md5, format string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/files/%s", c.baseURL, url.PathEscape(md5), url.PathEscape(format))
}

func (c *Client) CoverURL(md5, size string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/cover/%s", c.baseURL, url.PathEscape(md5), url.PathEscape(size))
}
