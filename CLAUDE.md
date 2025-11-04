# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a high-performance web scraper for article content extraction, deployed on Google Cloud Run. It's written in Go and optimized for speed, efficiency, and scalability. The service provides article extraction from URLs with a hybrid HTTP-first/browser-fallback strategy.

**Key Performance Goals:**
- 2-10x faster than Node.js alternatives
- 1-2s cold start times
- Sub-second HTTP scraping, 3-8s browser scraping
- Low memory footprint (1-2GB with browser)

## Common Commands

### Building and Running Locally

```bash
# Build the binary
go build -o cloudrun cmd/cloudrun/main.go

# Run locally
PORT=8080 ./cloudrun

# Or run with go run
PORT=8080 go run cmd/cloudrun/main.go

# Build with Docker
docker build -t extract-html-scraper .

# Run Docker container
docker run -p 8080:8080 extract-html-scraper
```

### Deployment

```bash
# Set your Google Cloud project
export GOOGLE_CLOUD_PROJECT="your-project-id"

# Deploy to Cloud Run (builds and deploys)
./deploy.sh

# Manage API keys
./manage-api-keys.sh set-env "key1,key2,key3"  # For dev/test
./manage-api-keys.sh set-secret "key1,key2"     # For production (Secret Manager)
./manage-api-keys.sh list                       # View current config
```

### Testing

```bash
# Test deployed service
./test.sh "SERVICE_URL" "API_KEY" "https://example.com"

# Test authenticated endpoint
./test-authenticated.sh

# Manual test
curl "https://your-service-url/?url=https://example.com&key=YOUR_API_KEY"

# Test with timeout parameter
curl "https://your-service-url/?url=https://example.com&key=YOUR_API_KEY&timeout=240000"
```

### Development

```bash
# Run tests (if any exist)
go test ./...

# Format code
go fmt ./...

# Tidy dependencies
go mod tidy

# View logs from Cloud Run
gcloud logs tail --follow --project=YOUR_PROJECT_ID
```

## Architecture

### High-Level Flow

The scraper uses a **two-phase hybrid strategy** with intelligent timeout management:

1. **Phase 1: HTTP Scraping** (cmd/cloudrun/main.go → internal/scraper/scraper.go → internal/scraper/http.go)
   - Fast HTTP-first approach with parallel alternate URL attempts
   - Tries 4 URL variants simultaneously (original, AMP, mobile, Google AMP cache)
   - Budget: Up to 80% of remaining timeout
   - Falls back to Phase 2 on failure or empty content

2. **Phase 2: Browser Automation** (internal/scraper/browser.go)
   - Full Chrome/Chromium automation via chromedp
   - Aggressive resource blocking (images, fonts, ads, analytics)
   - Cloudflare/challenge detection
   - Budget: Remaining time minus 5s buffer for cleanup
   - Returns HTTP 451 on Cloudflare blocks

### Core Components

**Entry Point:**
- `cmd/cloudrun/main.go` - Cloud Run HTTP handler with API key validation, request routing, timeout management

**Scraper Orchestration:**
- `internal/scraper/scraper.go` - Main orchestrator implementing the two-phase strategy with dynamic timeout budgeting
- `internal/scraper/http.go` - HTTP client with connection pooling, retry logic, concurrent alternate URL fetching
- `internal/scraper/browser.go` - chromedp browser automation with resource blocking and challenge detection

**Content Processing:**
- `internal/scraper/extractor.go` - Multi-strategy article extraction (go-readability, goquery custom selectors, metadata fallback)
- `internal/scraper/images.go` - Optimized image extraction with scoring algorithm (og:image priority, concurrent processing)
- `internal/scraper/content_scorer.go` - Content quality scoring for extraction strategy selection
- `internal/scraper/text_utils.go` - Text cleaning and normalization utilities

**Configuration:**
- `internal/config/config.go` - Centralized config with pre-compiled regexes for performance
- `internal/scraper/constants.go` - Timeout constants and thresholds
- `internal/scraper/browser_options.go` - Chrome flags and resource blocking configuration

**Models:**
- `internal/models/models.go` - Response structures (ScrapeResponse, Metadata, Quality scores)
- `internal/models/errors.go` - Custom error types (CloudflareBlockError)

### Timeout Budget Management

