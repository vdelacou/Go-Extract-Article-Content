package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"extract-html-scraper/internal/config"
	"extract-html-scraper/internal/models"

	"golang.org/x/sync/errgroup"
)

type HTTPClient struct {
	client  *http.Client
	config  config.ScrapeConfig
	regexes map[string]*regexp.Regexp
}

func NewHTTPClient() *HTTPClient {
	cfg := config.DefaultScrapeConfig()
	regexes := config.CompileRegexes()

	// Configure HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.TimeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow up to MaxRedirects redirects
			if len(via) >= MaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	return &HTTPClient{
		client:  client,
		config:  cfg,
		regexes: regexes,
	}
}

// setRequestHeaders sets browser-like headers on the request
func (h *HTTPClient) setRequestHeaders(req *http.Request) {
	req.Header.Set("User-Agent", h.config.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", "https://www.google.com/")
}

// retryWithBackoff implements exponential backoff for retries
func (h *HTTPClient) retryWithBackoff(ctx context.Context, targetURL string, retryCount int) (string, error) {
	if retryCount >= h.config.MaxRetries {
		return "", fmt.Errorf("max retries exceeded")
	}

	delay := time.Duration(1000*(1<<retryCount)) * time.Millisecond
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}

	time.Sleep(delay)
	return h.FetchHTML(ctx, targetURL, retryCount+1)
}

// FetchHTML fetches HTML content from a URL with retry logic
func (h *HTTPClient) FetchHTML(ctx context.Context, targetURL string, retryCount int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to mimic a real browser
	h.setRequestHeaders(req)

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Classify Cloudflare challenge/block pages from a bounded prefix of the body
		// before deciding to retry: a 503/403 challenge will not go away on retry.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if v := DetectCloudflare(resp.StatusCode, resp.Header, string(snippet)); v != CFNone {
			return "", &models.CloudflareBlockError{
				Domain: req.URL.Hostname(), Kind: v.String(), Status: resp.StatusCode,
				RayID: cfRayID(resp.Header), Err: fmt.Errorf("HTTP %d", resp.StatusCode),
			}
		}
		// A CDN can keep serving an origin's transient 404 for minutes (pandaily's
		// origin sends max-age=300 on them); a unique query string skips that copy
		if resp.StatusCode == http.StatusNotFound && retryCount == 0 && looksCDNServed(resp.Header) {
			return h.retryUncached404(ctx, targetURL)
		}
		// Handle 5xx server errors with retry logic
		if resp.StatusCode >= 500 {
			return h.retryWithBackoff(ctx, targetURL, retryCount)
		}
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return "", fmt.Errorf("non-HTML content-type: %s", contentType)
	}

	// Read response body with size limit
	reader := io.LimitReader(resp.Body, int64(h.config.SizeLimitBytes))
	body, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if v := DetectCloudflare(resp.StatusCode, resp.Header, string(body)); v != CFNone {
		return "", &models.CloudflareBlockError{
			Domain: req.URL.Hostname(), Kind: v.String(), Status: resp.StatusCode,
			RayID: cfRayID(resp.Header), Err: fmt.Errorf("HTTP %d with Cloudflare %s page", resp.StatusCode, v),
		}
	}

	return string(body), nil
}

// retryUncached404 refetches a CDN-served 404 past the cache, at once and again
// after a pause, since the origin's 404s come in bursts of a few seconds
func (h *HTTPClient) retryUncached404(ctx context.Context, targetURL string) (string, error) {
	for _, delay := range []time.Duration{0, 3 * time.Second} {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", fmt.Errorf("HTTP 404")
		}
		html, err := h.FetchHTML(ctx, addCacheBuster(targetURL), 1)
		if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			return html, err
		}
	}
	return "", fmt.Errorf("HTTP 404")
}

