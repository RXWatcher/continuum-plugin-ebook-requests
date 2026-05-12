package ebookdb

// Book is the upstream summary. EbookDB uses MD5 as `id`.
type Book struct {
	ID          string   `json:"id"` // md5 hash
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	ISBN        string   `json:"isbn,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex float64  `json:"series_index,omitempty"`
	Year        int      `json:"year,omitempty"`
	Language    string   `json:"language,omitempty"`
	CoverURL    string   `json:"cover_url,omitempty"`
	HasCover    bool     `json:"has_cover"`
	Rating      float64  `json:"rating,omitempty"`
	Formats     []string `json:"formats,omitempty"`
}

type BookDetail struct {
	Book
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Files       []File   `json:"files,omitempty"`
}

type File struct {
	Format    string `json:"format"`
	SizeBytes int64  `json:"file_size"`
	URL       string `json:"url,omitempty"`
}

type Paged[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total,omitempty"`
}

// ExternalSearchHit is one result from Anna's Archive aggregator.
type ExternalSearchHit struct {
	SourceID  string   `json:"source_id"` // md5
	Source    string   `json:"source"`    // "anna"
	Title     string   `json:"title"`
	Authors   []string `json:"authors,omitempty"`
	Year      int      `json:"year,omitempty"`
	Language  string   `json:"language,omitempty"`
	Formats   []string `json:"formats,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
	CoverURL  string   `json:"cover_url,omitempty"`
}

// DownloadResponse describes the state of an EbookDB-side download job.
type DownloadResponse struct {
	ID     string `json:"id"`     // EbookDB job id
	Status string `json:"status"` // queued | downloading | imported | failed
	BookID string `json:"book_id,omitempty"`
}
