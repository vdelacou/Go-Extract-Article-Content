package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsHTTPURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://www.scmp.com/tech/article/3368357/china-eyes-chip-industry?utm_source=rss_feed", true},
		{"http://www.geekpark.net/news/371040", true},
		{"HTTPS://pandaily.com/unitree-g1", true},
		// what a client sent on Oct 2 when it lost the article URL
		{"undefined", false},
		{"null", false},
		{"www.tvinsider.com/1215678/tulsa-king-season-4-cast-premiere-date-details/", false},
		{"/1215678/tulsa-king-season-4-cast-premiere-date-details/", false},
		{"file:///etc/passwd", false},
		{"javascript:alert(1)", false},
		{"https://", false},
		{"http://:8080/path", false},
	}

	for _, c := range cases {
		if got := isHTTPURL(c.raw); got != c.want {
			t.Errorf("isHTTPURL(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestHandlerRejectsRelativeURL(t *testing.T) {
	// A zero handler has no API keys, so the request reaches URL validation. It has
	// no scraper either, so it must answer before scraping starts
	h := &CloudRunHandler{}
	rec := httptest.NewRecorder()
	h.Handler(rec, httptest.NewRequest(http.MethodGet, "/?url=undefined", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "Invalid URL format") {
		t.Errorf("body = %s, want the invalid URL error", rec.Body.String())
	}
}
