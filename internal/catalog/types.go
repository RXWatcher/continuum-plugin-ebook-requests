// Package catalog defines the ebook_backend.v1 contract response types and
// the upstream→contract translator.
package catalog

type EbookSummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex float64  `json:"series_index,omitempty"`
	Year        int      `json:"year,omitempty"`
	Language    string   `json:"language,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
	HasCover    bool     `json:"has_cover"`
	Rating      float64  `json:"rating,omitempty"`
	Formats     []string `json:"formats"`
}

type EbookFile struct {
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
	URL       string `json:"url,omitempty"`
}

type EbookDetail struct {
	EbookSummary
	Description string      `json:"description,omitempty"`
	ISBN        string      `json:"isbn,omitempty"`
	Publisher   string      `json:"publisher,omitempty"`
	Genres      []string    `json:"genres,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Files       []EbookFile `json:"files"`
	UpstreamID  string      `json:"upstream_id,omitempty"`
}

type PageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total,omitempty"`
}
