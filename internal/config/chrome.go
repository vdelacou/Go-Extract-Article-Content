package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// fallbackChromeMajor is the version the user agent claims when there is no
// browser to ask (local development without Chrome): the one Alpine 3.24 ships
const fallbackChromeMajor = 152

// chromeNames are the executables chromedp looks for on PATH, in its order
var chromeNames = []string{"headless_shell", "headless-shell", "chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"}

// ChromeBinary returns the browser the scraper drives: CHROME_BIN when it names
// a file, otherwise the first of chromedp's usual names on PATH, or "" if none
func ChromeBinary() string {
	if bin := os.Getenv("CHROME_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	for _, name := range chromeNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

var (
	chromeVersionRe = regexp.MustCompile(`(\d+)\.\d+\.\d+\.\d+`)

	installedMajor     int
	installedMajorOnce sync.Once
)

// InstalledChromeMajor returns the major version of the browser the scraper
// drives, read once from its --version output, or 0 when there is none
func InstalledChromeMajor() int {
	installedMajorOnce.Do(func() {
		if bin := ChromeBinary(); bin != "" {
			installedMajor = chromeMajorOf(bin)
		}
	})
	return installedMajor
}

// chromeMajorOf runs a browser binary with --version ("Chromium 152.0.7977.82
// Alpine Linux") and returns its major version, or 0 if that fails
func chromeMajorOf(bin string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return 0
	}
	m := chromeVersionRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	major, _ := strconv.Atoi(string(m[1]))
	return major
}

// ChromeUserAgent is the user agent Chrome on Linux sends, in the reduced form
// current versions use: only the major version is real
func ChromeUserAgent(major int) string {
	return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", major)
}
