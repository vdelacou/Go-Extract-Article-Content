package scraper

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loadHeaders parses a `curl -D` dump, keeping only the last response block
// (a proxied fetch starts with "HTTP/1.1 200 Connection Established").
func loadHeaders(t *testing.T, path string) (int, http.Header) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	status, h := 0, http.Header{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "HTTP/") {
			status, h = 0, http.Header{}
			if parts := strings.Fields(line); len(parts) >= 2 {
				status, _ = strconv.Atoi(parts[1])
			}
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			h.Add(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	return status, h
}

func TestDetectCloudflareFixtures(t *testing.T) {
	dir := filepath.Join("testdata", "cloudflare")
	cases := []struct {
		file   string
		status int // used when there is no .headers.txt
		want   CFVerdict
	}{
		// Real Cloudflare managed challenges (proxied curl, 403 + cf-mitigated: challenge)
		{"challenge-scmp-403-raw.html", 0, CFChallenge},
		{"challenge-tvinsider-403-raw.html", 0, CFChallenge},
		// Real Chrome DOM of the challenge (before and after the orchestrate script rendered it)
		{"challenge-tvinsider-chrome-dom-initial.html", 403, CFChallenge},
		{"challenge-tvinsider-chrome-dom-rendered.html", 403, CFChallenge},
		{"challenge-scmp-chrome-dom-rendered.html", 403, CFChallenge},
		// Synthetic block pages (Cloudflare public templates)
		{"block-attention-required-SYNTHETIC.html", 403, CFBlocked},
		{"block-access-denied-1020-SYNTHETIC.html", 403, CFBlocked},
		// Normal pages served via Cloudflare, trimmed to every fragment that
		// mentions it (must NOT be flagged)
		{"ok-cnevpost-raw.html", 0, CFNone},
		{"ok-scmp-raw.html", 0, CFNone},
		{"ok-tvinsider-raw.html", 0, CFNone},
		{"ok-scmp-404-raw.html", 0, CFNone},
		{"ok-cnevpost-chrome-dom.html", 200, CFNone},
		{"ok-scmp-chrome-dom.html", 200, CFNone},
		{"ok-tvinsider-chrome-dom.html", 200, CFNone},
		{"ok-origin-error-522-SYNTHETIC.html", 522, CFNone},
		{"ok-article-about-cloudflare-SYNTHETIC.html", 200, CFNone},
		// Quotes the challenge's script and config in <pre>, <p> and JSON-LD
		{"ok-article-quoting-challenge-markers-SYNTHETIC.html", 200, CFNone},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			body := string(b)
			status, h := loadHeaders(t, filepath.Join(dir, strings.TrimSuffix(tc.file, ".html")+".headers.txt"))
			if status == 0 {
				status = tc.status
			}
			got := DetectCloudflare(status, h, body)
			gotHTMLOnly := DetectCloudflareHTML(body)
			if got != tc.want {
				t.Errorf("DetectCloudflare = %v, want %v", got, tc.want)
			}
			// Browser path has no headers: the HTML-only verdict must agree on blocked/not-blocked.
			if (gotHTMLOnly == CFNone) != (tc.want == CFNone) {
				t.Errorf("DetectCloudflareHTML = %v, want blocked=%v", gotHTMLOnly, tc.want != CFNone)
			}
		})
	}
}

func TestDetectCloudflarePlainErrorCode(t *testing.T) {
	h := http.Header{}
	h.Set("Server", "cloudflare")
	h.Set("Content-Type", "text/plain; charset=UTF-8")
	if got := DetectCloudflare(403, h, "error code: 1020"); got != CFBlocked {
		t.Fatalf("got %v want block", got)
	}
	// 1003 (direct IP access, real response captured in this investigation) and 1016 (origin DNS)
	// are Cloudflare errors but not bot blocks.
	for _, body := range []string{"error code: 1003", "error code: 1016"} {
		if got := DetectCloudflare(403, h, body); got != CFNone {
			t.Fatalf("%q: got %v want none", body, got)
		}
	}
	// Same body from a non-Cloudflare server must not be attributed to Cloudflare.
	h.Set("Server", "nginx")
	if got := DetectCloudflare(403, h, "error code: 1020"); got != CFNone {
		t.Fatalf("got %v want none", got)
	}
}

func TestDetectCloudflareTitleNeedsMarker(t *testing.T) {
	page := `<html><head><title>Just a moment...</title></head><body><article>` +
		strings.Repeat("<p>A real article whose headline is Just a moment...</p>", 50) + `</article></body></html>`
	if got := DetectCloudflareHTML(page); got != CFNone {
		t.Fatalf("got %v want none", got)
	}
}