The system uses **context-based timeout budgeting** to ensure completion within Cloud Run's 5-minute limit:

- **Request timeout**: Default 300000ms (5 min), capped at 240000ms (4 min) in main.go:164
- **Phase 1 (HTTP)**: Allocated up to 80% of remaining budget
- **Phase 2 (Browser)**: Remaining time minus 5s cleanup buffer
- **Dynamic adjustment**: Each phase checks remaining time via context deadline and adjusts its timeout accordingly

See `scraper.go:31-51` for the timeout budget calculation logic.

### Cloudflare Detection

Cloudflare blocking is detected via regex patterns (config.go:102) and returned as HTTP 451 with structured error response. The system checks for:
- "attention required", "cloudflare ray id"
- "verify you are human", "checking your browser"
- Other common challenge page indicators

### Image Extraction

Images are extracted with a scoring algorithm that prioritizes:
1. OpenGraph images (og:image) with dimensions
2. Large content images (>300px short side, >140k area)
3. Good aspect ratios (0.5-2.6, with whitelist for common ratios)
4. Filters out ads, icons, trackers via regex and dimension checks

## Important Implementation Notes

### API Key Authentication

- API keys are validated in `cmd/cloudrun/main.go:90-107` using constant-time comparison to prevent timing attacks
- If no keys are configured, the service allows all requests (development mode)
- Keys can be loaded from:
  - Environment variable: `SCRAPER_API_KEYS` (comma-separated)
  - Google Secret Manager: `SCRAPER_API_KEY_SECRET` (not yet implemented, returns error)

### Error Handling

- Error sanitization happens in `main.go:236-284` to prevent leaking sensitive paths
- Set `VERBOSE_ERRORS=true` or `DEBUG=true` for full error messages (up to 500 chars)
- Cloudflare blocks return HTTP 451 with `BlockedResponse` structure
- Timeouts return HTTP 504
- Failed scrapes return HTTP 500 with sanitized error message

### Timeout Limits

- **Cloud Run maximum**: 300 seconds (5 minutes)
- **Safe maximum for requests**: 240 seconds (4 minutes) to account for overhead
- **Timeout capping**: Enforced in `main.go:164-166`
- Clients should request timeouts ≤240000ms for reliable completion

### Browser Automation Notes

- chromedp may log "ERROR: could not unmarshal event" warnings - these are harmless (see browser.go:57-61)
- They occur when Chrome uses newer protocol features that chromedp doesn't recognize
- These warnings don't affect functionality and cannot be easily suppressed

### Performance Optimizations

1. **Pre-compiled regexes**: All patterns compiled once at startup (config.go:76-105)
2. **Connection pooling**: HTTP client reuses connections (http.go:30-35)
3. **Concurrent processing**: Parallel alternate URLs, parallel image extraction
4. **Resource blocking**: Chrome blocks images, fonts, ads, analytics in Phase 2
5. **Single-pass parsing**: HTML parsed once with goquery, all data extracted in one pass

## Security Best Practices

From `.cursor/rules/snyk_rules.mdc`:
- Always run Snyk code scans for new or modified code in supported languages
- Fix security issues found by Snyk using the results context
- Rescan after fixes to ensure no newly introduced issues
- Repeat until no new issues are found

**Additional security considerations:**
- Never commit API keys or sensitive data to version control (see deploy.sh:14)
- Use Secret Manager for production API keys
- API keys validated with constant-time comparison (main.go:101)
- CORS headers set appropriately (main.go:113-115)
- Input validation on all URL parameters (main.go:140-150)

## Environment Variables

**Cloud Run Service:**
- `PORT` - Server port (default: 8080)
- `SCRAPER_API_KEYS` - Comma-separated API keys (for env-based auth)
- `SCRAPER_API_KEY_SECRET` - Secret Manager secret name (for Secret Manager auth)
- `SCRAPE_USER_AGENT` - Custom user agent string (optional)
- `CHROME_BIN` - Chrome binary path (auto-detected in container)
- `CHROME_MAJOR` - Chrome major version for user agent (default: 133)
- `VERBOSE_ERRORS` - Set to "true" for detailed error messages
- `DEBUG` - Set to "true" for debug mode

**Deployment:**
- `GOOGLE_CLOUD_PROJECT` - Your GCP project ID (required for deployment)
- `GOOGLE_CLOUD_REGION` - GCP region (default: us-central1)

