package scraper

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"extract-html-scraper/internal/config"

	"github.com/chromedp/chromedp"
)

type BrowserClient struct {
	config  config.ScrapeConfig
	regexes map[string]*regexp.Regexp
}

func NewBrowserClient() *BrowserClient {
	cfg := config.DefaultScrapeConfig()
	regexes := config.CompileRegexes()

	return &BrowserClient{
		config:  cfg,
		regexes: regexes,
	}
}

// ScrapeWithBrowser uses chromedp to scrape content with fallback to alternate URLs
func (b *BrowserClient) ScrapeWithBrowser(ctx context.Context, targetURL string, timeoutMs int) (string, string, error) {
	opts := DefaultBrowserOptions()
	opts.UserAgent = b.config.UserAgent
	return b.scrapeWithOptions(ctx, targetURL, timeoutMs, opts)
}

// ScrapeWithBrowserOptimized is an optimized version that blocks more resources
func (b *BrowserClient) ScrapeWithBrowserOptimized(ctx context.Context, targetURL string, timeoutMs int) (string, string, error) {
	opts := OptimizedBrowserOptions()
	opts.UserAgent = b.config.UserAgent
	return b.scrapeWithOptions(ctx, targetURL, timeoutMs, opts)
}

// scrapeWithOptions is the unified scraping function using browser options
func (b *BrowserClient) scrapeWithOptions(ctx context.Context, targetURL string, timeoutMs int, opts BrowserOptions) (string, string, error) {
	// Create a new context with timeout
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Build Chrome options
	chromeOpts := BuildChromeOptions(opts)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, chromeOpts...)
	defer cancel()

	// Create browser context
	ctx, cancel = chromedp.NewContext(allocCtx)
	defer cancel()

	// Set up request blocking
	err := chromedp.Run(ctx, chromedp.Tasks{
		chromedp.ActionFunc(func(ctx context.Context) error {
			return chromedp.Run(ctx, chromedp.Tasks{
				chromedp.Evaluate(GetRequestBlockingScript(opts), nil),
			})
		}),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to set up request blocking: %w", err)
	}

	// Try primary URL first
	html, finalURL, err := b.navigateAndExtract(ctx, targetURL)
	if err == nil && !b.LooksLikeCFBlock(html) {
		return html, finalURL, nil
	}

	// Generate alternate URLs and try them
	alternates, err := b.GenerateAlternateURLs(targetURL)
	if err != nil {
		return "", "", err
	}

	for _, altURL := range alternates {
		html, finalURL, err := b.navigateAndExtract(ctx, altURL)
		if err == nil && !b.LooksLikeCFBlock(html) {
			return html, finalURL, nil
		}
	}

	return "", "", fmt.Errorf("all URLs failed or were blocked by Cloudflare")
}

// navigateAndExtract navigates to a URL and extracts HTML content
func (b *BrowserClient) navigateAndExtract(ctx context.Context, targetURL string) (string, string, error) {
	var html string
	var finalURL string

	// Calculate remaining time to adjust challenge wait
	remainingTime := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		remainingTime = time.Until(deadline)
		if remainingTime < 0 {
			remainingTime = 0
		}
	} else {
		// No deadline, use default values
		remainingTime = 60 * time.Second
	}

	// Adjust challenge wait times based on remaining budget
	var initialSleep time.Duration
	var maxChallengeWait time.Duration

	if remainingTime < 25*time.Second {
		// Time-limited (gateway request): aggressive wait
		initialSleep = 1 * time.Second
		maxChallengeWait = 8 * time.Second
	} else if remainingTime < 60*time.Second {
		// Medium time budget: moderate wait
		initialSleep = 2 * time.Second
		maxChallengeWait = 12 * time.Second
	} else {
		// Full time budget: default wait
		initialSleep = 3 * time.Second
		maxChallengeWait = 15 * time.Second
	}

	// Ensure challenge wait doesn't exceed remaining time (with safety margin)
	if maxChallengeWait > remainingTime-initialSleep-2*time.Second {
		maxChallengeWait = remainingTime - initialSleep - 2*time.Second
		if maxChallengeWait < 1*time.Second {
			maxChallengeWait = 1 * time.Second
		}
	}

	err := chromedp.Run(ctx, chromedp.Tasks{
		// Navigate to the URL
		chromedp.Navigate(targetURL),

		// Wait for network to be idle
		chromedp.WaitReady("body"),

		// Wait for potential challenge to resolve (with timeout)
		chromedp.Sleep(initialSleep),

		// Check if challenge resolved by waiting for content to appear
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Use dynamically calculated challenge wait time
			// But also respect the parent context deadline
			challengeCtx, cancel := context.WithTimeout(ctx, maxChallengeWait)
			defer cancel()

			var previousHTML string
			var challengeDetected bool
			for {
				select {
				case <-ctx.Done():
					// Parent context expired, stop immediately
					return nil
				case <-challengeCtx.Done():
					// Challenge wait timeout - proceed with whatever we have
					return nil
				default:
					var bodyHTML string
					if err := chromedp.Run(ctx, chromedp.Tasks{
						chromedp.OuterHTML("body", &bodyHTML),
					}); err == nil {
						isChallengePage := b.LooksLikeChallengePage(bodyHTML)
						if isChallengePage {
							challengeDetected = true
							previousHTML = bodyHTML
						} else if challengeDetected {
							// Challenge was detected earlier but now it's resolved
							if previousHTML != bodyHTML {
								return nil
							}
						} else {
							// No challenge detected at all, check if content has changed
							if previousHTML != "" && previousHTML != bodyHTML {
								return nil
							}
							previousHTML = bodyHTML
						}
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
		}),

		// Get the final URL after redirects
		chromedp.Location(&finalURL),

		// Get the HTML content
		chromedp.OuterHTML("html", &html),
	})

	if err != nil {
		return "", "", fmt.Errorf("navigation failed: %w", err)
	}

	return html, finalURL, nil
}

// LooksLikeCFBlock checks if HTML content indicates Cloudflare blocking
func (b *BrowserClient) LooksLikeCFBlock(html string) bool {
	htmlLower := strings.ToLower(html)
	return b.regexes["cfBlock"].MatchString(htmlLower)
}

// LooksLikeChallengePage checks if HTML content indicates a challenge page
func (b *BrowserClient) LooksLikeChallengePage(html string) bool {
	htmlLower := strings.ToLower(html)
	challengePatterns := []string{
		"verifying you are human",
		"verify you are human",
		"checking your browser",
		"please wait",
		"this may take a few seconds",
	}
	for _, pattern := range challengePatterns {
		if strings.Contains(htmlLower, pattern) {
			return true
		}
	}
	return false
}

// GenerateAlternateURLs creates alternative URLs for AMP/mobile fallback
func (b *BrowserClient) GenerateAlternateURLs(originalURL string) ([]string, error) {
	// Reuse the same logic from HTTP client
	httpClient := NewHTTPClient()
	return httpClient.GenerateAlternateURLs(originalURL)
}

// navigateAndExtractOptimized uses domcontentloaded for faster loading
func (b *BrowserClient) navigateAndExtractOptimized(ctx context.Context, targetURL string) (string, string, error) {
	return b.navigateAndExtract(ctx, targetURL)
}
