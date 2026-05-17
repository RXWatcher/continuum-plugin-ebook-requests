package catalog_test

import (
	"testing"

	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/catalog"
	"github.com/ContinuumApp/continuum-plugin-annas-archive-downloader/internal/ebookdb"
)

// The upstream cover endpoint requires X-API-Key, so the cover URL must point
// at this plugin's stream-proxy route (/cover/{id}/{size}), not be the raw
// upstream URL passed through (which the browser can't auth to, and which
// leaks the internal upstream URL).
func TestToSummary_CoverURLIsPortalRelative(t *testing.T) {
	got := catalog.ToSummary(ebookdb.Book{
		ID: "bk1", Title: "T", HasCover: true,
		CoverURL: "https://internal-upstream.example/secret/cover.jpg",
	})
	if got.CoverURL != "/cover/bk1/medium" {
		t.Errorf("CoverURL = %q, want /cover/bk1/medium", got.CoverURL)
	}
	if !got.HasCover {
		t.Errorf("HasCover should be preserved")
	}

	// No cover -> no URL (so the portal renders a placeholder, not a 404).
	none := catalog.ToSummary(ebookdb.Book{ID: "bk2", Title: "T2", HasCover: false})
	if none.CoverURL != "" {
		t.Errorf("CoverURL = %q, want empty when HasCover is false", none.CoverURL)
	}

	// ToDetail inherits the same rule via ToSummary.
	d := catalog.ToDetail(ebookdb.BookDetail{Book: ebookdb.Book{
		ID: "bk3", Title: "T3", HasCover: true, CoverURL: "https://up/x.jpg",
	}})
	if d.CoverURL != "/cover/bk3/medium" {
		t.Errorf("ToDetail CoverURL = %q, want /cover/bk3/medium", d.CoverURL)
	}
}