## File Organization

```
cmd/cloudrun/         - Cloud Run entry point and HTTP handler
internal/
  config/            - Configuration and compiled regexes
  models/            - Response structures and custom errors
  scraper/           - Core scraping logic
    scraper.go       - Orchestrator with two-phase strategy
    http.go          - HTTP client with alternate URLs
    browser.go       - chromedp browser automation
    extractor.go     - Multi-strategy content extraction
    images.go        - Image extraction and scoring
    content_scorer.go - Quality scoring
    text_utils.go    - Text processing utilities
    *_options.go     - Configuration builders
    constants.go     - Timeout and threshold constants
```

## Cloud Run Configuration

Default settings in `deploy.sh`:
- Memory: 2Gi
- CPU: 2 vCPU
- Timeout: 300 seconds (5 minutes max)
- Concurrency: 10 requests per instance
- Max instances: 100
- Region: us-central1
- Public access: Enabled (--allow-unauthenticated)

## Dependencies

Key Go modules (see go.mod):
- `github.com/PuerkitoBio/goquery` - HTML parsing and CSS selectors
- `github.com/chromedp/chromedp` - Headless Chrome automation
- `github.com/go-shiori/go-readability` - Article extraction (primary strategy)
- `github.com/microcosm-cc/bluemonday` - HTML sanitization
- `golang.org/x/sync/errgroup` - Concurrent error handling

## Common Modifications

**Adjusting timeouts:**
- HTTP phase timeout: Change `HTTPTimeout` in `internal/scraper/constants.go`
- Browser phase timeout: Change `BrowserTimeout` in `internal/scraper/constants.go`
- Request timeout cap: Modify `main.go:164-166`

**Changing extraction strategy:**
- Edit `internal/scraper/extractor.go:ExtractArticleWithMultipleStrategies()`
- The function tries 4 strategies in order: JSON-LD structured data, go-readability, custom selectors, metadata fallback
- JSON-LD extraction (Strategy 0) provides fastest and most reliable extraction for news sites
- Each strategy returns a quality score; highest quality wins
- JSON-LD gets +10 quality bonus for reliability

**Modifying browser behavior:**
- Chrome flags: `internal/scraper/browser_options.go:BuildChromeOptions()`
- Resource blocking: `internal/scraper/browser_options.go:GetRequestBlockingScript()`
- Blocked resource types: images, stylesheets, fonts, media, analytics, ads

**Adding alternate URL patterns:**
- Modify `internal/scraper/http.go:generateAlternateURLs()` to add new URL variants
- Current variants: original, AMP, mobile, Google AMP cache

## Recent Improvements (SCMP & News Sites)

### JSON-LD Structured Data Extraction
- **File**: `internal/scraper/extractor_helpers.go:ExtractJSONLD()`
- Extracts content from schema.org `<script type="application/ld+json">` tags
- Works for SCMP, NYT, WSJ, Guardian, and most major news sites
- 3-5x faster than DOM-based extraction
- Often bypasses client-side paywalls (metadata outside paywall)
- Automatically used as Strategy 0 in multi-strategy extraction

### Paywall Handling
- **File**: `internal/scraper/browser.go:handlePaywall()`
- Detects and removes common paywall overlays
- SCMP-specific selectors: `[grid-area="paywall"]`, `.css-1kfpym9`
- Generic patterns: `[class*="paywall"]`, `.piano-template-modal`, etc.
- Runs automatically after page scroll in browser phase
- Restores body scroll when locked by paywalls
- **Note**: Only works for client-side paywalls; server-side paywalls require authentication

### Enhanced Content Selectors
- **File**: `internal/scraper/constants.go:ContentSelectors`
- Added data attribute selectors: `[data-module='ArticleBody']`, `[data-qa='article-body']`
- Added BEM-style patterns: `.article__body`, `.story__content-body`
- More resilient to CSS class name changes

### Extraction Strategy Order
1. **JSON-LD** - Parse structured data (fastest, most reliable)
2. **Readability** - go-readability algorithm (good for most articles)
3. **Simple** - Basic DOM selectors (fallback)
4. **Metadata-only** - Last resort (title/description only)

For detailed information, see `SCMP_IMPROVEMENTS.md`
