package scraper

// Page-level helpers for the browser phase. Each wait ends as soon as its
// condition holds, and every read of the page goes through Runtime.evaluate
// under a timeout: chromedp's OuterHTML waits on its own DOM tracking and
// blocked for up to 52 s on ad-heavy pages such as tvinsider's.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// URL patterns Chrome never needs to render an article's HTML. Cloudflare's
// challenge hosts (challenges.cloudflare.com, /cdn-cgi/) are deliberately absent
var (
	blockedImagePatterns = []string{"*.jpg*", "*.jpeg*", "*.png*", "*.gif*", "*.webp*", "*.avif*", "*.ico*"}
	blockedFontPatterns  = []string{"*.woff*", "*.ttf*", "*.otf*", "*.eot*"}
	blockedMediaPatterns = []string{"*.mp4*", "*.webm*", "*.m3u8*", "*.mp3*"}
)

// blockedURLPatterns lists what the browser should not fetch for these options
func blockedURLPatterns(opts BrowserOptions) []string {
	var patterns []string
	for _, domain := range BlockedDomains {
		patterns = append(patterns, "*"+domain+"*")
	}
	if opts.BlockImages {
		patterns = append(patterns, blockedImagePatterns...)
		patterns = append(patterns, blockedMediaPatterns...)
	}
	if opts.BlockFonts {
		patterns = append(patterns, blockedFontPatterns...)
	}
	return patterns
}

// enableNetworkBlocking makes Chrome drop those requests at the network layer.
// Ad auctions otherwise hold the load event for 20 to 60 s
func enableNetworkBlocking(ctx context.Context, opts BrowserOptions) error {
	return chromedp.Run(ctx, network.Enable(), network.SetBlockedURLS(blockedURLPatterns(opts)))
}

// navigateNoWait starts a navigation and returns once the main document is
// committed, without waiting for the load event
func (b *BrowserClient) navigateNoWait(ctx context.Context, url string, maxWait time.Duration) error {
	navCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	return chromedp.Run(navCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, errText, err := page.Navigate(url).Do(ctx)
		if err != nil {
			return err
		}
		if errText != "" {
			return fmt.Errorf("page load error %s", errText)
		}
		return nil
	}))
}

// documentStatus reads the main document's HTTP status from the page itself
// (PerformanceNavigationTiming.responseStatus, Chrome 109+); 0 when unknown
func (b *BrowserClient) documentStatus(ctx context.Context) int64 {
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var status int64
	_ = chromedp.Run(statusCtx, chromedp.Evaluate(`(() => {
		const nav = performance.getEntriesByType('navigation')[0];
		return nav && nav.responseStatus ? nav.responseStatus : 0;
	})()`, &status))
	return status
}

// captureInline snapshots the document, or returns nil if the page can't
// answer within 5 s
func (b *BrowserClient) captureInline(ctx context.Context, stage string) *HTMLSnapshot {
	captureCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var doc struct {
		HTML string `json:"html"`
		URL  string `json:"url"`
	}
	err := chromedp.Run(captureCtx, chromedp.Evaluate(
		`({html: document.documentElement ? document.documentElement.outerHTML : '', url: location.href})`, &doc))
	if err != nil {
		return nil
	}
	length := len(strings.TrimSpace(doc.HTML))
	if length == 0 {
		return nil
	}
	return &HTMLSnapshot{HTML: doc.HTML, URL: doc.URL, Timestamp: time.Now(), Stage: stage, Length: length}
}

// articleContainers are the elements whose text shows the article rendered
var articleContainers = []string{
	"[data-module='ArticleBody']", "[data-qa='article-body']", ".article__body", ".story__content-body",
	".article-body", ".article-content", ".story-body", ".post-body", ".entry-content", ".post-content",
	".story-content", "article", "main", "[role='main']", ".content",
}

var pageStateScript = func() string {
	selectors, _ := json.Marshal(articleContainers)
	return fmt.Sprintf(`(() => {
		let article = 0;
		for (const selector of %s) {
			const el = document.querySelector(selector);
			if (el) { article = Math.max(article, (el.textContent || '').trim().length); }
		}
		const body = document.body ? (document.body.innerText || '').length : 0;
		return {ready: document.readyState, article: article, body: body};
	})()`, selectors)
}()

// pageState is the page's load state and text lengths, read in one round trip
type pageState struct {
	Ready   string `json:"ready"`
	Article int    `json:"article"`
	Body    int    `json:"body"`
}

func (b *BrowserClient) readPageState(ctx context.Context) (pageState, error) {
	stateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var state pageState
	err := chromedp.Run(stateCtx, chromedp.Evaluate(pageStateScript, &state))
	return state, err
}

