package scraper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func readPandailyFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "pandaily", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractRemixArticleFindsTheRequestedPost(t *testing.T) {
	cases := []struct {
		fixture, url, title, image string
	}{
		{
			fixture: "unitree-g1-200.html",
			url:     "https://pandaily.com/unitree-g1-unifolm-x2-autonomous-sparring-world-model",
			title:   "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model Without Teleoperation",
			image:   "https://cms-image.pandaily.com/1/unitree_g1_sparring_de412e8609.png",
		},
		{
			fixture: "alibaba-xekrung-200.html",
			url:     "https://pandaily.com/alibaba-xekrung-27b-cybergym-model-leaderboard-88-9",
			title:   "Alibaba's 27B XekRung Model Tops CyberGym Model Leaderboard at 88.9%",
			image:   "https://cms-image.pandaily.com/1/alibaba_xekrung_27b_cybergym_017cc6cb29.png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			page := readPandailyFixture(t, tc.fixture)
			article, ok := ExtractRemixArticle(page, tc.url)
			if !ok {
				t.Fatal("article not found in the hydration payload")
			}
			if article.Title != tc.title {
				t.Errorf("title = %q, want %q", article.Title, tc.title)
			}
			if article.ImageURL != tc.image {
				t.Errorf("image = %q, want %q", article.ImageURL, tc.image)
			}
			if len(article.BodyHTML) < 1500 {
				t.Errorf("body is only %d bytes", len(article.BodyHTML))
			}
			// The payload also carries featured posts, some with longer bodies
			if strings.Contains(article.BodyHTML, "Hy4 preview") {
				t.Error("body belongs to the featured Tencent Hunyuan post")
			}
		})
	}
}

func TestExtractRemixArticleIgnoresNotFoundPage(t *testing.T) {
	// Cloudflare's cached copy of pandaily's 404 for a live article: the payload
	// has only featured posts, none with the requested slug
	page := readPandailyFixture(t, "agibot-cached-404.html")
	if article, ok := ExtractRemixArticle(page, "https://pandaily.com/agibot-ge-act-2-native-world-action-model-scaling"); ok {
		t.Fatalf("expected no article, got %q", article.Title)
	}
}

func TestExtractRemixArticleRejectsAmbiguousSlug(t *testing.T) {
	body := "<p>" + strings.Repeat("Body text. ", 30) + "</p>"
	stream, _ := json.Marshal([]interface{}{
		map[string]int{"_1": 2},
		"posts",
		[]int{3, 8},
		map[string]int{"_4": 5, "_6": 7},
		"slug", "same-slug", "content", body,
		map[string]int{"_4": 5, "_6": 9},
		"<p>" + strings.Repeat("Other text. ", 30) + "</p>",
	})
	arg, _ := json.Marshal(string(stream))
	page := `<script>window.__remixContext.streamController.enqueue(` + string(arg) + `);</script>`

	if _, ok := ExtractRemixArticle(page, "https://example.com/same-slug"); ok {
		t.Fatal("two posts share the slug: expected no guess")
	}

	// Sanity check that the same payload decodes when the slug is unique
	stream, _ = json.Marshal([]interface{}{
		map[string]int{"_1": 2}, "post", map[string]int{"_3": 4, "_5": 6}, "slug", "only-slug", "content", body,
	})
	arg, _ = json.Marshal(string(stream))
	page = `<script>window.__remixContext.streamController.enqueue(` + string(arg) + `);</script>`
	if article, ok := ExtractRemixArticle(page, "https://example.com/only-slug"); !ok || article.BodyHTML != body {
		t.Fatalf("expected the unique post, got %+v", article)
	}
}

func TestExtractArticleRecoversPandailyBody(t *testing.T) {
	page := readPandailyFixture(t, "unitree-g1-200.html")
	got := NewArticleExtractor().ExtractArticleWithMultipleStrategies(page, "https://pandaily.com/unitree-g1-unifolm-x2-autonomous-sparring-world-model")

	if n := utf8.RuneCountInString(got.Content); n < 1500 {
		t.Fatalf("expected the article body, got %d chars: %q", n, got.Content)
	}
	if got.Title != "Unitree G1 Sparring Demo Uses UnifoLM-X2-1.0 World Model Without Teleoperation" {
		t.Errorf("title = %q", got.Title)
	}
	if len(got.Images) == 0 || got.Images[0].URL != "https://cms-image.pandaily.com/1/unitree_g1_sparring_de412e8609.png" {
		t.Errorf("expected the og:image first, got %+v", got.Images)
	}
}
