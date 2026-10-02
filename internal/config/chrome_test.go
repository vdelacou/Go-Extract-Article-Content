package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChromeMajorOf(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]int{
		"Chromium 152.0.7977.82 Alpine Linux": 152,
		"Google Chrome 141.0.7390.122 ":       141,
		"not a browser":                       0,
	}
	for output, want := range cases {
		bin := filepath.Join(dir, "chromium")
		script := "#!/bin/sh\necho '" + output + "'\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := chromeMajorOf(bin); got != want {
			t.Errorf("chromeMajorOf(%q) = %d, want %d", output, got, want)
		}
	}
	if got := chromeMajorOf(filepath.Join(dir, "missing")); got != 0 {
		t.Errorf("missing binary: got %d, want 0", got)
	}
}

func TestChromeUserAgentMatchesLinuxChrome(t *testing.T) {
	ua := ChromeUserAgent(152)
	want := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	if ua != want {
		t.Fatalf("ChromeUserAgent(152) = %q, want %q", ua, want)
	}
	if strings.Contains(ua, "Headless") || strings.Contains(ua, "Windows") {
		t.Fatalf("user agent must not claim headless or another platform: %q", ua)
	}
}