// sleepCtx sleeps for d and reports false if the context ended first
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// waitForArticleReady returns once an article container holds real text or
// the document has finished loading with some body text
func (b *BrowserClient) waitForArticleReady(ctx context.Context, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for {
		if state, err := b.readPageState(ctx); err == nil {
			if state.Article >= 500 || (state.Ready == "complete" && state.Body >= 200) {
				return true
			}
		}
		if time.Now().After(deadline) || !sleepCtx(ctx, 250*time.Millisecond) {
			return false
		}
	}
}

// waitForTextStability waits until the visible text stops growing: two reads
// in a row within 2% of the previous one
func (b *BrowserClient) waitForTextStability(ctx context.Context, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	previous, stableReads := -1, 0
	for time.Now().Before(deadline) {
		if state, err := b.readPageState(ctx); err == nil {
			if previous > 0 {
				change := state.Body - previous
				if change < 0 {
					change = -change
				}
				if float64(change) <= 0.02*float64(previous) {
					stableReads++
					if stableReads >= 2 {
						return true
					}
				} else {
					stableReads = 0
				}
			}
			previous = state.Body
		}
		if !sleepCtx(ctx, 300*time.Millisecond) {
			return false
		}
	}
	return false
}

// handleConsentOnce clicks one visible consent button, if any. Only buttons
// count, and "continue" doesn't: a "Continue reading" link once took the
// scrape to another page
func (b *BrowserClient) handleConsentOnce(ctx context.Context) bool {
	script := `(() => {
		const known = ["#onetrust-accept-btn-handler", "[data-testid='accept-button']", "[data-testid='consent-accept']",
			".scmp-consent-accept", ".consent-accept-button", "button[id*='accept']", "button[class*='accept']",
			".accept-all", ".cookie-accept", "[data-consent='accept']"];
		for (const selector of known) {
			const el = document.querySelector(selector);
			if (el && el.offsetParent !== null) { el.click(); return true; }
		}
		for (const el of document.querySelectorAll('button, [role="button"]')) {
			const text = (el.textContent || '').toLowerCase().trim();
			if (text.length < 40 && (text.includes('accept') || text.includes('agree') || text.includes('consent')) &&
				!text.includes('decline') && !text.includes('reject') && el.offsetParent !== null) {
				el.click();
				return true;
			}
		}
		return false;
	})()`
	clickCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var clicked bool
	if err := chromedp.Run(clickCtx, chromedp.Evaluate(script, &clicked)); err != nil || !clicked {
		return false
	}
	fmt.Printf("Consent dialog dismissed\n")
	sleepCtx(ctx, 500*time.Millisecond)
	return true
}

// quickScroll nudges lazy loaders. The depths stay at 500, 1000 and 1500 px:
// scrolling further pulled related-post thumbnails into cnevpost pages
func (b *BrowserClient) quickScroll(ctx context.Context) {
	for i := 1; i <= 3; i++ {
		_ = chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf("window.scrollTo(0, %d)", i*500), nil))
		if !sleepCtx(ctx, 200*time.Millisecond) {
			return
		}
	}
	_ = chromedp.Run(ctx, chromedp.Evaluate("window.scrollTo(0, 0)", nil))
}

// waitOutCloudflare polls a Cloudflare challenge, starting from the page's
// first snapshot, until it clears. A block page, or a challenge still showing
// when the wait ends, returns errCloudflarePersisted
func (b *BrowserClient) waitOutCloudflare(ctx context.Context, firstHTML string) error {
	v := DetectCloudflareHTML(firstHTML)
	if v == CFNone {
		return nil
	}
	if v == CFBlocked {
		fmt.Printf("Cloudflare block page detected; waiting will not help\n")
		return errCloudflarePersisted
	}

	cfWait := 15 * time.Second
	if remaining := calculateRemainingTime(ctx); remaining < cfWait+5*time.Second {
		cfWait = remaining - 5*time.Second
		if cfWait < 3*time.Second {
			cfWait = 3 * time.Second
		}
	}
	fmt.Printf("Cloudflare challenge detected, polling up to %v for resolution...\n", cfWait)
	start := time.Now()
	for time.Since(start) < cfWait {
		if !sleepCtx(ctx, time.Second) {
			return errCloudflarePersisted
		}
		snap := b.captureInline(ctx, "cf-check")
		if snap == nil {
			continue // the page is navigating away from the challenge
		}
		if DetectCloudflareHTML(snap.HTML) == CFNone {
			fmt.Printf("Challenge resolved after %v\n", time.Since(start).Round(100*time.Millisecond))
			return nil
		}
	}
	fmt.Printf("Challenge still present after %v; skipping remaining browser phases\n", cfWait)
	return errCloudflarePersisted
}
