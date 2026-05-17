package catalog

import (
	"strings"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

func ToSummary(b ebookdb.Book) EbookSummary {
	s := EbookSummary{
		ID: b.ID, Title: b.Title,
		Authors: b.Authors, Series: b.Series, SeriesIndex: b.SeriesIndex,
		Year: b.Year, Language: b.Language,
		HasCover: b.HasCover,
		Rating:   b.Rating, Formats: b.Formats,
	}
	// Point covers at this plugin's stream-proxy route, not the raw upstream
	// URL: the upstream cover endpoint requires X-API-Key (the browser can't
	// send it) and passing the upstream URL through also leaks the internal
	// base URL. Empty when there's no cover so the portal shows a placeholder
	// instead of a broken image.
	if b.HasCover {
		s.CoverURL = "/cover/" + b.ID + "/medium"
	}
	return s
}

func ToDetail(d ebookdb.BookDetail) EbookDetail {
	out := EbookDetail{
		EbookSummary: ToSummary(d.Book),
		Description:  d.Description,
		ISBN:         d.ISBN,
		Publisher:    d.Publisher,
		Genres:       d.Genres,
		Tags:         d.Tags,
		UpstreamID:   d.ID,
		Files:        []EbookFile{},
	}
	if len(d.Files) > 0 {
		out.Files = make([]EbookFile, len(d.Files))
		for i, f := range d.Files {
			out.Files[i] = EbookFile{
				Format: f.Format, SizeBytes: f.SizeBytes,
				MimeType: FormatToMime(f.Format), URL: f.URL,
			}
		}
	}
	return out
}

func FormatToMime(format string) string {
	switch strings.ToLower(format) {
	case "epub":
		return "application/epub+zip"
	case "pdf":
		return "application/pdf"
	case "mobi":
		return "application/x-mobipocket-ebook"
	case "azw", "azw3":
		return "application/vnd.amazon.ebook"
	case "djvu":
		return "image/vnd.djvu"
	case "fb2":
		return "application/x-fictionbook+xml"
	case "cbz":
		return "application/vnd.comicbook+zip"
	case "cbr":
		return "application/vnd.comicbook-rar"
	case "txt":
		return "text/plain"
	}
	return "application/octet-stream"
}
