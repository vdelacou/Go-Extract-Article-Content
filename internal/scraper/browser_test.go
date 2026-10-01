package scraper

import "testing"

func TestGetBestHTMLSkipsChromeErrorPages(t *testing.T) {
	b := NewBrowserClient()
	snapshots := []HTMLSnapshot{
		{HTML: "<html><body><article>Story</article></body></html>", URL: "https://example.com/story", Stage: "initial", Length: 50},
		{HTML: "<html><body>This site can't be reached</body></html>", URL: "chrome-error://chromewebdata/", Stage: "stable", Length: 183625},
	}

	best := b.getBestHTML(snapshots)
	if best == nil || best.URL != "https://example.com/story" {
		t.Fatalf("expected the site's snapshot, got %+v", best)
	}

	if best := b.getBestHTML(snapshots[1:]); best != nil {
		t.Fatalf("expected no snapshot when only Chrome's error page was captured, got %+v", best)
	}
}
