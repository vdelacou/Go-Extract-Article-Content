// Package scraper provides constants used throughout the scraping functionality.
package scraper

import "time"

// Timeout constants
const (
	HTTPTimeout    = 12 * time.Second  // Reduced from 18s - SCMP blocks HTTP anyway
	BrowserTimeout = 60 * time.Second  // Increased from 40s - SCMP needs more time
	DefaultTimeout = 15 * time.Second
)

// Content extraction selectors
const (
	ContentSelectors = "[data-module='ArticleBody'], [data-qa='article-body'], .article__body, .story__content-body, article, main, [role='main'], .content, .post-content, .entry-content, .article-content, .story-content"
	TextElements     = "p, h1, h2, h3, h4, h5, h6, li, blockquote"
	NonContentTags   = "script, style, nav, header, footer, aside"
)

// Meta tag properties
const (
	OGTitle       = "og:title"
	OGDescription = "og:description"
	OGImage       = "og:image"
	OGImageSecure = "og:image:secure_url"
	OGImageWidth  = "og:image:width"
	OGImageHeight = "og:image:height"
	TwitterTitle  = "twitter:title"
	TwitterDesc   = "twitter:description"
	MetaDesc      = "description"
)

// Text processing constants
const (
	DoubleNewline = "\n\n"
	SingleNewline = "\n"
	TripleNewline = "\n\n\n"
	DoubleSpace   = "  "
	SingleSpace   = " "
)

// Browser configuration
const (
	DefaultWindowWidth  = 1366
	DefaultWindowHeight = 900
	MaxRedirects        = 5
)

// Image processing constants
const (
	DefaultImageLimit = 3
	TargetImageWidth  = 1000
	MinDescriptionLen = 50
	MaxDescriptionLen = 300
)

// Blocked domains for browser requests
var BlockedDomains = []string{
	"doubleclick",
	"googlesyndication",
	"google-analytics",
	"facebook.com/tr",
	"taboola",
	"outbrain",
	"scorecardresearch",
	"chartbeat",
	"amazon-adsystem",
}

// Cloudflare detection patterns
var CloudflarePatterns = []string{
	"CF_BLOCKED",
	"cloudflare",
	"HTTP 403",
	"all alternate URLs failed",
	"attention required",
	"cloudflare ray id",
	"what can i do to resolve this?",
	"why have i been blocked?",
	"performance & security by cloudflare",
	"verifying you are human",
	"verify you are human",
	"checking your browser",
	"please wait while we verify",
	"this may take a few seconds",
}
