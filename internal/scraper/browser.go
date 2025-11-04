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
	// Note: CDP unmarshaling errors (ERROR: could not unmarshal event) are harmless warnings
	// These occur when Chrome uses newer protocol features that chromedp doesn't recognize yet.
	// They don't affect functionality - chromedp continues working normally.
	// The errors come from chromedp's internal event handlers and cannot be easily suppressed
	// without modifying the chromedp library itself.
	ctx, cancel = chromedp.NewContext(allocCtx, chromedp.WithLogf(func(string, ...interface{}) {
		// Suppress chromedp's own log messages, though CDP protocol errors will still appear
	}))
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

	// Try primary URL first with graceful degradation
	html, finalURL, err := b.navigateAndExtract(ctx, targetURL)
	if err == nil && len(html) > 0 {
		// Check for blocking first - this is a hard failure
		if b.LooksLikeCFBlock(html) {
			fmt.Printf("Primary URL blocked by site protection\n")
			// Continue to alternates instead of returning error immediately
		} else {
			// Got HTML - return it (let extraction determine validity)
			textLength := len(strings.TrimSpace(html))
			fmt.Printf("Primary URL navigation complete: HTML length=%d chars, finalURL=%s\n", textLength, finalURL)
		return html, finalURL, nil
		}
	} else if err != nil {
		fmt.Printf("Primary URL navigation had errors: %v (HTML length: %d)\n", err, len(html))
		// Even with errors, if we got HTML, try to use it
		if len(html) > 0 && !b.LooksLikeCFBlock(html) {
			fmt.Printf("Using HTML despite navigation errors (graceful degradation)\n")
			return html, finalURL, nil
		}
	} else {
		fmt.Printf("Primary URL navigation returned empty HTML\n")
	}

	// Generate alternate URLs and try them
	alternates, err := b.GenerateAlternateURLs(targetURL)
	if err != nil {
		fmt.Printf("Failed to generate alternate URLs: %v\n", err)
		// If we have HTML from primary, return it even without alternates
		if len(html) > 0 && !b.LooksLikeCFBlock(html) {
			return html, finalURL, nil
		}
		return "", "", fmt.Errorf("failed to generate alternate URLs: %w", err)
	}

	fmt.Printf("Trying %d alternate URLs\n", len(alternates))
	for i, altURL := range alternates {
		fmt.Printf("Trying alternate URL %d/%d: %s\n", i+1, len(alternates), altURL)
		altHTML, altFinalURL, altErr := b.navigateAndExtract(ctx, altURL)
		if altErr == nil && len(altHTML) > 0 {
			// Reject only if blocked
			if b.LooksLikeCFBlock(altHTML) {
				fmt.Printf("Alternate URL %d blocked, trying next\n", i+1)
				continue // Try next alternate
			}
			// Got valid HTML from alternate
			textLength := len(strings.TrimSpace(altHTML))
			fmt.Printf("Alternate URL %d succeeded: HTML length=%d chars\n", i+1, textLength)
			return altHTML, altFinalURL, nil
		} else if len(altHTML) > 0 && !b.LooksLikeCFBlock(altHTML) {
			// Got HTML despite errors
			fmt.Printf("Alternate URL %d had errors but returning HTML (graceful degradation)\n", i+1)
			return altHTML, altFinalURL, nil
		}
		fmt.Printf("Alternate URL %d failed: %v\n", i+1, altErr)
	}

	// Last resort: return HTML from primary if we have any
	if len(html) > 0 && !b.LooksLikeCFBlock(html) {
		fmt.Printf("Returning primary URL HTML as last resort (length: %d)\n", len(html))
			return html, finalURL, nil
		}

	return "", "", fmt.Errorf("all URLs failed or were blocked")
}

// HTMLSnapshot represents a captured HTML at a specific point in time
type HTMLSnapshot struct {
	HTML      string
	URL       string
	Timestamp time.Time
	Stage     string // "initial", "after-consent", "after-scroll", "stable"
	Length    int
}

// navigateAndExtract navigates to a URL and extracts HTML content
// Refactored to use progressive capture, retry logic, and graceful degradation
func (b *BrowserClient) navigateAndExtract(ctx context.Context, targetURL string) (string, string, error) {
	// Use retry logic with up to 3 attempts for transient failures
	remainingTime := calculateRemainingTime(ctx)
	maxRetries := 3
	// Reduce retries if we're running low on time
	if remainingTime < 60*time.Second {
		maxRetries = 2
	}
	if remainingTime < 30*time.Second {
		maxRetries = 1
	}

	snapshots, capturedURL, err := b.retryNavigation(ctx, targetURL, maxRetries)
	if err != nil {
		// Even if errors occurred, try to return best HTML we captured
		if len(snapshots) > 0 {
			best := b.getBestHTML(snapshots)
			if best != nil && len(best.HTML) > 0 {
				fmt.Printf("Navigation had errors but returning captured HTML (stage: %s, length: %d)\n",
					best.Stage, best.Length)
				return best.HTML, best.URL, nil
			}
		}
		return "", "", fmt.Errorf("navigation failed: %w", err)
	}

	// Return best HTML from snapshots
	best := b.getBestHTML(snapshots)
	if best != nil {
		fmt.Printf("Returning best HTML snapshot (stage: %s, length: %d)\n", best.Stage, best.Length)
		// Use captured URL if best snapshot doesn't have URL
		if best.URL == "" {
			best.URL = capturedURL
		}
		return best.HTML, best.URL, nil
	}

	return "", "", fmt.Errorf("no HTML captured from any snapshot")
}

