// Package scraper provides the core web scraping functionality with a hybrid approach:
// HTTP-first scraping with browser automation fallback. It includes smart content
// extraction, image processing, and Cloudflare detection capabilities.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"extract-html-scraper/internal/models"

	"github.com/PuerkitoBio/goquery"
)

// pageFetcher is the Phase 1 HTTP client
type pageFetcher interface {
	FetchWithAlternatesGroup(ctx context.Context, targetURL string) (string, string, error)
}

// pageRenderer is the Phase 2 browser
type pageRenderer interface {
	ScrapeWithBrowserOptimized(ctx context.Context, targetURL string, timeoutMs int) (string, string, error)
}

// Scraper orchestrates the scraping process with HTTP-first, browser-fallback strategy
type Scraper struct {
	httpClient    pageFetcher
	browserClient pageRenderer
	extractor     *ArticleExtractor
}

func NewScraper() *Scraper {
	return &Scraper{
		httpClient:    NewHTTPClient(),
		browserClient: NewBrowserClient(),
		extractor:     NewArticleExtractor(),
	}
}

// calculateRemainingTime gets the time until context deadline
func calculateRemainingTime(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
		return 0
	}
	// No deadline set, return a large value
	return 300 * time.Second
}

// adjustTimeoutForBudget scales a timeout based on remaining time budget
func adjustTimeoutForBudget(baseTimeout, remainingTime time.Duration, maxPercent float64) time.Duration {
	maxAllowed := time.Duration(float64(remainingTime) * maxPercent)
	if maxAllowed < baseTimeout {
		return maxAllowed
	}
	return baseTimeout
}

