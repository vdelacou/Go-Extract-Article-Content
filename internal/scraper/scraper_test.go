package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"extract-html-scraper/internal/models"
)

func TestBrowserResultBetter(t *testing.T) {
	body := strings.Repeat("The robot sparred without teleoperation. ", 40)
	scored := models.Quality{Score: 80}
	cases := []struct {
		name     string
		phase1   models.ScrapeResponse
		browser  models.ScrapeResponse
		docTitle string // the browser document's <title>
		want     bool
	}{
		{
			name:    "browser found the body of the same story",
			phase1:  models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model"},
			browser: models.ScrapeResponse{Title: "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model - Pandaily", Content: body, Quality: scored},
			want:    true,
		},
		{
			name:    "browser landed on the site's not-found page",
			phase1:  models.ScrapeResponse{Title: "AgiBot Unveils GE-Act 2.0 Native World-Action Model"},
			browser: models.ScrapeResponse{Title: "Pandaily - China Tech News, AI & Electric Vehicle Insights", Content: body, Quality: scored},
			want:    false,
		},
		{
			name:    "browser text that scores 0 is not article text",
			phase1:  models.ScrapeResponse{Title: "Story"},
			browser: models.ScrapeResponse{Title: "Story", Content: "Menu"},
			want:    false,
		},
		{
			name:    "short browser text beats none",
			phase1:  models.ScrapeResponse{Title: "Story"},
			browser: models.ScrapeResponse{Title: "Story", Content: "A short brief on the story.", Quality: models.Quality{Score: 20}},
			want:    true,
		},
		{
			name:    "browser text is not clearly longer than Phase 1's",
			phase1:  models.ScrapeResponse{Title: "Story", Content: body[:400], Quality: scored},
			browser: models.ScrapeResponse{Title: "Story", Content: body[:700], Quality: scored},
			want:    false,
		},
		{
			name:    "Chinese titles compare by character",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期 | 极客公园", Content: body, Quality: scored},
			want:    true,
		},
		{
			name:    "different Chinese story",
			phase1:  models.ScrapeResponse{Title: "李彦宏的长期主义，进入回报周期"},
			browser: models.ScrapeResponse{Title: "页面不存在", Content: body, Quality: scored},
			want:    false,
		},
		{
			name:     "site-name title from the server, headline set in the browser",
			phase1:   models.ScrapeResponse{Title: "City Times"},
			browser:  models.ScrapeResponse{Title: "Council approves protected bike lanes on Main Street", Content: body, Quality: scored},
			docTitle: "Council approves protected bike lanes on Main Street | City Times",
			want:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserResultBetter(tc.phase1, tc.browser, tc.docTitle); got != tc.want {
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

// shortStaticPage is a brief served whole by the server
func shortStaticPage(title string) string {
	return `<html><head><meta property="og:title" content="` + title + `" /></head><body>
		<nav><a href="/">Home</a> <a href="/tech">Tech</a> <a href="/ev">EV</a></nav>
		<article><p>The robot sparred for three rounds without remote control, the company said on Monday.</p></article>
		<footer>Copyright 2026 Example News. All rights reserved.</footer></body></html>`
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
			name:        "short static article never starts the browser",
			fetcher:     fakeFetcher{html: shortStaticPage(story)},
			renderer:    &fakeRenderer{html: articlePage(story)},
			wantTitle:   story,
			wantBrowser: 0,
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

func TestLooksClientRendered(t *testing.T) {
	pandaily := readPandailyFixture(t, "unitree-g1-200.html")
	cases := []struct {
		name string
		page string
		want bool
	}{
		{"Remix page with an empty body", pandaily, true},
		{"empty mount node", thinPage("Story"), true},
		{"Next.js payload", `<html><body><nav>Home News Sport Weather and many more sections</nav><script id="__NEXT_DATA__" type="application/json">{}</script></body></html>`, true},
		{"short static article", shortStaticPage("Story"), false},
		{"full article", articlePage("Story"), false},
	}
	for _, tc := range cases {
		if got := looksClientRendered(tc.page); got != tc.want {
			t.Errorf("%s: looksClientRendered = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHasArticleText(t *testing.T) {
	cases := []struct {
		name   string
		result models.ScrapeResponse
		want   bool
	}{
		// pandaily: title and images from the static HTML, body rendered client-side
		{"title only", models.ScrapeResponse{Title: "Meituan LongCat 2.5 preview"}, false},
		{"text scored 0", models.ScrapeResponse{Content: "Menu", Quality: models.Quality{Score: 0}}, false},
		{"article", models.ScrapeResponse{Content: "Meituan released a preview.", Quality: models.Quality{Score: 60}}, true},
	}

	for _, c := range cases {
		if got := hasArticleText(c.result); got != c.want {
			t.Errorf("%s: hasArticleText = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsChallengeResult(t *testing.T) {
	// tvinsider as the browser returned it, with a quality score of 45
	interstitial := models.ScrapeResponse{
		Title:   "www.tvinsider.com",
		Content: "Performing security verification\n\nThis website uses a security service to protect against malicious bots. This page is displayed while the website verifies you are not a bot.",
	}
	if !isChallengeResult(interstitial) {
		t.Error("expected the verification interstitial to be recognised")
	}

	article := models.ScrapeResponse{
		Title:   "Why sites ask you to verify you are human",
		Content: strings.Repeat("Bot checks ask visitors to verify you are human before a page loads. ", 20),
	}
	if isChallengeResult(article) {
		t.Error("an article quoting challenge wording is not a challenge")
	}

	// A block page's text, with no Cloudflare markup left to match
	block := models.ScrapeResponse{Content: "Sorry, you have been blocked\nCloudflare Ray ID: 8c1f2e3d4a5b6c7d"}
	if !isChallengeResult(block) {
		t.Error("expected the block page text to be recognised")
	}
	if !isChallengeResult(models.ScrapeResponse{Title: "Just a moment..."}) {
		t.Error("expected the challenge title to be recognised")
	}
}

func TestWithContentFlagAlwaysEmitsContent(t *testing.T) {
	missing, err := json.Marshal(withContentFlag(models.ScrapeResponse{Title: "Only a title"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"content":""`, `"contentMissing":true`} {
		if !strings.Contains(string(missing), want) {
			t.Errorf("expected %s in %s", want, missing)
		}
	}

	present, err := json.Marshal(withContentFlag(models.ScrapeResponse{Content: "Body text."}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(present), "contentMissing") {
		t.Errorf("contentMissing should be omitted when text exists: %s", present)
	}
}