// navigateAndExtractLegacy is the old implementation kept for reference/fallback
func (b *BrowserClient) navigateAndExtractLegacy(ctx context.Context, targetURL string) (string, string, error) {
	var html string
	var finalURL string

	// Fixed wait times for challenge resolution
	initialSleep := 3 * time.Second
	
	// Calculate challenge wait time based on remaining parent context time
	// Leave at least 5 seconds for final operations (extraction, cleanup)
	minRemainingForCleanup := 5 * time.Second
	defaultChallengeWait := 30 * time.Second // Increased to 30s for Cloudflare challenges
	
	// Check parent context deadline
	var maxChallengeWait time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > minRemainingForCleanup {
			// Use smaller of: default wait time OR (remaining time - cleanup buffer)
			maxChallengeWait = defaultChallengeWait
			if remaining-minRemainingForCleanup < maxChallengeWait {
				maxChallengeWait = remaining - minRemainingForCleanup
			}
			// Ensure minimum wait time
			if maxChallengeWait < 3*time.Second {
				maxChallengeWait = 3 * time.Second
			}
		} else {
			// Very little time left, use minimal wait
			maxChallengeWait = 3 * time.Second
		}
	} else {
		// No deadline, use default
		maxChallengeWait = defaultChallengeWait
	}

	// Log challenge wait configuration
	if deadline, ok := ctx.Deadline(); ok {
		fmt.Printf("Challenge wait timeout: %v (parent deadline: %v)\n", maxChallengeWait, deadline.Format(time.RFC3339))
	} else {
		fmt.Printf("Challenge wait timeout: %v (no parent deadline)\n", maxChallengeWait)
	}

	err := chromedp.Run(ctx, chromedp.Tasks{
		// Navigate to the URL
		chromedp.Navigate(targetURL),

		// Wait for network to be idle
		chromedp.WaitReady("body"),

		// Wait for document ready state to be complete
		chromedp.ActionFunc(func(ctx context.Context) error {
			var readyState string
			maxWait := 10 * time.Second
			deadline := time.Now().Add(maxWait)
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				if err := chromedp.Evaluate("document.readyState", &readyState).Do(ctx); err == nil {
					if readyState == "complete" {
						// Additional wait for JS execution - increased for JS-heavy sites
						chromedp.Sleep(2 * time.Second).Do(ctx)
						// Also wait for network idle if possible
						chromedp.Evaluate(`
							new Promise((resolve) => {
								if (document.readyState === 'complete') {
									setTimeout(resolve, 1000);
								} else {
									window.addEventListener('load', () => setTimeout(resolve, 1000));
								}
							});
						`, nil).Do(ctx)
						return nil
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}),

		// Handle consent dialogs after page loads
		chromedp.ActionFunc(func(ctx context.Context) error {
			return b.handleConsentDialogs(ctx)
		}),

		// Wait for potential challenge to resolve (with timeout)
		chromedp.Sleep(initialSleep),

		// Scroll page to trigger lazy-loaded content
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Scroll down gradually to trigger lazy loading
			for i := 0; i < 3; i++ {
				if err := chromedp.Evaluate(fmt.Sprintf("window.scrollTo(0, %d)", (i+1)*500), nil).Do(ctx); err == nil {
					chromedp.Sleep(500 * time.Millisecond).Do(ctx)
				}
			}
			// Scroll back to top
			if err := chromedp.Evaluate("window.scrollTo(0, 0)", nil).Do(ctx); err == nil {
				chromedp.Sleep(500 * time.Millisecond).Do(ctx)
			}
			return nil
		}),

		// Handle paywall overlays after content loads
		chromedp.ActionFunc(func(ctx context.Context) error {
			b.handlePaywall(ctx)
			return nil
		}),

		// Check if challenge resolved by waiting for content to appear
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Use dynamically calculated challenge wait time
			// But also respect the parent context deadline
			challengeCtx, cancel := context.WithTimeout(ctx, maxChallengeWait)
			defer cancel()

			var previousHTML string
			var challengeDetected bool
			var errorDetected bool
			var errorCheckCount int
			checkCount := 0
			
			for {
				// Check parent context first (more frequent checks)
				select {
				case <-ctx.Done():
					// Parent context expired, return error to distinguish from timeout
					fmt.Printf("Challenge wait: parent context expired\n")
					return fmt.Errorf("parent context expired during challenge wait")
				default:
				}

				// Check challenge timeout
				select {
				case <-challengeCtx.Done():
					// Challenge wait timeout - proceed with whatever we have
					if challengeDetected {
						fmt.Printf("Challenge wait: timeout after detecting challenge\n")
					}
					// Check if parent context also expired
					if ctx.Err() != nil {
						return fmt.Errorf("parent context expired during challenge wait timeout")
					}
					return nil
				default:
				}

				// Periodic check every 500ms
				checkCount++
				var bodyHTML string
				if err := chromedp.Run(ctx, chromedp.Tasks{
					chromedp.OuterHTML("body", &bodyHTML),
				}); err == nil {
					// Re-check parent context after DOM operation
					select {
					case <-ctx.Done():
						fmt.Printf("Challenge wait: context expired during check\n")
						return fmt.Errorf("parent context expired during challenge wait check")
					default:
					}

					// Check for application errors
					isAppError := b.LooksLikeApplicationError(bodyHTML)
					if isAppError {
						if !errorDetected {
							errorDetected = true
							fmt.Printf("Application error detected, waiting for recovery...\n")
						}
						errorCheckCount++
						// Wait up to 10 checks (5 seconds) for error to resolve
						if errorCheckCount < 10 {
							previousHTML = bodyHTML
							time.Sleep(500 * time.Millisecond)
							continue
						} else {
							// Error persisted, but check if we have content anyway
							fmt.Printf("Application error persisted after %d checks\n", errorCheckCount)
						}
					} else if errorDetected {
						// Error was detected but now resolved
						fmt.Printf("Application error resolved after %d checks\n", errorCheckCount)
						errorDetected = false
					}

					isChallengePage := b.LooksLikeChallengePage(bodyHTML)
					if isChallengePage {
						challengeDetected = true
						previousHTML = bodyHTML
					} else if challengeDetected {
						// Challenge was detected earlier but now it's resolved
						if previousHTML != bodyHTML {
							fmt.Printf("Challenge wait: challenge resolved after %d checks\n", checkCount)
							return nil
						}
					} else {
						// No challenge detected, check if content has changed meaningfully
						if previousHTML != "" && previousHTML != bodyHTML {
							// Verify that we have actual article content, not just error messages
							isAppError := b.LooksLikeApplicationError(bodyHTML)
							hasContent := b.hasArticleContent(bodyHTML)
							textLength := len(strings.TrimSpace(bodyHTML))
							
							// Progressive threshold: lower requirement if we've waited longer
							// Start with 5000, reduce to 3000 after 10 checks, 2000 after 20 checks
							minTextLength := 5000
							if checkCount > 20 {
								minTextLength = 2000
							} else if checkCount > 10 {
								minTextLength = 3000
							}
							
							// Diagnostic logging
							if checkCount%5 == 0 { // Log every 5th check to reduce noise
								fmt.Printf("Content check %d: appError=%v, hasContent=%v, textLength=%d (min=%d)\n",
									checkCount, isAppError, hasContent, textLength, minTextLength)
							}
							
							if !isAppError && hasContent {
								// Check text length with progressive threshold
								if textLength > minTextLength {
									fmt.Printf("Content verified after %d checks (text length: %d, min required: %d)\n",
										checkCount, textLength, minTextLength)
									return nil
								} else if textLength > 1000 && checkCount > 15 {
									// If we've waited a while and have some content, be more lenient
									fmt.Printf("Content verified (lenient) after %d checks (text length: %d)\n",
										checkCount, textLength)
							return nil
						}
							}
							
							// Content changed but might be error or still loading, continue waiting
						previousHTML = bodyHTML
						} else if previousHTML == "" {
							// First check, store initial HTML
							previousHTML = bodyHTML
							textLength := len(strings.TrimSpace(bodyHTML))
							fmt.Printf("Initial content check: HTML length=%d chars\n", textLength)
						}
					}
				}
				time.Sleep(500 * time.Millisecond)
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

// LooksLikeApplicationError checks if HTML content indicates an application error
func (b *BrowserClient) LooksLikeApplicationError(html string) bool {
	htmlLower := strings.ToLower(html)
	return b.regexes["appError"].MatchString(htmlLower)
}

// handleConsentDialogs attempts to automatically dismiss common consent/privacy dialogs
// Enhanced with SCMP-specific selectors and retry logic
func (b *BrowserClient) handleConsentDialogs(ctx context.Context) error {
	maxRetries := 3
	retryDelay := 1 * time.Second

	// Use JavaScript to find and click consent buttons by text content as well
	jsCheck := `
		(function() {
			try {
				// SCMP-specific and common CSS selector-based buttons
				const selectors = [
					// SCMP-specific
					"[data-testid='accept-button']",
					"[data-testid='consent-accept']",
					".scmp-consent-accept",
					".consent-accept-button",
					// Common patterns
					"button[id*='accept']",
					"button[id*='consent']",
					"button[class*='accept']",
					"button[class*='consent']",
					".accept-all",
					".cookie-accept",
					"#onetrust-accept-btn-handler",
					"[data-consent='accept']",
					"[data-action='accept']"
				];
				
				for (const selector of selectors) {
					const elem = document.querySelector(selector);
					if (elem && elem.offsetParent !== null) {
						elem.click();
						return true;
					}
				}
				
				// Text-based matching for buttons containing accept/consent text
				const buttons = document.querySelectorAll('button, a, div[role="button"]');
				for (const btn of buttons) {
					const text = btn.textContent || btn.innerText || '';
					const lowerText = text.toLowerCase().trim();
					if ((lowerText.includes('accept') || lowerText.includes('consent') || 
						 lowerText.includes('agree') || lowerText.includes('continue')) && 
						btn.offsetParent !== null && 
						!lowerText.includes('decline') && 
						!lowerText.includes('reject')) {
						btn.click();
						return true;
					}
				}
			} catch(e) {
				console.error('Error handling consent dialog:', e);
			}
			return false;
		})();
	`

	// Retry up to maxRetries times to handle multiple dialogs or delayed appearance
	for attempt := 0; attempt < maxRetries; attempt++ {
		var clicked bool
		if err := chromedp.Evaluate(jsCheck, &clicked).Do(ctx); err != nil {
			if attempt < maxRetries-1 {
				chromedp.Sleep(retryDelay).Do(ctx)
				continue
			}
			return err
		}

		if clicked {
			// Wait longer for dialog to dismiss (increased from 500ms to 1s)
			chromedp.Sleep(1 * time.Second).Do(ctx)
			fmt.Printf("Consent dialog dismissed (attempt %d/%d)\n", attempt+1, maxRetries)
			
			// Check if there are more dialogs (some sites have nested consent)
			// Continue to next attempt to handle additional dialogs
			if attempt < maxRetries-1 {
				chromedp.Sleep(retryDelay).Do(ctx)
				continue
			}
			return nil
		}

		// No dialog found, wait before next attempt in case it appears
		if attempt < maxRetries-1 {
			chromedp.Sleep(retryDelay).Do(ctx)
		}
	}

	return nil
}

// handlePaywall attempts to detect and handle paywall overlays
// Returns true if a paywall was detected and potentially handled
func (b *BrowserClient) handlePaywall(ctx context.Context) bool {
	paywallScript := `
		(function() {
			try {
				// Common paywall overlay selectors
				const paywallSelectors = [
					'[data-testid="paywall"]',
					'[class*="paywall"]',
					'[id*="paywall"]',
					'[class*="subscription-wall"]',
					'[class*="meter-wall"]',
					'.piano-template-modal',
					'[data-piano-template]',
					// SCMP-specific
					'[grid-area="paywall"]',
					'.css-1kfpym9'
				];

				let paywallFound = false;

				// Try to remove or hide paywall overlays
				for (const selector of paywallSelectors) {
					const elements = document.querySelectorAll(selector);
					if (elements.length > 0) {
						paywallFound = true;
						elements.forEach(el => {
							// Try to remove or hide
							if (el.style) {
								el.style.display = 'none';
								el.style.visibility = 'hidden';
							}
							// Also try to remove from DOM
							if (el.parentNode) {
								el.parentNode.removeChild(el);
							}
						});
					}
				}

				// Remove overflow hidden from body (often used to prevent scrolling)
				if (document.body && document.body.style) {
					document.body.style.overflow = 'auto';
					document.body.style.position = 'static';
				}

				// Remove any backdrop/overlay elements
				const backdrops = document.querySelectorAll('[class*="backdrop"], [class*="overlay"], [class*="modal-backdrop"]');
				backdrops.forEach(el => {
					if (el.style) {
						el.style.display = 'none';
					}
				});

				return paywallFound;
			} catch(e) {
				console.error('Error handling paywall:', e);
				return false;
			}
		})();
	`

	var paywallFound bool
	if err := chromedp.Evaluate(paywallScript, &paywallFound).Do(ctx); err != nil {
		return false
	}

	if paywallFound {
		fmt.Printf("Paywall detected and removal attempted\n")
		chromedp.Sleep(500 * time.Millisecond).Do(ctx)
	}

	return paywallFound
}

// hasArticleContent checks if the HTML contains meaningful article content
func (b *BrowserClient) hasArticleContent(html string) bool {
	htmlLower := strings.ToLower(html)

	// Check for article tags
	if strings.Contains(htmlLower, "<article") || strings.Contains(htmlLower, "<main") {
		return true
	}

	// Check for content classes
	contentIndicators := []string{
		"class=\"article",
		"class='article",
		"class=\"content",
		"class='content",
		"class=\"story-content",
		"class='story-content",
		"class=\"post-content",
		"class='post-content",
	}

	for _, indicator := range contentIndicators {
		if strings.Contains(htmlLower, indicator) {
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

// retryNavigation wraps captureHTMLSnapshots with retry logic
// Retries navigation on specific errors (timeout, network errors) with exponential backoff
// Returns the best result from all attempts
func (b *BrowserClient) retryNavigation(ctx context.Context, targetURL string, maxRetries int) ([]HTMLSnapshot, string, error) {
	var allSnapshots []HTMLSnapshot
	var finalURL string
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 2s, 4s
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			fmt.Printf("Retrying navigation (attempt %d/%d) after %v backoff...\n", attempt+1, maxRetries, backoff)
			select {
			case <-ctx.Done():
				break
			case <-time.After(backoff):
			}
		}

		snapshots, url, err := b.captureHTMLSnapshots(ctx, targetURL)

		// Collect all snapshots
		allSnapshots = append(allSnapshots, snapshots...)
		if url != "" {
			finalURL = url
		}

		// Check if we should retry
		if err == nil && len(snapshots) > 0 {
			// Success - return immediately
			fmt.Printf("Navigation succeeded on attempt %d/%d\n", attempt+1, maxRetries)
			return snapshots, url, nil
		}

		lastErr = err
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		// Retry on timeout or network errors
		shouldRetry := strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "context deadline exceeded") ||
			strings.Contains(errStr, "network") ||
			strings.Contains(errStr, "connection") ||
			(len(snapshots) == 0 && attempt < maxRetries-1)

		if !shouldRetry || attempt >= maxRetries-1 {
			// Don't retry or last attempt
			break
		}

		fmt.Printf("Navigation attempt %d/%d failed: %v, will retry...\n", attempt+1, maxRetries, err)
	}

	// Return best result from all attempts
	if len(allSnapshots) > 0 {
		best := b.getBestHTML(allSnapshots)
		if best != nil {
			fmt.Printf("Returning best snapshot from %d attempts (%d total snapshots)\n", maxRetries, len(allSnapshots))
			return []HTMLSnapshot{*best}, finalURL, nil
		}
	}

	return allSnapshots, finalURL, lastErr
}

// captureHTMLSnapshots navigates and captures HTML at multiple points during page load
// Returns all snapshots, final URL, and any error encountered
// CRITICAL: Captures HTML inline during navigation to ensure we always get at least one snapshot
func (b *BrowserClient) captureHTMLSnapshots(ctx context.Context, targetURL string) ([]HTMLSnapshot, string, error) {
	var snapshots []HTMLSnapshot
	var finalURL string
	var captureErrors []error

	fmt.Printf("Starting progressive HTML capture for: %s\n", targetURL)

	// Log context deadline information
	remainingTime := calculateRemainingTime(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		fmt.Printf("Context deadline: %v (remaining: %v)\n", deadline.Format(time.RFC3339), remainingTime)
		if remainingTime < 30*time.Second {
			fmt.Printf("WARNING: Low time budget (%v), some phases may be skipped\n", remainingTime)
		}
	} else {
		fmt.Printf("No context deadline set\n")
	}

	// Calculate wait times
	maxChallengeWait := b.calculateChallengeWait(ctx)
	fmt.Printf("Max challenge wait: %v\n", maxChallengeWait)

	// Variables to capture HTML inline during tasks
	var initialHTML, afterConsentHTML, afterScrollHTML string
	var currentURL string

	// CRITICAL: Capture HTML in a separate quick operation BEFORE long waits
	// This ensures we always get at least skeleton HTML even if navigation fails
	// Use timeout-protected navigation and DOMContentLoaded for speed
	navTimeout := 20 * time.Second
	if remainingTime < navTimeout+5*time.Second {
		navTimeout = remainingTime - 5*time.Second
		if navTimeout < 5*time.Second {
			navTimeout = 5 * time.Second
		}
	}

	// Navigate with timeout protection
	err := b.navigateWithTimeout(ctx, targetURL, navTimeout)
	if err != nil {
		fmt.Printf("Navigation had error (will try to capture anyway): %v\n", err)
	}

	// Wait for DOMContentLoaded (faster than WaitReady("body"))
	domWait := 5 * time.Second
	if remainingTime < domWait+3*time.Second {
		domWait = 3 * time.Second
	}
	_ = b.waitForDOMContentLoaded(ctx, domWait)
	
	// Try periodic capture during DOM wait (limited duration)
	periodicSnaps := b.captureHTMLPeriodically(ctx, 2*time.Second, 5*time.Second)
	if len(periodicSnaps) > 0 {
		snapshots = append(snapshots, periodicSnaps...)
		fmt.Printf("Collected %d periodic snapshots during navigation\n", len(periodicSnaps))
	}
	
	// Immediately capture HTML in separate operation (even if navigation had errors)
	_ = chromedp.Run(ctx, chromedp.Tasks{
		chromedp.ActionFunc(func(ctx context.Context) error {
			var html, url string
			if err := chromedp.Location(&url).Do(ctx); err == nil {
				if err := chromedp.OuterHTML("html", &html).Do(ctx); err == nil {
					initialHTML = html
					currentURL = url
					textLength := len(strings.TrimSpace(html))
					fmt.Printf("Captured initial snapshot: %d chars\n", textLength)
					if textLength > 0 {
						snapshots = append(snapshots, HTMLSnapshot{
							HTML:      html,
							URL:       url,
							Timestamp: time.Now(),
							Stage:     "initial",
							Length:    textLength,
						})
					}
				}
			}
			return nil
		}),
	})

	// Now do the longer waits and processing in separate chromedp.Run
	// This way if context expires, we already have the initial snapshot
	err2 := chromedp.Run(ctx, chromedp.Tasks{

		// Check for Cloudflare challenge (with adaptive timeout)
		chromedp.ActionFunc(func(ctx context.Context) error {
			var bodyHTML string
			if err := chromedp.OuterHTML("body", &bodyHTML).Do(ctx); err == nil {
				if b.LooksLikeCFBlock(bodyHTML) {
					// Adaptive wait based on remaining time
					remainingTime := calculateRemainingTime(ctx)
					cfWait := 30 * time.Second
					if remainingTime < cfWait+5*time.Second {
						cfWait = remainingTime - 5*time.Second
						if cfWait < 5*time.Second {
							cfWait = 5 * time.Second
						}
					}
					fmt.Printf("Cloudflare challenge detected, waiting up to %v for resolution...\n", cfWait)
					chromedp.Sleep(cfWait).Do(ctx)
					// Re-check after wait
					chromedp.OuterHTML("body", &bodyHTML).Do(ctx)
					if b.LooksLikeCFBlock(bodyHTML) {
						fmt.Printf("Challenge still present after %v wait\n", cfWait)
					} else {
						fmt.Printf("Challenge resolved after wait\n")
					}
				}
			}
			return nil
		}),

		// Phase 2: Wait for ready state (adaptive timeout based on remaining time)
		chromedp.ActionFunc(func(ctx context.Context) error {
			remainingTime := calculateRemainingTime(ctx)
			maxWait := 10 * time.Second // Reduced default from 15s
			// Reduce wait if we're low on time
			if remainingTime < maxWait+5*time.Second {
				maxWait = remainingTime - 5*time.Second
				if maxWait < 2*time.Second {
					maxWait = 2 * time.Second
				}
			}

			var readyState string
			deadline := time.Now().Add(maxWait)
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				if err := chromedp.Evaluate("document.readyState", &readyState).Do(ctx); err == nil {
					if readyState == "complete" {
						// Reduced JS execution wait
						chromedp.Sleep(1 * time.Second).Do(ctx)
						return nil
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
			return nil
		}),

		// Phase 2.5: Wait for network idle (optional, skip if low on time)
		chromedp.ActionFunc(func(ctx context.Context) error {
			remainingTime := calculateRemainingTime(ctx)
			if remainingTime < 10*time.Second {
				fmt.Printf("Skipping network idle wait (low time: %v)\n", remainingTime)
				return nil
			}
			networkWait := 3 * time.Second // Reduced from 5s
			if remainingTime < networkWait+3*time.Second {
				networkWait = 2 * time.Second
			}
			b.waitForNetworkIdle(ctx, networkWait)
			return nil // Non-critical, continue even if it fails
		}),

		// Phase 2.6: Wait for content selectors (optional, skip if low on time)
		chromedp.ActionFunc(func(ctx context.Context) error {
			remainingTime := calculateRemainingTime(ctx)
			if remainingTime < 10*time.Second {
				fmt.Printf("Skipping content selector wait (low time: %v)\n", remainingTime)
				return nil
			}
			contentWait := 10 * time.Second // Reduced from 15s
			if remainingTime < contentWait+3*time.Second {
				contentWait = 5 * time.Second
				if remainingTime < contentWait+3*time.Second {
					contentWait = 3 * time.Second
				}
			}
			b.waitForContentSelectors(ctx, contentWait)
			return nil // Non-critical, continue even if it fails
		}),

		// Phase 3: Handle consent dialogs
		chromedp.ActionFunc(func(ctx context.Context) error {
			return b.handleConsentDialogs(ctx)
		}),
		chromedp.Sleep(500 * time.Millisecond),

		// Capture after consent inline
		chromedp.ActionFunc(func(ctx context.Context) error {
			var html, url string
			if err := chromedp.Location(&url).Do(ctx); err == nil {
				if err := chromedp.OuterHTML("html", &html).Do(ctx); err == nil {
					afterConsentHTML = html
					if url != "" {
						currentURL = url
					}
					textLength := len(strings.TrimSpace(html))
					fmt.Printf("Captured after-consent snapshot: %d chars\n", textLength)
					if textLength > 0 {
						snapshots = append(snapshots, HTMLSnapshot{
							HTML:      html,
							URL:       url,
							Timestamp: time.Now(),
							Stage:     "after-consent",
							Length:    textLength,
						})
					}
				}
			}
			return nil
		}),

		// Phase 4: Scroll to trigger lazy loading (optional - skip if low on time)
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Check if we have enough time for scrolling
			remainingTime := calculateRemainingTime(ctx)
			if remainingTime < 10*time.Second {
				fmt.Printf("Skipping scroll phase due to low time budget (%v)\n", remainingTime)
				return nil
			}

			// Scroll down gradually
			for i := 0; i < 3; i++ {
				// Check context before each scroll action
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				chromedp.Evaluate(fmt.Sprintf("window.scrollTo(0, %d)", (i+1)*500), nil).Do(ctx)
				chromedp.Sleep(500 * time.Millisecond).Do(ctx)
			}
			// Scroll back to top
			chromedp.Evaluate("window.scrollTo(0, 0)", nil).Do(ctx)
			chromedp.Sleep(500 * time.Millisecond).Do(ctx)
			return nil
		}),

		// Capture after scroll inline
		chromedp.ActionFunc(func(ctx context.Context) error {
			var html, url string
			if err := chromedp.Location(&url).Do(ctx); err == nil {
				if err := chromedp.OuterHTML("html", &html).Do(ctx); err == nil {
					afterScrollHTML = html
					if url != "" {
						currentURL = url
					}
					textLength := len(strings.TrimSpace(html))
					fmt.Printf("Captured after-scroll snapshot: %d chars\n", textLength)
					if textLength > 0 {
						snapshots = append(snapshots, HTMLSnapshot{
							HTML:      html,
							URL:       url,
							Timestamp: time.Now(),
							Stage:     "after-scroll",
							Length:    textLength,
						})
					}
				}
			}
			return nil
		}),

		// Phase 5: Wait for stability (captures inline during stability checks)
		chromedp.ActionFunc(func(ctx context.Context) error {
			stableSnap := b.waitForContentStabilityInline(ctx, maxChallengeWait, &snapshots)
			if stableSnap != nil {
				fmt.Printf("Captured stable snapshot: %d chars\n", stableSnap.Length)
			}
			return nil
		}),

		// Final URL capture
		chromedp.ActionFunc(func(ctx context.Context) error {
			chromedp.Location(&finalURL).Do(ctx)
			if finalURL == "" {
				finalURL = currentURL
			}
			return nil
		}),
	})

	// Combine errors from both navigation runs
	if err != nil {
		captureErrors = append(captureErrors, fmt.Errorf("initial navigation failed: %w", err))
	}
	if err2 != nil {
		captureErrors = append(captureErrors, fmt.Errorf("processing phase failed: %w", err2))
	}
	
	if len(captureErrors) > 0 {
		fmt.Printf("Navigation had errors but captured %d snapshots\n", len(snapshots))
	}

	// Ensure we have at least one snapshot - use whatever HTML we captured
	if len(snapshots) == 0 {
		// Try to use any HTML we captured in variables
		bestHTML := afterScrollHTML
		if bestHTML == "" {
			bestHTML = afterConsentHTML
		}
		if bestHTML == "" {
			bestHTML = initialHTML
		}

		if bestHTML != "" {
			textLength := len(strings.TrimSpace(bestHTML))
			fmt.Printf("Creating fallback snapshot from captured HTML: %d chars\n", textLength)
			snapshots = append(snapshots, HTMLSnapshot{
				HTML:      bestHTML,
				URL:       currentURL,
				Timestamp: time.Now(),
				Stage:     "fallback",
				Length:    textLength,
			})
		} else {
			// Last resort: try minimal navigation
			fmt.Printf("No HTML captured, trying minimal navigation strategy\n")
			minHTML, minURL, minErr := b.minimalNavigation(ctx, targetURL)
			if minErr == nil && len(minHTML) > 0 {
				textLength := len(strings.TrimSpace(minHTML))
				snapshots = append(snapshots, HTMLSnapshot{
					HTML:      minHTML,
					URL:       minURL,
					Timestamp: time.Now(),
					Stage:     "minimal-fallback",
					Length:    textLength,
				})
				fmt.Printf("Minimal navigation captured: %d chars\n", textLength)
			} else {
				// Final fallback: try JavaScript fetch
				fmt.Printf("Minimal navigation failed, trying JavaScript fetch fallback\n")
				jsHTML := b.fetchHTMLViaJS(ctx, targetURL)
				if len(jsHTML) > 0 {
					snapshots = append(snapshots, HTMLSnapshot{
						HTML:      jsHTML,
						URL:       targetURL,
						Timestamp: time.Now(),
						Stage:     "js-fetch-fallback",
						Length:    len(strings.TrimSpace(jsHTML)),
					})
					fmt.Printf("JS fetch captured: %d chars\n", len(strings.TrimSpace(jsHTML)))
				} else {
					// Last resort: try final capture
					finalSnap := b.captureSnapshotFallback(ctx)
					if finalSnap != nil {
						snapshots = append(snapshots, *finalSnap)
					}
				}
			}
		}
	}

	if finalURL == "" {
		finalURL = currentURL
	}

	// Combine errors if any
	var combinedErr error
	if len(captureErrors) > 0 && len(snapshots) == 0 {
		// Only treat as error if we got no snapshots at all
		combinedErr = fmt.Errorf("capture errors: %v", captureErrors)
	}

	fmt.Printf("Total snapshots captured: %d\n", len(snapshots))
	return snapshots, finalURL, combinedErr
}

// captureSnapshot captures HTML at current point in time
// NOTE: Uses separate chromedp.Run - use inline capture when within existing context
func (b *BrowserClient) captureSnapshot(ctx context.Context, stage string) *HTMLSnapshot {
	var html string
	var url string

	err := chromedp.Run(ctx, chromedp.Tasks{
		chromedp.Location(&url),
		chromedp.OuterHTML("html", &html),
	})

	if err != nil {
		return nil
	}

	textLength := len(strings.TrimSpace(html))
	return &HTMLSnapshot{
		HTML:      html,
		URL:       url,
		Timestamp: time.Now(),
		Stage:     stage,
		Length:    textLength,
	}
}

// captureSnapshotFallback is a last-resort attempt to capture HTML
// Uses a very short timeout to avoid hanging
func (b *BrowserClient) captureSnapshotFallback(ctx context.Context) *HTMLSnapshot {
	// Create a short timeout for fallback
	fallbackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var html string
	var url string

	err := chromedp.Run(fallbackCtx, chromedp.Tasks{
		chromedp.Location(&url),
		chromedp.OuterHTML("html", &html),
	})

	if err != nil {
		return nil
	}

	textLength := len(strings.TrimSpace(html))
	if textLength == 0 {
		return nil
	}

	return &HTMLSnapshot{
		HTML:      html,
		URL:       url,
		Timestamp: time.Now(),
		Stage:     "final-fallback",
		Length:    textLength,
	}
}

// waitForContentStabilityInline waits for HTML content to stabilize (inline version)
// Works within existing chromedp context and appends snapshots to the provided slice
// Returns the final stable snapshot
func (b *BrowserClient) waitForContentStabilityInline(ctx context.Context, maxWait time.Duration, snapshots *[]HTMLSnapshot) *HTMLSnapshot {
	stabilityCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	var previousLength int
	var stableCount int
	stableThreshold := 3 // Number of consecutive stable checks needed
	checkInterval := 500 * time.Millisecond

	fmt.Printf("Waiting for content stability (max: %v)\n", maxWait)

	var lastSnap *HTMLSnapshot

	for {
		select {
		case <-stabilityCtx.Done():
			fmt.Printf("Stability wait timeout, returning latest snapshot\n")
			// Return whatever we have
			if lastSnap != nil {
				lastSnap.Stage = "stable-timeout"
				*snapshots = append(*snapshots, *lastSnap)
			}
			return lastSnap
		case <-ctx.Done():
			if lastSnap != nil {
				lastSnap.Stage = "stable-interrupted"
				*snapshots = append(*snapshots, *lastSnap)
			}
			return lastSnap
		default:
		}

		// Capture snapshot inline
		var html, url string
		snap := &HTMLSnapshot{}
		if err := chromedp.Location(&url).Do(ctx); err == nil {
			if err := chromedp.OuterHTML("html", &html).Do(ctx); err == nil {
				textLength := len(strings.TrimSpace(html))
				snap = &HTMLSnapshot{
					HTML:      html,
					URL:       url,
					Timestamp: time.Now(),
					Stage:     "stability-check",
					Length:    textLength,
				}
				lastSnap = snap
			}
		}

		if snap == nil || snap.Length == 0 {
			chromedp.Sleep(checkInterval).Do(ctx)
			continue
		}

		currentLength := snap.Length
		lengthDiff := currentLength - previousLength
		percentChange := 0.0
		if previousLength > 0 {
			absDiff := lengthDiff
			if absDiff < 0 {
				absDiff = -absDiff
			}
			percentChange = float64(absDiff) / float64(previousLength) * 100
		}

		// Consider stable if change is less than 5% for 3 consecutive checks
		if previousLength > 0 && percentChange < 5.0 {
			stableCount++
			if stableCount >= stableThreshold {
				fmt.Printf("Content stabilized after %d checks (length: %d, stable for %d checks)\n",
					stableCount, currentLength, stableThreshold)
				snap.Stage = "stable"
				*snapshots = append(*snapshots, *snap)
				return snap
			}
		} else {
			stableCount = 0
		}

		if previousLength > 0 {
			fmt.Printf("Stability check: length=%d (change: %d, %.1f%%), stable_count=%d\n",
				currentLength, lengthDiff, percentChange, stableCount)
		} else {
			fmt.Printf("Stability check: initial length=%d\n", currentLength)
		}

		previousLength = currentLength
		chromedp.Sleep(checkInterval).Do(ctx)
	}
}

// waitForContentStability waits for HTML content to stabilize (stop changing significantly)
// Simplified approach: track HTML length changes, consider stable when changes are minimal
// NOTE: This is the standalone version, use waitForContentStabilityInline when within chromedp context
func (b *BrowserClient) waitForContentStability(ctx context.Context, maxWait time.Duration) *HTMLSnapshot {
	stabilityCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	var previousLength int
	var stableCount int
	stableThreshold := 3 // Number of consecutive stable checks needed
	checkInterval := 500 * time.Millisecond

	fmt.Printf("Waiting for content stability (max: %v)\n", maxWait)

	for {
		select {
		case <-stabilityCtx.Done():
			fmt.Printf("Stability wait timeout, returning latest snapshot\n")
			// Return whatever we have
			return b.captureSnapshot(ctx, "stable-timeout")
		case <-ctx.Done():
			return b.captureSnapshot(ctx, "stable-interrupted")
		default:
		}

		snap := b.captureSnapshot(ctx, "stability-check")
		if snap == nil {
			time.Sleep(checkInterval)
			continue
		}

		currentLength := snap.Length
		lengthDiff := currentLength - previousLength
		percentChange := 0.0
		if previousLength > 0 {
			absDiff := lengthDiff
			if absDiff < 0 {
				absDiff = -absDiff
			}
			percentChange = float64(absDiff) / float64(previousLength) * 100
		}

		// Consider stable if change is less than 5% for 3 consecutive checks
		if previousLength > 0 && percentChange < 5.0 {
			stableCount++
			if stableCount >= stableThreshold {
				fmt.Printf("Content stabilized after %d checks (length: %d, stable for %d checks)\n",
					stableCount, currentLength, stableThreshold)
				return snap
			}
		} else {
			stableCount = 0
		}

		if previousLength > 0 {
			fmt.Printf("Stability check: length=%d (change: %d, %.1f%%), stable_count=%d\n",
				currentLength, lengthDiff, percentChange, stableCount)
		} else {
			fmt.Printf("Stability check: initial length=%d\n", currentLength)
		}

		previousLength = currentLength
		time.Sleep(checkInterval)
	}
}

// getBestHTML selects the best HTML snapshot from multiple captures
// Prefers: stable > after-scroll > after-consent > initial
// Among same stage, prefers longer HTML
func (b *BrowserClient) getBestHTML(snapshots []HTMLSnapshot) *HTMLSnapshot {
	if len(snapshots) == 0 {
		return nil
	}

	stagePriority := map[string]int{
		"stable":           6,
		"stable-timeout":   5,
		"stable-interrupted": 4,
		"after-scroll":     3,
		"after-consent":    2,
		"periodic":         1,
		"initial":          0,
		"minimal":          0,
		"minimal-fallback": -1,
		"js-fetch-fallback": -2,
		"fallback":         -1,
		"final":            -1,
		"final-fallback":   -2,
		"stability-check":  0,
	}

	var best *HTMLSnapshot
	bestPriority := -1

	for i := range snapshots {
		snap := &snapshots[i]
		priority := stagePriority[snap.Stage]

		// Skip if it's an application error or too small
		if b.LooksLikeApplicationError(snap.HTML) && snap.Length < 1000 {
			continue
		}

		// Prefer higher priority stages
		if priority > bestPriority {
			best = snap
			bestPriority = priority
		} else if priority == bestPriority && best != nil {
			// Same priority: prefer longer HTML
			if snap.Length > best.Length {
				best = snap
			}
		}
	}

	return best
}

// navigateWithTimeout wraps chromedp.Navigate with a hard timeout
// Returns error if navigation takes longer than maxWait, but ensures HTML can still be captured
func (b *BrowserClient) navigateWithTimeout(ctx context.Context, url string, maxWait time.Duration) error {
	navCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- chromedp.Run(navCtx, chromedp.Tasks{
			chromedp.Navigate(url),
		})
	}()

	select {
	case err := <-errChan:
		if err != nil && navCtx.Err() == context.DeadlineExceeded {
			fmt.Printf("Navigation timeout after %v (will try to capture HTML anyway)\n", maxWait)
			// Don't return error - allow HTML capture to proceed
			return nil
		}
		return err
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

// waitForDOMContentLoaded waits for DOMContentLoaded event instead of full page load
// Much faster for JS-heavy sites that load content dynamically
func (b *BrowserClient) waitForDOMContentLoaded(ctx context.Context, maxWait time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	return chromedp.Run(waitCtx, chromedp.Tasks{
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Wait for DOMContentLoaded event
			script := `
				(() => {
					return new Promise((resolve) => {
						if (document.readyState === 'loading') {
							document.addEventListener('DOMContentLoaded', () => resolve(true), { once: true });
						} else {
							resolve(true);
						}
					});
				})();
			`
			return chromedp.Evaluate(script, nil).Do(ctx)
		}),
	})
}

// captureHTMLPeriodically captures HTML at regular intervals during navigation
// Returns snapshots collected during the period
// NOTE: This must be called from within a chromedp context
func (b *BrowserClient) captureHTMLPeriodically(ctx context.Context, interval, maxDuration time.Duration) []HTMLSnapshot {
	var periodicSnapshots []HTMLSnapshot
	deadline := time.Now().Add(maxDuration)
	
	for {
		select {
		case <-ctx.Done():
			return periodicSnapshots
		default:
		}
		
		if time.Now().After(deadline) {
			return periodicSnapshots
		}
		
		// Capture HTML in a quick operation using chromedp
		var html, url string
		if err := chromedp.Run(ctx, chromedp.Tasks{
			chromedp.Location(&url),
			chromedp.OuterHTML("html", &html),
		}); err == nil {
			textLength := len(strings.TrimSpace(html))
			if textLength > 0 {
				snap := HTMLSnapshot{
					HTML:      html,
					URL:       url,
					Timestamp: time.Now(),
					Stage:     "periodic",
					Length:    textLength,
				}
				periodicSnapshots = append(periodicSnapshots, snap)
				fmt.Printf("Periodic capture: %d chars\n", textLength)
				// If we have meaningful content (>500 chars), we can stop early
				if textLength > 500 {
					fmt.Printf("Got meaningful content (%d chars), stopping periodic capture\n", textLength)
					return periodicSnapshots
				}
			}
		}
		
		// Wait for next interval
		select {
		case <-ctx.Done():
			return periodicSnapshots
		case <-time.After(interval):
			continue
		}
	}
}

// minimalNavigation performs a fast, minimal navigation strategy
// Navigates with short timeout, captures immediately, skips most waits
func (b *BrowserClient) minimalNavigation(ctx context.Context, targetURL string) (string, string, error) {
	minCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var html, url string
	var snapshots []HTMLSnapshot

	// Very quick navigation with timeout
	_ = b.navigateWithTimeout(minCtx, targetURL, 10*time.Second)
	
	// Quick DOMContentLoaded wait (or skip if time is low)
	_ = b.waitForDOMContentLoaded(minCtx, 3*time.Second)

	// Capture immediately
	err := chromedp.Run(minCtx, chromedp.Tasks{
		chromedp.Location(&url),
		chromedp.OuterHTML("html", &html),
	})

	if err == nil && len(html) > 0 {
		textLength := len(strings.TrimSpace(html))
		snapshots = append(snapshots, HTMLSnapshot{
			HTML:      html,
			URL:       url,
			Timestamp: time.Now(),
			Stage:     "minimal",
			Length:    textLength,
		})
		fmt.Printf("Minimal navigation captured: %d chars\n", textLength)
		return html, url, nil
	}

	if len(html) == 0 {
		return "", "", fmt.Errorf("minimal navigation: no HTML captured")
	}
	return html, url, err
}

// fetchHTMLViaJS attempts to fetch HTML using JavaScript fetch() as last resort
// This can work even if navigation failed, assuming we're already on the page
func (b *BrowserClient) fetchHTMLViaJS(ctx context.Context, targetURL string) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var html string
	fetchScript := fmt.Sprintf(`
		(async () => {
			try {
				const response = await fetch('%s', {
					method: 'GET',
					headers: {
						'Accept': 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8',
						'Accept-Language': 'en-US,en;q=0.9',
						'User-Agent': navigator.userAgent
					}
				});
				if (response.ok) {
					return await response.text();
				}
			} catch(e) {
				console.error('JS fetch failed:', e);
			}
			return '';
		})();
	`, targetURL)

	err := chromedp.Run(fetchCtx, chromedp.Tasks{
		chromedp.Evaluate(fetchScript, &html),
	})

	if err != nil {
		return ""
	}
	return html
}

// calculateChallengeWait calculates appropriate wait time based on context deadline
func (b *BrowserClient) calculateChallengeWait(ctx context.Context) time.Duration {
	minRemainingForCleanup := 5 * time.Second
	defaultChallengeWait := 30 * time.Second // Increased to 30s for Cloudflare challenges

	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > minRemainingForCleanup {
			maxWait := defaultChallengeWait
			if remaining-minRemainingForCleanup < maxWait {
				maxWait = remaining - minRemainingForCleanup
			}
			if maxWait < 3*time.Second {
				maxWait = 3 * time.Second
			}
			return maxWait
		}
		return 3 * time.Second
	}
	return defaultChallengeWait
}

