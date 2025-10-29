// Package scraper provides the core web scraping functionality with a hybrid approach:
// HTTP-first scraping with browser automation fallback. It includes smart content
// extraction, image processing, and Cloudflare detection capabilities.
package scraper

import (
	"context"
	"fmt"
	"net/url"
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

	// Calculate remaining time budget from context
	remainingTime := calculateRemainingTime(ctx)
	isTimeLimited := remainingTime < 25*time.Second

	// Calculate dynamic timeouts based on remaining budget
	var httpTimeout time.Duration
	var browserTimeout time.Duration

	if isTimeLimited {
		// For gateway requests (< 25s), use conservative timeouts
		// Reserve 4s buffer for: overhead (2s) + extraction (1s) + processing (1s)
		availableTime := remainingTime - 4*time.Second

		// Reduce HTTP timeout to 8s for more conservative budget
		httpTimeout = adjustTimeoutForBudget(8*time.Second, availableTime, 0.38)

		// Browser phase needs: startup (2s) + challenge (6s) + navigation (2s) + extraction (1s) = 11s minimum
		// Reserve 1s overhead between phases
		browserBudget := availableTime - httpTimeout - 1*time.Second

		// Only allocate browser timeout if we have sufficient budget (12s minimum required)
		if browserBudget >= 12*time.Second {
			browserTimeout = adjustTimeoutForBudget(12*time.Second, browserBudget, 0.9)
			fmt.Printf("Time-limited mode: HTTP=%v, Browser=%v (remaining=%v, budget=%v)\n", httpTimeout, browserTimeout, remainingTime, browserBudget)
		} else {
			// Insufficient budget for browser phase
			browserTimeout = 0
			fmt.Printf("Time-limited mode: HTTP=%v, Browser=SKIPPED (insufficient budget: %v, need 12s)\n", httpTimeout, browserBudget)
		}
	} else {
		// For direct requests, use full timeouts
		httpTimeout = HTTPTimeout
		browserTimeout = BrowserTimeout
	}

	// Phase 1: Try HTTP fetching with alternate URLs
	httpCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	html, finalURL, err := s.httpClient.FetchWithAlternatesGroup(httpCtx, targetURL)
	if err == nil {
		// Success with HTTP - extract content
		result := s.extractor.ExtractArticle(html, finalURL)
		return result, nil
	}

	// Check remaining time before entering browser phase
	remainingAfterHTTP := calculateRemainingTime(ctx)
	if isTimeLimited {
		// Need at least 12s for browser phase (challenge 6s + operation 6s)
		if remainingAfterHTTP < 12*time.Second {
			fmt.Printf("Skipping browser phase: only %v remaining (need 12s for challenge+operation)\n", remainingAfterHTTP)
			return models.ScrapeResponse{}, fmt.Errorf("scraping failed: insufficient time remaining")
		}

		// Double-check browser timeout was allocated
		if browserTimeout == 0 || browserTimeout < 6*time.Second {
			fmt.Printf("Skipping browser phase: timeout too short (%v)\n", browserTimeout)
			return models.ScrapeResponse{}, fmt.Errorf("scraping failed: insufficient time remaining")
		}

		fmt.Printf("Entering browser phase: %v remaining, timeout=%v\n", remainingAfterHTTP, browserTimeout)
	}

	// Phase 2: Browser fallback
	browserCtx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()

	html, finalURL, err = s.browserClient.ScrapeWithBrowserOptimized(browserCtx, targetURL, int(browserTimeout.Milliseconds()))
	if err == nil {
		// Success with browser - extract content
		result := s.extractor.ExtractArticle(html, finalURL)
		return result, nil
	}

	// Check if it's a Cloudflare block
	if IsCloudflareBlock(err) {
		domain, _ := url.Parse(targetURL)
		return models.ScrapeResponse{
				Images: []string{},
			}, &models.CloudflareBlockError{
				Domain: domain.Hostname(),
				Err:    err,
			}
	}

	return models.ScrapeResponse{}, fmt.Errorf("scraping failed: %w", err)
}

// ScrapeSmartWithTimeout runs ScrapeSmart with a timeout
func (s *Scraper) ScrapeSmartWithTimeout(ctx context.Context, targetURL string, timeoutMs int) (models.ScrapeResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	return s.ScrapeSmart(ctx, targetURL)
}