// ScrapeSmart implements the hybrid scraping strategy: HTTP first, browser fallback
func (s *Scraper) ScrapeSmart(ctx context.Context, targetURL string) (models.ScrapeResponse, error) {
	// Validate URL
	if _, err := url.Parse(targetURL); err != nil {
		return models.ScrapeResponse{}, fmt.Errorf("invalid URL: %w", err)
	}

	// Add small random delay to avoid rate limiting (100-500ms)
	// This helps when multiple requests hit the same domain
	randomDelay := time.Duration(100+time.Now().UnixNano()%400) * time.Millisecond
	time.Sleep(randomDelay)

	// Calculate remaining time budget from parent context
	remainingTime := calculateRemainingTime(ctx)
	fmt.Printf("Remaining time budget: %v\n", remainingTime)

	// Cloudflare verdict from Phase 1, if any (reported if Phase 2 also fails)
	var phase1CF *models.CloudflareBlockError
	// A Phase 1 result with a title but no article body, kept while the browser tries
	var phase1Result *models.ScrapeResponse

	// Phase 1: Try HTTP fetching with alternate URLs
	// Adjust HTTP timeout based on remaining budget (allow 80% max for HTTP phase)
	httpTimeout := adjustTimeoutForBudget(HTTPTimeout, remainingTime, 0.8)
	if httpTimeout < 1*time.Second {
		fmt.Printf("Phase 1: Skipping HTTP fetch - insufficient time budget (%v)\n", remainingTime)
	} else {
		fmt.Printf("Phase 1: Starting HTTP fetch for %s (timeout: %v, remaining budget: %v)\n", targetURL, httpTimeout, remainingTime)
		httpCtx, cancel := context.WithTimeout(ctx, httpTimeout)

		phase1Start := time.Now()
		html, finalURL, err := s.httpClient.FetchWithAlternatesGroup(httpCtx, targetURL)
		phase1Duration := time.Since(phase1Start)
		cancel()

		if err == nil {
			// Validate HTML has content before extracting
			if len(html) == 0 || len(strings.TrimSpace(html)) < 100 {
				fmt.Printf("Phase 1: HTTP fetch returned empty or minimal HTML (%d bytes), treating as failure\n", len(html))
				// Treat as failure and continue to Phase 2
				err = fmt.Errorf("HTTP fetch returned empty or minimal HTML")
			} else {
				// Success with HTTP - extract content with multiple strategies
				remainingAfterPhase1 := calculateRemainingTime(ctx)
				fmt.Printf("Phase 1: HTTP fetch succeeded for %s (HTML size: %d bytes, consumed: %v, remaining: %v)\n", finalURL, len(html), phase1Duration, remainingAfterPhase1)
				result := s.extractor.ExtractArticleWithMultipleStrategies(html, finalURL)
				switch {
				case len(result.Content) == 0 && len(result.Title) == 0:
					fmt.Printf("Phase 1: All extraction strategies returned empty, treating as failure\n")
					err = fmt.Errorf("content extraction returned empty results")
				case isChallengeResult(result):
					fmt.Printf("Phase 1: Page is a bot-check interstitial, treating as failure\n")
					err = fmt.Errorf("HTTP fetch returned a bot-check page")
				case needsBrowser(result, html) && calculateRemainingTime(ctx) >= thinPageBrowserBudget:
					fmt.Printf("Phase 1: Only %d chars of article text (quality=%d), rendering in the browser\n",
						len(result.Content), result.Quality.Score)
					phase1Result = &result
				default:
					fmt.Printf("Phase 1: Extraction succeeded (strategy worked, title=%d, content=%d)\n",
						len(result.Title), len(result.Content))
					return withContentFlag(result), nil
				}
			}
		}

		if phase1Result == nil {
			remainingAfterPhase1 := calculateRemainingTime(ctx)
			fmt.Printf("Phase 1: HTTP fetch failed for %s: %v (consumed: %v, remaining: %v)\n", targetURL, err, phase1Duration, remainingAfterPhase1)
			errors.As(err, &phase1CF)

			// Check if parent context expired during Phase 1
			if ctx.Err() != nil {
				return models.ScrapeResponse{}, fmt.Errorf("scraping failed: parent context expired during HTTP phase: %w", ctx.Err())
			}
		}
	}

	// Phase 2: Browser fallback
	// Recalculate remaining time after Phase 1
	remainingTime = calculateRemainingTime(ctx)

	// Adjust browser timeout based on remaining budget (leave 5s buffer for cleanup)
	buffer := 5 * time.Second
	maxBrowserTime := remainingTime - buffer
	if maxBrowserTime < 1*time.Second {
		if phase1Result != nil {
			return withContentFlag(*phase1Result), nil
		}
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed: insufficient time budget for browser phase (remaining: %v)", remainingTime)
	}

	browserTimeout := adjustTimeoutForBudget(BrowserTimeout, maxBrowserTime, 1.0)

	fmt.Printf("Phase 2: Starting browser scraping for %s (timeout: %v, remaining budget: %v)\n", targetURL, browserTimeout, remainingTime)
	browserCtx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()

	phase2Start := time.Now()
	html, finalURL, err := s.browserClient.ScrapeWithBrowserOptimized(browserCtx, targetURL, int(browserTimeout.Milliseconds()))
	phase2Duration := time.Since(phase2Start)

	if err == nil {
		// Defense in depth: never hand a challenge/block page to the extractor
		if v := DetectCloudflareHTML(html); v != CFNone {
			u, _ := url.Parse(targetURL)
			err = &models.CloudflareBlockError{Domain: u.Hostname(), Kind: v.String(), Err: fmt.Errorf("browser returned a Cloudflare %s page", v)}
		}
	}

	if err == nil {
		// Success with browser - extract content
		remainingAfterPhase2 := calculateRemainingTime(ctx)
		htmlLength := len(html)
		textLength := len(strings.TrimSpace(html))
		fmt.Printf("Phase 2: Browser scraping succeeded for %s (HTML: %d chars, text: %d chars, consumed: %v, remaining: %v)\n",
			finalURL, htmlLength, textLength, phase2Duration, remainingAfterPhase2)
		result := s.extractor.ExtractArticleWithMultipleStrategies(html, finalURL)
		// Let extraction be the final judge - only reject if both title and content are empty
		switch {
		case len(result.Content) == 0 && len(result.Title) == 0:
			fmt.Printf("Phase 2: All extraction strategies returned empty (title=%d chars, content=%d chars), treating as failure\n",
				len(result.Title), len(result.Content))
			err = fmt.Errorf("content extraction returned empty results")
		case isChallengeResult(result):
			// A bot check whose page carries none of Cloudflare's markers
			fmt.Printf("Phase 2: Browser got a bot-check interstitial instead of the article\n")
			u, _ := url.Parse(targetURL)
			err = &models.CloudflareBlockError{Domain: u.Hostname(), Kind: CFChallenge.String(), Err: errors.New("browser returned bot-check text")}
		default:
			fmt.Printf("Phase 2: Extraction successful (title=%d chars, content=%d chars, quality score=%d)\n",
				len(result.Title), len(result.Content), result.Quality.Score)
			if phase1Result != nil && !browserResultBetter(*phase1Result, result, documentTitle(html)) {
				fmt.Printf("Phase 2: Result is no better than Phase 1's, keeping Phase 1\n")
				return withContentFlag(*phase1Result), nil
			}
			return withContentFlag(result), nil
		}
	}

	if phase1Result != nil {
		// The page itself loaded: its title and images are worth more to the caller than an error
		fmt.Printf("Phase 2: Browser scraping failed (%v), keeping Phase 1 result\n", err)
		return withContentFlag(*phase1Result), nil
	}

	remainingAfterPhase2 := calculateRemainingTime(ctx)
	fmt.Printf("Phase 2: Browser scraping failed for %s: %v (consumed: %v, remaining: %v)\n", targetURL, err, phase2Duration, remainingAfterPhase2)

	// Check if parent context expired during Phase 2
	if ctx.Err() != nil {
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed: parent context expired during browser phase: %w", ctx.Err())
	}

	// 451 only when the browser itself ended on a Cloudflare page. From datacenter
	// IPs Phase 1 is challenged on every request to some sites while the browser
	// gets through, so a Phase 1 challenge says nothing about why Phase 2 failed
	var cfErr *models.CloudflareBlockError
	if errors.As(err, &cfErr) {
		fmt.Printf("Detected Cloudflare %s for domain: %s\n", cfErr.Kind, cfErr.Domain)
		return models.ScrapeResponse{Images: []models.Image{}}, cfErr
	}
	if phase1CF != nil {
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed - HTTP phase was challenged by Cloudflare, browser phase failed: %w", err)
	}

	// Combine errors from both phases for better context
	return models.ScrapeResponse{}, fmt.Errorf("scraping failed - HTTP phase failed, browser phase also failed: %w", err)
}