// waitForContentSelectors waits for article content selectors to appear in the DOM
// Returns true if any selector matches, false if timeout expires
// Uses SCMP-specific selectors first, then falls back to generic ones
func (b *BrowserClient) waitForContentSelectors(ctx context.Context, maxWait time.Duration) bool {
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	// SCMP-specific selectors first, then generic fallbacks
	selectors := []string{
		// SCMP-specific
		"[data-module='ArticleBody']",
		".article-body",
		".article-content",
		".story-body",
		".post-body",
		// Generic
		"article",
		"main",
		"[role='main']",
		".content",
		".post-content",
		".entry-content",
		".story-content",
	}

	checkInterval := 500 * time.Millisecond
	checkScript := `
		(function(selectors) {
			for (var i = 0; i < selectors.length; i++) {
				var element = document.querySelector(selectors[i]);
				if (element) {
					// Check if element is visible and has meaningful content
					var text = element.textContent || element.innerText || '';
					if (text.trim().length > 100) {
						return true;
					}
				}
			}
			return false;
		})(arguments[0]);
	`

	for {
		select {
		case <-waitCtx.Done():
			return false
		case <-ctx.Done():
			return false
		default:
		}

		var found bool
		// Pass selectors as JSON string to the JavaScript function
		selectorsJSON := fmt.Sprintf(`["%s"]`, strings.Join(selectors, `", "`))
		fullScript := fmt.Sprintf(`(%s)(%s)`, checkScript, selectorsJSON)
		if err := chromedp.Evaluate(fullScript, &found).Do(ctx); err == nil && found {
			if deadline, ok := waitCtx.Deadline(); ok {
				fmt.Printf("Content selector found (wait: %v)\n", maxWait-time.Until(deadline))
			} else {
				fmt.Printf("Content selector found\n")
			}
			return true
		}

		chromedp.Sleep(checkInterval).Do(ctx)
	}
}

