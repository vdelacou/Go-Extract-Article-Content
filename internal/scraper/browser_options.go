// Package scraper provides browser configuration options for Chrome automation.
package scraper

import (
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
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor,IsolateOrigins,site-per-process"),
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

// GetRequestBlockingScript returns JavaScript for blocking unwanted requests
func GetRequestBlockingScript(opts BrowserOptions) string {
	script := `
		const originalFetch = window.fetch;
		const originalXHR = window.XMLHttpRequest;
		
		// Block ads and trackers
		const blockedDomains = [
			'doubleclick', 'googlesyndication', 'google-analytics',
			'facebook.com/tr', 'taboola', 'outbrain', 'scorecardresearch',
			'chartbeat', 'amazon-adsystem'
		];
		
		// Override fetch
		window.fetch = function(...args) {
			const url = args[0];
			if (typeof url === 'string' && blockedDomains.some(domain => url.includes(domain))) {
				return Promise.reject(new Error('Blocked'));
			}
			return originalFetch.apply(this, args);
		};
		
		// Override XMLHttpRequest
		const originalOpen = XMLHttpRequest.prototype.open;
		XMLHttpRequest.prototype.open = function(method, url, ...args) {
			if (typeof url === 'string' && blockedDomains.some(domain => url.includes(domain))) {
				throw new Error('Blocked');
			}
			return originalOpen.apply(this, [method, url, ...args]);
		};
		
		// Anti-detection: Hide webdriver property (ALWAYS, not just in optimized mode)
		Object.defineProperty(navigator, 'webdriver', {
			get: () => false,
			configurable: true
		});
		
		// Additional stealth measures
		Object.defineProperty(navigator, 'plugins', {
			get: () => [1, 2, 3, 4, 5]
		});
		
		// Override permissions
		const originalQuery = window.navigator.permissions.query;
		window.navigator.permissions.query = (parameters) => (
			parameters.name === 'notifications' ?
				Promise.resolve({ state: Notification.permission }) :
				originalQuery(parameters)
		);
		
		// Chrome object
		window.chrome = {
			runtime: {},
		};
		
		// Override languages
		Object.defineProperty(navigator, 'languages', {
			get: () => ['en-US', 'en']
		});
	`

	if opts.Optimized {
		script += `
		// Block resource types for optimized mode
		const originalCreateElement = document.createElement;
		document.createElement = function(tagName) {
			const element = originalCreateElement.call(this, tagName);
			if (['img', 'link', 'style'].includes(tagName.toLowerCase())) {
				element.style.display = 'none';
			}
			return element;
		};
		`
	}

	return script
}
