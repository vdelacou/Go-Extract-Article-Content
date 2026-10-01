package scraper

import (
	"regexp"
	"strings"
	"testing"
)

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

func TestBlockedURLPatterns(t *testing.T) {
	patterns := blockedURLPatterns(OptimizedBrowserOptions())
	// CDP patterns use * as the only wildcard
	matches := func(u string) bool {
		for _, p := range patterns {
			re := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, ".*") + "$")
			if re.MatchString(u) {
				return true
			}
		}
		return false
	}

	allowed := []string{
		// A challenge must still be able to run
		"https://www.scmp.com/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1?ray=a43c51e869d487ac",
		"https://challenges.cloudflare.com/turnstile/v0/b/1a2b3c/api.js",
		// Articles from the September list
		"https://www.scmp.com/tech/tech-trends/article/3368168/chinese-ai-chipmaker-hygon-plots-expansion-data-centres-robotics?utm_source=rss_feed",
		"https://www.tvinsider.com/1288260/the-simpsons-season-38-cast-episode-details-premiere-date-trailer-more/",
		"https://pandaily.com/catl-2027-all-solid-state-small-batch",
		"http://www.geekpark.net/news/371040",
	}
	for _, u := range allowed {
		if matches(u) {
			t.Errorf("%s would be blocked", u)
		}
	}

	blocked := []string{
		"https://securepubads.g.doubleclick.net/gampad/ads?iu=/1234",
		"https://ads.adthrive.com/sites/5f3/ads.min.js",
		"https://cms-image.pandaily.com/1/catl_solid_state_fe5363eef2.png",
		"https://www.tvinsider.com/wp-content/fonts/inter.woff2",
	}
	for _, u := range blocked {
		if !matches(u) {
			t.Errorf("%s would not be blocked", u)
		}
	}
}