// waitForNetworkIdle waits for network requests to complete using JavaScript Promise
// Returns error if timeout expires or context is cancelled
func (b *BrowserClient) waitForNetworkIdle(ctx context.Context, maxWait time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	// JavaScript to wait for network idle
	networkIdleScript := `
		new Promise((resolve, reject) => {
			// Check if all resources are loaded
			if (document.readyState === 'complete') {
				// Use Performance API to detect active requests
				const checkNetworkIdle = () => {
					const perfEntries = performance.getEntriesByType('resource');
					const activeRequests = perfEntries.filter(entry => {
						// Check if entry is still loading (simple heuristic)
						return entry.duration === 0 || entry.transferSize === 0;
					}).length;
					
					// Also check fetch/XHR active count via monkey-patching detection
					if (activeRequests === 0 || window.__networkIdleCheck) {
						resolve(true);
						return;
					}
					
					setTimeout(checkNetworkIdle, 500);
				};
				
				// Wait a bit for ongoing requests to complete
				setTimeout(checkNetworkIdle, 2000);
				
				// Fallback: resolve after maxWait if still waiting
				setTimeout(() => resolve(true), arguments[0]);
			} else {
				// Page still loading, wait for load event
				window.addEventListener('load', () => {
					setTimeout(() => resolve(true), 2000);
				}, { once: true });
				
				// Fallback timeout
				setTimeout(() => resolve(true), arguments[0]);
			}
		});
	`

	err := chromedp.Evaluate(fmt.Sprintf("(%s)(%d)", networkIdleScript, int(maxWait.Milliseconds())), nil).Do(waitCtx)
	if err != nil {
		if waitCtx.Err() == context.DeadlineExceeded {
			fmt.Printf("Network idle wait timeout after %v\n", maxWait)
			return nil // Not a critical error, continue anyway
		}
		return err
	}

	fmt.Printf("Network idle detected\n")
	return nil
}

// navigateAndExtractOptimized uses domcontentloaded for faster loading
func (b *BrowserClient) navigateAndExtractOptimized(ctx context.Context, targetURL string) (string, string, error) {
	return b.navigateAndExtract(ctx, targetURL)
}
