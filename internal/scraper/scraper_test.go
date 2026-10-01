package scraper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"extract-html-scraper/internal/models"
)

func TestBrowserResultBetter(t *testing.T) {
	body := strings.Repeat("The robot sparred without teleoperation. ", 40)
	cases := []struct {
		name    string
		phase1  models.ScrapeResponse
		browser models.ScrapeResponse
		want    bool
	}{
		{
			name:    "browser found the body of the same story",
			phase1:  models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model"},
			browser: models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model - Pandaily", Content: body},
			want:    true,
		},
		{
			name:    "browser landed on the site's not-found page",
			phase1:  models.ScrapeResponse{Title: "AgiBot Unveils GE-Act 2.0 Native World-Action Model"},
			browser: models.ScrapeResponse{Title: "Pandaily - China Tech News, AI & Electric Vehicle Insights", Content: body},
			want:    false,
		},
		{
			name:    "browser text is still thin",
			phase1:  models.ScrapeResponse{Title: "Story"},
			browser: models.ScrapeResponse{Title: "Story", Content: "Short teaser."},
			want:    false,
		},
		{
			name:    "browser text is not clearly longer",
			phase1:  models.ScrapeResponse{Title: "Story", Content: body[:400]},
			browser: models.ScrapeResponse{Title: "Story", Content: body[:700]},
			want:    false,
		},
		{
			name:    "Chinese titles compare by character",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期 | 极客公园", Content: body},
			want:    true,
		},
		{
			name:    "different Chinese story",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "页面不存在", Content: body},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserResultBetter(tc.phase1, tc.browser); got != tc.want {
				t.Errorf("browserResultBetter = %v, want %v (overlap %.2f)", got, tc.want, titleOverlap(tc.phase1.Title, tc.browser.Title))
			}
		})
	}
}

type fakeFetcher struct {
	html string
	err  error
}

func (f fakeFetcher) FetchWithAlternatesGroup(ctx context.Context, targetURL string) (string, string, error) {
	return f.html, targetURL, f.err
}

type fakeRenderer struct {
	html  string
	err   error
	calls int
}

func (f *fakeRenderer) ScrapeWithBrowserOptimized(ctx context.Context, targetURL string, timeoutMs int) (string, string, error) {
	f.calls++
	return f.html, targetURL, f.err
}

func articlePage(title string) string {
	paragraph := "<p>The humanoid robot sparred with a person for three rounds and adjusted its stance after every exchange, without remote control.</p>"
	return `<html><head><meta property="og:title" content="` + title + `" /></head><body><article>` +
		strings.Repeat(paragraph, 8) + `</article></body></html>`
}

// thinPage is what a client-rendered site serves: metadata and an empty body
func thinPage(title string) string {
	return `<html><head><title>` + title + `</title><meta property="og:title" content="` + title + `" />
		<meta name="description" content="A short summary of the story." /></head><body><div id="root"></div></body></html>`
}

func TestScrapeSmartPhaseDecisions(t *testing.T) {
	const story = "Unitree G1 Sparring Demo Uses UnifoLM-X2 World Model"
	challenged := &models.CloudflareBlockError{Domain: "www.tvinsider.com", Kind: "challenge", Status: 403, Err: errors.New("HTTP 403")}
	challengePage, err := os.ReadFile(filepath.Join("testdata", "cloudflare", "challenge-tvinsider-chrome-dom-rendered.html"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		fetcher      fakeFetcher
		renderer     *fakeRenderer
		wantTitle    string
		wantBody     bool // at least ThinContentChars of content
		wantBrowser  int
		wantErr      bool
		wantCFStatus bool // error is a *models.CloudflareBlockError (HTTP 451)
	}{
		{
			name:        "full article from Phase 1 never starts the browser",
			fetcher:     fakeFetcher{html: articlePage(story)},
			renderer:    &fakeRenderer{},
			wantTitle:   story,
			wantBody:    true,
			wantBrowser: 0,
		},
		{
			name:        "thin Phase 1 takes the browser's body for the same story",
			fetcher:     fakeFetcher{html: thinPage(story)},
			renderer:    &fakeRenderer{html: articlePage(story + " - Pandaily")},
			wantTitle:   story + " - Pandaily",
			wantBody:    true,
			wantBrowser: 1,
		},
		{
			name:        "thin Phase 1 is kept when the browser lands on another page",
			fetcher:     fakeFetcher{html: thinPage(story)},
			renderer:    &fakeRenderer{html: articlePage("Pandaily - China Tech News, AI & Electric Vehicle Insights")},
			wantTitle:   story,
			wantBrowser: 1,
		},
		{
			name:        "thin Phase 1 is kept when the browser fails",
			fetcher:     fakeFetcher{html: thinPage(story)},
			renderer:    &fakeRenderer{err: errors.New("navigation failed")},
			wantTitle:   story,
			wantBrowser: 1,
		},
		{
			name:        "Phase 1 challenge and a browser failure is not a 451",
			fetcher:     fakeFetcher{err: challenged},
			renderer:    &fakeRenderer{err: errors.New("navigation failed: processing phase failed")},
			wantBrowser: 1,
			wantErr:     true,
		},
		{
			name:         "browser stuck on the challenge is a 451",
			fetcher:      fakeFetcher{err: challenged},
			renderer:     &fakeRenderer{html: string(challengePage)},
			wantBrowser:  1,
			wantErr:      true,
			wantCFStatus: true,
		},
		{
			name:        "browser past a Phase 1 challenge returns the article",
			fetcher:     fakeFetcher{err: challenged},
			renderer:    &fakeRenderer{html: articlePage(story)},
			wantTitle:   story,
			wantBody:    true,
			wantBrowser: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Scraper{httpClient: tc.fetcher, browserClient: tc.renderer, extractor: NewArticleExtractor()}
			got, err := s.ScrapeSmartWithTimeout(context.Background(), "https://pandaily.com/unitree-g1", 60000)

			if tc.renderer.calls != tc.wantBrowser {
				t.Errorf("browser called %d times, want %d", tc.renderer.calls, tc.wantBrowser)
			}
			var cf *models.CloudflareBlockError
			if isCF := errors.As(err, &cf); isCF != tc.wantCFStatus {
				t.Errorf("Cloudflare error = %v, want %v (err: %v)", isCF, tc.wantCFStatus, err)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if got.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tc.wantTitle)
			}
			if hasBody := len(got.Content) >= ThinContentChars; hasBody != tc.wantBody {
				t.Errorf("content is %d chars, want body %v", len(got.Content), tc.wantBody)
			}
		})
	}
}
