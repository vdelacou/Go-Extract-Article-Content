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
		// For gateway requests (< 25s), use aggressive timeouts
		// Reserve 1-2s buffer for overhead
		availableTime := remainingTime - 2*time.Second
		httpTimeout = adjustTimeoutForBudget(10*time.Second, availableTime, 0.4)
		// Browser phase gets remaining time after HTTP (with overhead)
		browserBudget := availableTime - httpTimeout - 1*time.Second
		browserTimeout = adjustTimeoutForBudget(18*time.Second, browserBudget, 0.7)
		fmt.Printf("Time-limited mode: HTTP=%v, Browser=%v (remaining=%v)\n", httpTimeout, browserTimeout, remainingTime)
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
	if isTimeLimited && remainingAfterHTTP < 5*time.Second {
		// Insufficient time for browser phase, skip it
		fmt.Printf("Skipping browser phase: only %v remaining\n", remainingAfterHTTP)
		return models.ScrapeResponse{}, fmt.Errorf("scraping failed: insufficient time remaining")
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
