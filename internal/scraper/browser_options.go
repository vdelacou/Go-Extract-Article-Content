// Package scraper provides browser configuration options for Chrome automation.
package scraper

import (
	"extract-html-scraper/internal/config"

	"github.com/chromedp/chromedp"
)

// BrowserOptions contains configuration for browser automation
type BrowserOptions struct {
	Optimized    bool
	BlockImages  bool
	BlockJS      bool
	BlockFonts   bool
	BlockCSS     bool
	WindowWidth  int
	WindowHeight int
	UserAgent    string
}

// DefaultBrowserOptions returns standard browser options
func DefaultBrowserOptions() BrowserOptions {
	return BrowserOptions{
		Optimized:    false,
		BlockImages:  false,
		BlockJS:      false,
		BlockFonts:   false,
		BlockCSS:     false,
		WindowWidth:  DefaultWindowWidth,
		WindowHeight: DefaultWindowHeight,
		UserAgent:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
}

// OptimizedBrowserOptions returns optimized browser options for faster scraping
func OptimizedBrowserOptions() BrowserOptions {
	return BrowserOptions{
		Optimized:    true,
		BlockImages:  true,
		BlockJS:      false, // Keep JS for dynamic content
		BlockFonts:   true,
		BlockCSS:     true,
		WindowWidth:  DefaultWindowWidth,
		WindowHeight: DefaultWindowHeight,
	}
}

// BuildChromeOptions creates Chrome options based on BrowserOptions
func BuildChromeOptions(opts BrowserOptions) []chromedp.ExecAllocatorOption {
	chromeOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-web-security", true),
		// HttpsUpgrades: its 3 s fallback timer aborts http:// navigations to slow
		// https hosts with net::ERR_BLOCKED_BY_CLIENT (GeekPark)
		chromedp.Flag("disable-features", "VizDisplayCompositor,IsolateOrigins,site-per-process,HttpsUpgrades"),
		chromedp.WindowSize(opts.WindowWidth, opts.WindowHeight),
		// Enhanced stealth flags
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("exclude-switches", "enable-automation"),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-domain-reliability", true),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("disable-wp-input-check", true),
		chromedp.Flag("disable-wp-input-validation", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
	)

	// Run the browser whose version the user agent names
	if bin := config.ChromeBinary(); bin != "" {
		chromeOpts = append(chromeOpts, chromedp.ExecPath(bin))
	}

	// Add user agent if provided
	if opts.UserAgent != "" {
		chromeOpts = append(chromeOpts, chromedp.UserAgent(opts.UserAgent))
	}

	// Add optimization flags
	if opts.Optimized {
		if opts.BlockImages {
			chromeOpts = append(chromeOpts, chromedp.Flag("disable-images", true))
		}
		if opts.BlockJS {
			chromeOpts = append(chromeOpts, chromedp.Flag("disable-javascript", true))
		}
		chromeOpts = append(chromeOpts,
			chromedp.Flag("disable-plugins", true),
			chromedp.Flag("disable-extensions", true),
		)
	}

	return chromeOpts
}