// looksCDNServed reports whether a response came through a caching CDN
func looksCDNServed(header http.Header) bool {
	return header.Get("Cf-Cache-Status") != "" || header.Get("Age") != "" ||
		header.Get("X-Cache") != "" || strings.EqualFold(header.Get("Server"), "cloudflare")
}

// addCacheBuster adds a unique query parameter so a CDN treats the URL as new.
// The existing query is kept byte for byte: re-encoding it would drop
// ';'-separated pairs and turn "?flag" into "?flag="
func addCacheBuster(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	buster := "_cb=" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if u.RawQuery == "" {
		u.RawQuery = buster
	} else {
		u.RawQuery += "&" + buster
	}
	return u.String()
}

// LooksLikeCFBlock checks if HTML content is a Cloudflare challenge/block page.
// It must not match normal pages that merely reference Cloudflare assets.
func (h *HTTPClient) LooksLikeCFBlock(html string) bool {
	return DetectCloudflareHTML(html) != CFNone
}

// GenerateAlternateURLs creates alternative URLs for AMP/mobile fallback
func (h *HTTPClient) GenerateAlternateURLs(originalURL string) ([]string, error) {
	u, err := url.Parse(originalURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	alternates := make([]string, 0, 4)

	// AMP prefix (/amp/path)
	if !strings.HasPrefix(u.Path, "/amp/") {
		ampURL := *u
		ampURL.Path = "/amp" + u.Path
		alternates = append(alternates, ampURL.String())
	}

	// AMP suffix (/path/amp)
	if !strings.HasSuffix(u.Path, "/amp") {
		ampURL := *u
		if strings.HasSuffix(ampURL.Path, "/") {
			ampURL.Path = strings.TrimSuffix(ampURL.Path, "/") + "/amp"
		} else {
			ampURL.Path = ampURL.Path + "/amp"
		}
		alternates = append(alternates, ampURL.String())
	}

	// Query AMP
	queryURL := *u
	queryURL.RawQuery = queryURL.Query().Encode()
	if queryURL.RawQuery != "" {
		queryURL.RawQuery += "&outputType=amp"
	} else {
		queryURL.RawQuery = "outputType=amp"
	}
	alternates = append(alternates, queryURL.String())

	// m. subdomain
	if !strings.HasPrefix(u.Hostname(), "m.") {
		mobileURL := *u
		mobileURL.Host = "m." + u.Hostname()
		alternates = append(alternates, mobileURL.String())
	}

	return alternates, nil
}

// FetchWithAlternates tries the primary URL first, then alternates in parallel
func (h *HTTPClient) FetchWithAlternates(ctx context.Context, targetURL string) (string, string, error) {
	// Try primary URL first
	html, err := h.FetchHTML(ctx, targetURL, 0)
	if err == nil && !h.LooksLikeCFBlock(html) {
		return html, targetURL, nil
	}

	// Check if we should try alternates (only for specific errors)
	if err != nil && !strings.Contains(err.Error(), "HTTP 403") &&
		!strings.Contains(err.Error(), "HTTP 406") &&
		!strings.Contains(err.Error(), "HTTP 451") &&
		!strings.Contains(err.Error(), "HTTP 5") {
		return "", "", err
	}

	// Generate alternate URLs
	alternates, err := h.GenerateAlternateURLs(targetURL)
	if err != nil {
		return "", "", err
	}

	// Try alternates in parallel
	var wg sync.WaitGroup
	resultChan := make(chan struct {
		html string
		url  string
		err  error
	}, len(alternates))

	for _, altURL := range alternates {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			html, err := h.FetchHTML(ctx, url, 0)
			if err == nil && !h.LooksLikeCFBlock(html) {
				resultChan <- struct {
					html string
					url  string
					err  error
				}{html, url, nil}
			} else {
				resultChan <- struct {
					html string
					url  string
					err  error
				}{"", "", err}
			}
		}(altURL)
	}

	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Check results as they come in
	for result := range resultChan {
		if result.err == nil && result.html != "" {
			return result.html, result.url, nil
		}
	}

	return "", "", fmt.Errorf("all alternate URLs failed or were blocked")
}

