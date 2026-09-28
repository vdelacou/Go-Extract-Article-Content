package scraper

import (
	"encoding/json"
	"strings"
	"testing"

	"extract-html-scraper/internal/models"
)

func TestHasArticleText(t *testing.T) {
	cases := []struct {
		name   string
		result models.ScrapeResponse
		want   bool
	}{
		// pandaily: title and images from the static HTML, body rendered client-side
		{"title only", models.ScrapeResponse{Title: "Meituan LongCat 2.5 preview"}, false},
		{"text scored 0", models.ScrapeResponse{Content: "Menu", Quality: models.Quality{Score: 0}}, false},
		{"article", models.ScrapeResponse{Content: "Meituan released a preview.", Quality: models.Quality{Score: 60}}, true},
	}

	for _, c := range cases {
		if got := hasArticleText(c.result); got != c.want {
			t.Errorf("%s: hasArticleText = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsChallengeResult(t *testing.T) {
	s := &Scraper{httpClient: NewHTTPClient()}

	// tvinsider as the browser returned it, with a quality score of 45
	interstitial := models.ScrapeResponse{
		Title:   "www.tvinsider.com",
		Content: "Performing security verification\n\nThis website uses a security service to protect against malicious bots. This page is displayed while the website verifies you are not a bot.",
	}
	if !s.isChallengeResult(interstitial) {
		t.Error("expected the verification interstitial to be recognised")
	}

	article := models.ScrapeResponse{
		Title:   "Why sites ask you to verify you are human",
		Content: strings.Repeat("Bot checks ask visitors to verify you are human before a page loads. ", 20),
	}
	if s.isChallengeResult(article) {
		t.Error("an article quoting challenge wording is not a challenge")
	}
}

func TestWithContentFlagAlwaysEmitsContent(t *testing.T) {
	missing, err := json.Marshal(withContentFlag(models.ScrapeResponse{Title: "Only a title"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"content":""`, `"contentMissing":true`} {
		if !strings.Contains(string(missing), want) {
			t.Errorf("expected %s in %s", want, missing)
		}
	}

	present, err := json.Marshal(withContentFlag(models.ScrapeResponse{Content: "Body text."}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(present), "contentMissing") {
		t.Errorf("contentMissing should be omitted when text exists: %s", present)
	}
}
