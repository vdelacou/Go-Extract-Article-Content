package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRetriesCDNCached404WithCacheBuster(t *testing.T) {
	article, err := os.ReadFile(filepath.Join("testdata", "pandaily", "unitree-g1-200.html"))
	if err != nil {
		t.Fatal(err)
	}
	notFound, err := os.ReadFile(filepath.Join("testdata", "pandaily", "agibot-cached-404.html"))
	if err != nil {
		t.Fatal(err)
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Query().Get("_cb") == "" {
			// Cloudflare's cached copy of an origin 404
			w.Header().Set("Cf-Cache-Status", "HIT")
			w.Header().Set("Age", "194")
			w.WriteHeader(http.StatusNotFound)
			w.Write(notFound)
			return
		}
		w.Header().Set("Cf-Cache-Status", "MISS")
		w.Write(article)
	}))
	defer srv.Close()

	target := srv.URL + "/unitree-g1-unifolm-x2-autonomous-sparring-world-model"
	html, finalURL, err := NewHTTPClient().FetchWithAlternatesGroup(context.Background(), target)
	if err != nil {
		t.Fatalf("expected the article after a cache-busting retry, got %v", err)
	}
	if finalURL != target {
		t.Errorf("final URL should stay the requested one, got %s", finalURL)
	}
	if len(html) != len(article) || atomic.LoadInt32(&hits) != 2 {
		t.Errorf("got %d bytes after %d requests, want %d bytes after 2", len(html), hits, len(article))
	}
}

func TestFetchGenuineCDN404StopsAfterTwoRetries(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Cf-Cache-Status", "MISS")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	start := time.Now()
	_, _, err := NewHTTPClient().FetchWithAlternatesGroup(context.Background(), srv.URL+"/gone")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("want HTTP 404, got %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 3 || time.Since(start) > 5*time.Second {
		t.Errorf("hits=%d elapsed=%v, want 3 hits within 5s", n, time.Since(start))
	}
}

func TestFetchPlain404IsNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := NewHTTPClient().FetchWithAlternatesGroup(context.Background(), srv.URL+"/gone")
	if err == nil || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("want one request and an error, got hits=%d err=%v", hits, err)
	}
}
