package scraper

import (
	"net/http"
	"regexp"
	"strings"
)

// CFVerdict classifies a response with respect to Cloudflare bot protection.
type CFVerdict int

const (
	// CFNone means the response is not a Cloudflare challenge or block page.
	// Normal pages that merely load Cloudflare assets (email-decode, rocket-loader,
	// insights beacon, the bot-management "jsd" snippet, cdnjs) are CFNone.
	CFNone CFVerdict = iota
	// CFChallenge is an interstitial challenge ("Just a moment...", managed/JS/interactive).
	// A real browser may pass it after a few seconds.
	CFChallenge
	// CFBlocked is a hard block (WAF "Attention Required! | Cloudflare", 1020 access denied,
	// 1010 browser signature ban, 1015 rate limited). Waiting does not help.
	CFBlocked
)

func (v CFVerdict) String() string {
	switch v {
	case CFChallenge:
		return "challenge"
	case CFBlocked:
		return "block"
	default:
		return "none"
	}
}

// cfMaxInspectBytes bounds the body-marker scan. Raw challenge pages are 3-7 KB,
// Chrome-rendered challenge DOMs are ~29 KB, block pages are < 10 KB.
const cfMaxInspectBytes = 128 << 10

var (
	cfTitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	// Block-class error codes only (1016 origin DNS, 1003 direct IP etc. are not bot blocks).
	cfPlainErrCodeRe = regexp.MustCompile(`^\s*error code: (1005|1006|1007|1008|1009|1010|1012|1015|1020)\s*$`)
	// Orchestrate script of the challenge platform (chl_page, jsch, managed, captcha).
	// NOTE: normal pages load /cdn-cgi/challenge-platform/scripts/jsd/main.js (or
	// /cdn-cgi/challenge-platform/h/<x>/scripts/jsd/...) - that is bot management telemetry, NOT a challenge.
	cfOrchestrateRe = regexp.MustCompile(`/cdn-cgi/challenge-platform/h/[a-z]/orchestrate/`)
)

// Phrases that, together with id="cf-error-details", identify a Cloudflare *block* page
// (as opposed to an origin error page such as 52x / 1016 which shares the template).
var cfBlockPhrases = []string{
	"sorry, you have been blocked",
	`data-translate="block_headline"`,
	"<span>1020</span>",
	"<span>1010</span>",
	"<span>1012</span>",
	"<span>1015</span>",
	"error 1020",
	"error 1010",
	"error 1015",
	"you are being rate limited",
	"used cloudflare to restrict access",
}

// DetectCloudflare classifies an HTTP response (status, headers, body).
// status and header may be zero/nil when only HTML is available (browser DOM).
func DetectCloudflare(status int, header http.Header, body string) CFVerdict {
	if header != nil {
		// Cloudflare sets "cf-mitigated: challenge" on every challenge response.
		if strings.EqualFold(strings.TrimSpace(header.Get("Cf-Mitigated")), "challenge") {
			return CFChallenge
		}
		// Non-HTML clients get a bare "error code: 1020" text body on blocks.
		if status >= 400 && len(body) < 64 && cfPlainErrCodeRe.MatchString(body) &&
			strings.EqualFold(header.Get("Server"), "cloudflare") {
			return CFBlocked
		}
	}
	return DetectCloudflareHTML(body)
}

// DetectCloudflareHTML classifies an HTML document (raw response body or browser DOM).
func DetectCloudflareHTML(html string) CFVerdict {
	if html == "" {
		return CFNone
	}
	head := html
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	if m := cfTitleRe.FindStringSubmatch(head); m != nil {
		title := strings.ToLower(strings.TrimSpace(m[1]))
		switch {
		case title == "attention required! | cloudflare",
			strings.HasPrefix(title, "access denied |") && strings.Contains(title, "cloudflare"):
			return CFBlocked
		case title == "just a moment..." || title == "just a moment…" ||
			title == "please wait... | cloudflare":
			// Title alone is strong, but require a Cloudflare marker so a page that is
			// legitimately titled "Just a moment..." is not discarded.
			if strings.Contains(html, "_cf_chl_opt") || strings.Contains(html, "challenges.cloudflare.com") ||
				strings.Contains(html, "/cdn-cgi/challenge-platform/") {
				return CFChallenge
			}
		}
	}
	// Body markers only on small documents: challenge/block pages are small,
	// articles that merely mention these strings are large.
	if len(html) > cfMaxInspectBytes {
		return CFNone
	}
	if strings.Contains(html, "window._cf_chl_opt") || cfOrchestrateRe.MatchString(html) {
		return CFChallenge
	}
	lower := strings.ToLower(html)
	if strings.Contains(lower, `id="cf-error-details"`) {
		for _, p := range cfBlockPhrases {
			if strings.Contains(lower, p) {
				return CFBlocked
			}
		}
	}
	return CFNone
}

// cfRayID extracts the cf-ray header value (for logging / error messages).
func cfRayID(header http.Header) string {
	if header == nil {
		return ""
	}
	return header.Get("Cf-Ray")
}
