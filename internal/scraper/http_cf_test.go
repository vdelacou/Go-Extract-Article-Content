package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"extract-html-scraper/internal/models"
)

func serveFixture(t *testing.T, file string, status int, hdr map[string]string) (*httptest.Server, *int32, int) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "cloudflare", file))
	if err != nil {
		t.Fatal(err)
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, len(body)
}

// A normal Cloudflare-served article (contains "cloudflare", email-decode, insights beacon,
// jsd challenge-platform snippet) must be returned by Phase 1, not thrown away.
func TestFetchWithAlternatesGroup_NormalCloudflarePageIsAccepted(t *testing.T) {
	srv, hits, size := serveFixture(t, "ok-cnevpost-raw.html", 200, map[string]string{"Server": "cloudflare", "Cf-Ray": "x-ORD"})
	h := NewHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	html, _, err := h.FetchWithAlternatesGroup(ctx, srv.URL+"/2026/09/11/faw-vw-launch-id-aura-t6-sept-20/")
	if err != nil || len(html) != size {
		t.Fatalf("err=%v len=%d", err, len(html))
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("expected 1 request (no alternates), got %d", n)
	}
}

// A real managed challenge (403 + cf-mitigated: challenge) yields a typed error, no 5xx-style
// retries and no same-site alternates.
func TestFetchWithAlternatesGroup_ChallengeIsTyped(t *testing.T) {
	srv, hits, _ := serveFixture(t, "challenge-scmp-403-raw.html", 403, map[string]string{"Server": "cloudflare", "Cf-Mitigated": "challenge", "Cf-Ray": "a43c51e869d487ac-IAD"})
	h := NewHTTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := h.FetchWithAlternatesGroup(ctx, srv.URL+"/tech/article/3367998/x")
	var cf *models.CloudflareBlockError
	if !errors.As(err, &cf) || cf.Kind != "challenge" || cf.Status != 403 || cf.RayID != "a43c51e869d487ac-IAD" {
		t.Fatalf("want typed challenge error, got %#v (%v)", cf, err)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("expected 1 request, got %d", n)
	}
	if !IsCloudflareBlock(err) {
		t.Fatal("IsCloudflareBlock should be true for typed error")
	}
}

// Old-style JS challenge was a 503: must not be retried with backoff.
func TestFetchHTML_503ChallengeNotRetried(t *testing.T) {
	srv, hits, _ := serveFixture(t, "challenge-tvinsider-403-raw.html", 503, map[string]string{"Server": "cloudflare"})
	h := NewHTTPClient()
	start := time.Now()
	_, err := h.FetchHTML(context.Background(), srv.URL+"/", 0)
	if !IsCloudflareBlock(err) {
		t.Fatalf("want CF error, got %v", err)
	}
	if n := atomic.LoadInt32(hits); n != 1 || time.Since(start) > time.Second {
		t.Fatalf("retried: hits=%d elapsed=%v", n, time.Since(start))
	}
}

// Plain 403 from a non-Cloudflare origin is NOT a Cloudflare block (no 451).
func TestIsCloudflareBlock_PlainErrorsAreNotCloudflare(t *testing.T) {
	for _, e := range []error{
		errors.New("HTTP 403"),
		errors.New("HTTP fetch failed: all alternate URLs failed or were blocked"),
		errors.New(`Get "https://cdnjs.cloudflare.com/x.js": dial tcp: i/o timeout`),
		errors.New("all URLs failed or were blocked"),
	} {
		if IsCloudflareBlock(e) {
			t.Errorf("IsCloudflareBlock(%q) = true", e)
		}
	}
}
