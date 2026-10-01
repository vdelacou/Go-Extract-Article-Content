// Package scraper provides the core web scraping functionality with a hybrid approach:
// HTTP-first scraping with browser automation fallback. It includes smart content
// extraction, image processing, and Cloudflare detection capabilities.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"extract-html-scraper/internal/models"
)

// Scraper orchestrates the scraping process with HTTP-first, browser-fallback strategy
type Scraper struct {
	httpClient    *HTTPClient
	browserClient *BrowserClient
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

	// Cloudflare verdict from Phase 1, if any (used to report 451 if Phase 2 also fails)
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
				// Verify extraction found at least title or content
				if len(result.Content) == 0 && len(result.Title) == 0 {
					fmt.Printf("Phase 1: All extraction strategies returned empty, treating as failure\n")
					err = fmt.Errorf("content extraction returned empty results")
				} else {
					fmt.Printf("Phase 1: Extraction succeeded (strategy worked, title=%d, content=%d)\n",
						len(result.Title), len(result.Content))
					if len(result.Content) >= ThinContentChars || calculateRemainingTime(ctx) < thinPageBrowserBudget {
						return result, nil
					}
					// A title without a body: the page is probably built in the browser
					fmt.Printf("Phase 1: Only %d chars of article text, trying the browser\n", len(result.Content))
					phase1Result = &result
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
			return *phase1Result, nil
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
		if len(result.Content) == 0 && len(result.Title) == 0 {
			fmt.Printf("Phase 2: All extraction strategies returned empty (title=%d chars, content=%d chars), treating as failure\n",
				len(result.Title), len(result.Content))
			err = fmt.Errorf("content extraction returned empty results")
		} else {
			fmt.Printf("Phase 2: Extraction successful (title=%d chars, content=%d chars, quality score=%d)\n",
				len(result.Title), len(result.Content), result.Quality.Score)
			if phase1Result != nil && !browserResultBetter(*phase1Result, result) {
				fmt.Printf("Phase 2: Result is no better than Phase 1's, keeping Phase 1\n")
				return *phase1Result, nil
			}
			return result, nil
		}
	}

	if phase1Result != nil {
		fmt.Printf("Phase 2: Browser scraping failed (%v), keeping Phase 1 result\n", err)
		return *phase1Result, nil
	}

	remainingAfterPhase2 := calculateRemainingTime(ctx)
	fmt.Printf("Phase 2: Browser scraping failed for %s: %v (consumed: %v, remaining: %v)\n", targetURL, err, phase2Duration, remainingAfterPhase2)

	// Check if parent context expired during Phase 2
	if ctx.Err() != nil {
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed: parent context expired during browser phase: %w", ctx.Err())
	}

	// Check if it's a Cloudflare block (typed verdict from Phase 2, or from Phase 1 when
	// the browser could not get past it either)
	var cfErr *models.CloudflareBlockError
	if errors.As(err, &cfErr) || phase1CF != nil {
		if cfErr == nil {
			cfErr = &models.CloudflareBlockError{Domain: phase1CF.Domain, Kind: phase1CF.Kind, Status: phase1CF.Status, RayID: phase1CF.RayID, Err: err}
		}
		fmt.Printf("Detected Cloudflare %s for domain: %s\n", cfErr.Kind, cfErr.Domain)
		return models.ScrapeResponse{Images: []models.Image{}}, cfErr
	}

	// Combine errors from both phases for better context
	return models.ScrapeResponse{}, fmt.Errorf("scraping failed - HTTP phase failed, browser phase also failed: %w", err)
}

// thinPageBrowserBudget is the time a page with no article body must have left
// for the browser to be worth trying
const thinPageBrowserBudget = 30 * time.Second

// browserResultBetter reports whether the browser found the body Phase 1 missed:
// clearly more text, under a title about the same story. A different title means
// the browser got an error, 404 or interstitial page
func browserResultBetter(phase1, browser models.ScrapeResponse) bool {
	if len(browser.Content) < ThinContentChars || len(browser.Content) < 2*len(phase1.Content) {
		return false
	}
	return titleOverlap(phase1.Title, browser.Title) >= 0.5
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
