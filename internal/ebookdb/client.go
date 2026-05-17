// Package ebookdb is a typed HTTP client for the upstream EbookDB
// (Anna's-Archive-style external-fetch) service. Mirrors
// /opt/librarymanagerre/lib/ebookdb/client.ts.
package ebookdb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// maxResponseBytes caps the body read from the upstream EbookDB service.
// Search/detail responses are well under this; the cap defends against
// memory exhaustion if the upstream returns a runaway body.
const maxResponseBytes = 10 << 20 // 10 MiB

type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: defaultTimeout},
	}
}

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Get(ctx, "/api/v1/health")
	if err == nil {
		return nil
	}
	_, err = c.Get(ctx, "/health")
	return err
}

func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// GetStream issues a GET with the API key and returns the response so the
// caller can copy the body without buffering it. Used for cover images and
// ebook files (the upstream requires X-API-Key, so the browser can't follow
// a redirect there — it must be stream-proxied). Caller MUST close resp.Body.
func (c *Client) GetStream(ctx context.Context, path string) (*http.Response, error) {
	return c.GetStreamWithRange(ctx, path, "")
}

// GetStreamWithRange is GetStream that also forwards the caller's Range
// request header so byte-range (seek/resume) requests reach upstream and the
// 206 Partial Content response passes back through. A 416 is returned to the
// caller as a normal response (not an error) so it can be relayed verbatim.
// Caller MUST close resp.Body.
func (c *Client) GetStreamWithRange(ctx context.Context, path, rangeHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) PostJSON(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