// thinPageBrowserBudget is the time a page with no article body must have left
// for the browser to be worth trying
const thinPageBrowserBudget = 30 * time.Second

// needsBrowser reports whether a Phase 1 result should be rendered in the
// browser: it has no article text at all, or only a little on a page built
// client-side. A short static article is complete as it is
func needsBrowser(r models.ScrapeResponse, page string) bool {
	if !hasArticleText(r) {
		return true
	}
	return len(r.Content) < ThinContentChars && looksClientRendered(page)
}

// hasArticleText reports whether extraction found body text. A page that yields only a
// title and images renders its article client-side and needs the browser.
func hasArticleText(r models.ScrapeResponse) bool {
	return strings.TrimSpace(r.Content) != "" && r.Quality.Score > 0
}

// interstitialPhrases are the words of bot-check and block pages, Cloudflare's
// and others', as they come out of extraction
var interstitialPhrases = []string{
	"attention required! | cloudflare", "cloudflare ray id", "what can i do to resolve this?",
	"why have i been blocked?", "performance & security by cloudflare", "verifying you are human",
	"verify you are human", "checking your browser", "please wait while we verify",
	"this may take a few seconds", "performing security verification",
	"enable javascript and cookies to continue",
}

// isChallengeResult reports whether extraction returned a bot-check interstitial
// instead of an article, which would otherwise reach callers as a short article.
// Real articles that quote the same phrases are far longer
func isChallengeResult(r models.ScrapeResponse) bool {
	if len(r.Content) >= MaxChallengeContentLen {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.Title), "Just a moment...") {
		return true
	}
	return ContainsAny(r.Title+"\n"+r.Content, interstitialPhrases)
}

// withContentFlag marks a result whose article text is missing, so callers need not
// infer it from an empty content field.
func withContentFlag(r models.ScrapeResponse) models.ScrapeResponse {
	r.ContentMissing = strings.TrimSpace(r.Content) == ""
	return r
}

// browserResultBetter reports whether the browser found the body Phase 1 missed:
// article text where Phase 1 had none, or clearly more of it, under a title about
// the same story. A different title means the browser got an error, 404 or
// interstitial page. The browser document's own <title> counts too:
// client-rendered pages often serve the site name as the title and set the
// headline in the browser
func browserResultBetter(phase1, browser models.ScrapeResponse, browserDocTitle string) bool {
	if !hasArticleText(browser) {
		return false
	}
	if n := len(strings.TrimSpace(phase1.Content)); n > 0 &&
		(len(browser.Content) < ThinContentChars || len(browser.Content) < 2*n) {
		return false
	}
	return titleOverlap(phase1.Title, browser.Title) >= 0.5 || titleOverlap(phase1.Title, browserDocTitle) >= 0.5
}

var (
	titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	// Markers of pages whose article is built in the browser
	clientAppMarkers = []string{"__remixContext", "__reactRouterContext", "__NEXT_DATA__", "self.__next_f", "window.__NUXT__", "ng-version=", "<astro-island", "__sveltekit"}
	emptyMountRe     = regexp.MustCompile(`(?i)<div[^>]+id=["'](?:root|app|__next|__nuxt|svelte)["'][^>]*>\s*</div>`)
)

// documentTitle returns the page's <title> text
func documentTitle(page string) string {
	if m := titleTagRe.FindStringSubmatch(page); m != nil {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return ""
}

// looksClientRendered reports whether a page's article is probably built in
// the browser: a framework payload, an empty mount node, or almost no text.
// A short static article is not, and the browser would only add latency
func looksClientRendered(page string) bool {
	for _, marker := range clientAppMarkers {
		if strings.Contains(page, marker) {
			return true
		}
	}
	if emptyMountRe.MatchString(page) {
		return true
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		return false
	}
	body := doc.Find("body")
	body.Find("script, style, noscript, template").Remove()
	return len(strings.TrimSpace(body.Text())) < 50
}

// titleOverlap is the share of a's words that also appear in b. Each Han
// character counts as a word, since Chinese titles have no spaces
func titleOverlap(a, b string) float64 {
	wordsA, wordsB := titleWords(a), titleWords(b)
	if len(wordsA) == 0 {
		return 1
	}
	shared := 0
	for w := range wordsA {
		if wordsB[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(wordsA))
}

func titleWords(title string) map[string]bool {
	words := map[string]bool{}
	var word []rune
	flush := func() {
		if len(word) > 2 {
			words[string(word)] = true
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			words[string(r)] = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
		default:
			flush()
		}
	}
	flush()
	return words
}

// ScrapeSmartWithTimeout runs ScrapeSmart with a timeout
func (s *Scraper) ScrapeSmartWithTimeout(ctx context.Context, targetURL string, timeoutMs int) (models.ScrapeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	return s.ScrapeSmart(ctx, targetURL)
}