// FetchWithAlternatesGroup uses errgroup for better error handling
func (h *HTTPClient) FetchWithAlternatesGroup(ctx context.Context, targetURL string) (string, string, error) {
	// Check if parent context is already expired
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("HTTP fetch canceled: parent context expired before starting")
	}

	// Try primary URL first
	html, err := h.FetchHTML(ctx, targetURL, 0)
	if err == nil && !h.LooksLikeCFBlock(html) && len(html) > 0 {
		// Validate HTML has minimum content
		if len(strings.TrimSpace(html)) > 100 {
		return html, targetURL, nil
		}
		// HTML is too short, likely not a real page - fall through to alternates
	}

	// A Cloudflare challenge/block applies to the whole zone: same-site alternates
	// (/amp, ?outputType=amp, m.) get the same challenge, so return the typed error now.
	var cfErr *models.CloudflareBlockError
	if errors.As(err, &cfErr) {
		return "", "", err
	}

	// Check if we should try alternates
	if err != nil && !strings.Contains(err.Error(), "HTTP 403") &&
		!strings.Contains(err.Error(), "HTTP 406") &&
		!strings.Contains(err.Error(), "HTTP 451") &&
		!strings.Contains(err.Error(), "HTTP 5") {
		// Check if error is due to parent context expiration
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("HTTP fetch failed: parent context expired: %w", ctx.Err())
		}
		return "", "", err
	}

	// Generate alternate URLs
	alternates, err := h.GenerateAlternateURLs(targetURL)
	if err != nil {
		return "", "", err
	}

	if len(alternates) == 0 {
		// Check parent context before returning
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("HTTP fetch failed: parent context expired: %w", ctx.Err())
		}
		return "", "", fmt.Errorf("HTTP fetch failed: %w", err)
	}

	// Use errgroup for parallel execution
	// Use errgroup context but check parent context explicitly to avoid canceling parent
	g, groupCtx := errgroup.WithContext(ctx)
	resultChan := make(chan struct {
		html string
		url  string
		err  error
	}, 1)

	for _, altURL := range alternates {
		altURL := altURL // capture loop variable
		g.Go(func() error {
			// Check parent context before starting
			if ctx.Err() != nil {
				return nil // Parent expired
			}
			
			// Use parent context (not group context) for the actual fetch
			// This way parent expiration is independent of errgroup cancellation
			html, fetchErr := h.FetchHTML(ctx, altURL, 0)
			
			// Check parent context after fetch
			if ctx.Err() != nil {
				return nil // Parent expired during fetch
			}

			if fetchErr == nil && !h.LooksLikeCFBlock(html) && len(html) > 0 {
				// Validate HTML has minimum content before treating as success
				if len(strings.TrimSpace(html)) > 100 {
					// Send successful result (non-blocking)
				select {
				case resultChan <- struct {
					html string
					url  string
					err  error
				}{html, altURL, nil}:
				case <-ctx.Done():
					// Parent expired while sending
				case <-groupCtx.Done():
						// Group context expired (another routine might have succeeded or failed)
					}
				}
			}
			return nil
		})
	}

	// Wait for first successful result
	go func() {
		g.Wait()
		close(resultChan)
	}()

	select {
	case result := <-resultChan:
		if result.err == nil && result.html != "" {
			return result.html, result.url, nil
		}
	case <-ctx.Done():
		// Parent context expired while waiting
		return "", "", fmt.Errorf("HTTP fetch failed: parent context expired during alternate URL attempts: %w", ctx.Err())
	}

	// All alternates failed
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("HTTP fetch failed: parent context expired, all alternate URLs failed: %w", ctx.Err())
	}
	return "", "", fmt.Errorf("HTTP fetch failed: all alternate URLs failed or were blocked")
}
