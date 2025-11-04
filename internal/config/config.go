// Package config provides configuration structures and defaults for the web scraper.
// It includes settings for image extraction, scraping behavior, and performance tuning.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// ImageConfig contains configuration for image extraction
type ImageConfig struct {
	MinShortSide   int
	MinArea        int
	MinAspect      float64
	MaxAspect      float64
	RatioWhitelist []float64
	RatioTol       float64
	AdSizes        map[string]bool
	BadHintRegex   string
}

// ScrapeConfig contains general scraping configuration
type ScrapeConfig struct {
	UserAgent      string
	TimeoutMs      int
	SizeLimitBytes int
	MaxRetries     int
	ChromeMajor    int
}

// DefaultImageConfig returns the default image extraction configuration
func DefaultImageConfig() ImageConfig {
	return ImageConfig{
		MinShortSide:   300,
		MinArea:        140000,
		MinAspect:      0.5,
		MaxAspect:      2.6,
		RatioWhitelist: []float64{1.333, 1.5, 1.6, 1.667, 1.777, 1.85, 2},
		RatioTol:       0.09,
		AdSizes: map[string]bool{
			"728x90": true, "970x90": true, "970x250": true, "468x60": true,
			"320x50": true, "300x50": true, "300x250": true, "336x280": true,
			"300x600": true, "160x600": true, "120x600": true, "250x250": true,
			"200x200": true, "180x150": true, "234x60": true, "120x240": true,
			"88x31": true,
		},
		BadHintRegex: `(sprite|icon|favicon|logo|avatar|emoji|placeholder|pixel|tracker|ads?|adserver|promo|beacon)`,
	}
}

// DefaultScrapeConfig returns the default scraping configuration
func DefaultScrapeConfig() ScrapeConfig {
	chromeMajor := 133
	if env := os.Getenv("CHROME_MAJOR"); env != "" {
		if parsed, err := strconv.Atoi(env); err == nil {
			chromeMajor = parsed
		}
	}

	userAgent := os.Getenv("SCRAPE_USER_AGENT")
	if userAgent == "" {
		userAgent = fmt.Sprintf("Mozilla/5.0 (Windows NT 10; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.6943.126 Safari/537.36", chromeMajor)
	}

	return ScrapeConfig{
		UserAgent:      userAgent,
		TimeoutMs:      15000,
		SizeLimitBytes: 6_000_000,
		MaxRetries:     2,
		ChromeMajor:    chromeMajor,
	}
}

// CompileRegexes pre-compiles regex patterns for better performance
func CompileRegexes() map[string]*regexp.Regexp {
	config := DefaultImageConfig()

	badHintRegex, _ := regexp.Compile("(?i)" + config.BadHintRegex)

	return map[string]*regexp.Regexp{
		"badHint":           badHintRegex,
		"imgTag":            regexp.MustCompile(`<img\b[^>]*>`),
		"srcAttr":           regexp.MustCompile(`(?:\s|^)(?:src|data-src|data-original|data-lazy-src)=["']([^"']+)["']`),
		"widthAttr":         regexp.MustCompile(`(?:^|\s)width=["']?(\d+)[^"'>]*`),
		"heightAttr":        regexp.MustCompile(`(?:^|\s)height=["']?(\d+)[^"'>]*`),
		"styleAttr":         regexp.MustCompile(`style=["']([^"']+)["']`),
		"widthStyle":        regexp.MustCompile(`(?:^|;|\s)width\s*:\s*(\d+(?:\.\d+)?)px\b`),
		"heightStyle":       regexp.MustCompile(`(?:^|;|\s)height\s*:\s*(\d+(?:\.\d+)?)px\b`),
		"srcsetAttr":        regexp.MustCompile(`srcset=["']([^"']+)["']`),
		"srcsetItem":        regexp.MustCompile(`(\S+)\s+(\d+)w`),
		"dimensionsFromUrl": regexp.MustCompile(`(?:^|[^\d])(\d{3,4})x(\d{3,4})(?:[^\d]|$)`),
		"widthFromUrl":      regexp.MustCompile(`[?&](?:w|width)=(\d{3,4})\b`),
		"heightFromUrl":     regexp.MustCompile(`[?&](?:h|height)=(\d{3,4})\b`),
		"imageExt":          regexp.MustCompile(`\.(jpe?g|png|gif|webp|avif)(?:$|[?#])`),
		"ogImage":           regexp.MustCompile(`<meta[^>]*property=["']og:image(?::secure_url)?["'][^>]*content=["']([^"']+)["']`),
		"ogWidth":           regexp.MustCompile(`<meta[^>]*property=["']og:image:width["'][^>]*content=["']([^"']+)["']`),
		"ogHeight":          regexp.MustCompile(`<meta[^>]*property=["']og:image:height["'][^>]*content=["']([^"']+)["']`),
		"articleTag":        regexp.MustCompile(`<(article|main)[\s>]`),
		"closeArticleTag":   regexp.MustCompile(`</(article|main)>`),
		"cfBlock":           regexp.MustCompile(`(attention required|cloudflare ray id|what can i do to resolve this\?|why have i been blocked\?|performance & security by cloudflare|verifying you are human|verify you are human|checking your browser|please wait while we verify|this may take a few seconds)`),
		"appError":          regexp.MustCompile(`(?i)(application error|client-side exception|javascript error|an error occurred|something went wrong)`),
	}
}
