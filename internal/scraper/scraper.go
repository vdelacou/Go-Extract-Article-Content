// Package scraper provides the core web scraping functionality with a hybrid approach:
// HTTP-first scraping with browser automation fallback. It includes smart content
// extraction, image processing, and Cloudflare detection capabilities.
package scraper

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

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
					return result, nil
				}
			}
		}

		remainingAfterPhase1 := calculateRemainingTime(ctx)
		fmt.Printf("Phase 1: HTTP fetch failed for %s: %v (consumed: %v, remaining: %v)\n", targetURL, err, phase1Duration, remainingAfterPhase1)

		// Check if parent context expired during Phase 1
		if ctx.Err() != nil {
			return models.ScrapeResponse{}, fmt.Errorf("scraping failed: parent context expired during HTTP phase: %w", ctx.Err())
		}
	}

	// Phase 2: Browser fallback
	// Recalculate remaining time after Phase 1
	remainingTime = calculateRemainingTime(ctx)

	// Adjust browser timeout based on remaining budget (leave 5s buffer for cleanup)
	buffer := 5 * time.Second
	maxBrowserTime := remainingTime - buffer
	if maxBrowserTime < 1*time.Second {
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
			return result, nil
		}
	}

	remainingAfterPhase2 := calculateRemainingTime(ctx)
	fmt.Printf("Phase 2: Browser scraping failed for %s: %v (consumed: %v, remaining: %v)\n", targetURL, err, phase2Duration, remainingAfterPhase2)

	// Check if parent context expired during Phase 2
	if ctx.Err() != nil {
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed: parent context expired during browser phase: %w", ctx.Err())
	}

	// Check if it's a Cloudflare block
	if IsCloudflareBlock(err) {
		domain, _ := url.Parse(targetURL)
		fmt.Printf("Detected Cloudflare block for domain: %s\n", domain.Hostname())
		return models.ScrapeResponse{
				Images: []models.Image{},
			}, &models.CloudflareBlockError{
				Domain: domain.Hostname(),
				Err:    err,
			}
	}

	// Combine errors from both phases for better context
	return models.ScrapeResponse{}, fmt.Errorf("scraping failed - HTTP phase failed, browser phase also failed: %w", err)
}

// ScrapeSmartWithTimeout runs ScrapeSmart with a timeout
func (s *Scraper) ScrapeSmartWithTimeout(ctx context.Context, targetURL string, timeoutMs int) (models.ScrapeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	return s.ScrapeSmart(ctx, targetURL)
}
