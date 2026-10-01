package scraper

import (
	"encoding/json"
	"strings"
	"testing"

	"extract-html-scraper/internal/models"
)

func TestExtractImagesFromHTMLKeepsArticleWidgets(t *testing.T) {
	html := `
		<div class="sidebar widget">
			<img src="https://cdn.example.com/sidebar.jpg" width="600" height="400" alt="Sidebar" />
		</div>
		<section class="sidebar related-posts">
			<img src="https://static0.srcdn.com/sidebar.jpg?s400-rw-e365" width="800" height="600" alt="SidebarLarge" />
		</section>
		<main>
		<article class="w-article widget article layout-rich">
			<header class="article-header">
    <div class="heading_image">
        <img src="https://static0.srcdn.com/hero.jpg?q=70&fit=crop&w=1600&h=900" width="1600" height="900" alt="Hero" />
				</div>
			</header>
			<section id="article-body" class="article-body">
        <div class="body-img">
            <img src="https://static0.srcdn.com/body.jpg?w=1200&h=600&crop=faces" width="1000" height="700" alt="Body" />
        </div>
        <div class="body-img">
            <img src="https://static0.srcdn.com/portrait.jpg?w=600&h=900" width="600" height="900" alt="Portrait" />
        </div>
		<div class="body-img">
			<img src="https://static0.srcdn.com/small-landscape.jpg?w=550&h=300" width="550" height="300" alt="Small" />
		</div>
			</section>
		</article>
		</main>
	`

	ie := NewImageExtractor()
	images := ie.ExtractImagesFromHTML(html, "https://screenrant.com/lanterns-dcu-show-release-window-2026/")

	if len(images) != 2 {
		for _, img := range images {
			t.Logf("image: %s (alt=%s)", img.URL, img.Alt)
		}
		t.Fatalf("expected 2 article images, got %d", len(images))
	}

	for _, img := range images {
		t.Logf("extracted: %s", img.URL)
	}

	headerURL := "https://static0.srcdn.com/hero.jpg"
	bodyURL := "https://static0.srcdn.com/body.jpg"
	portraitURL := "https://static0.srcdn.com/portrait.jpg"
	smallURL := "https://static0.srcdn.com/small-landscape.jpg"
	sidebarURL := "https://cdn.example.com/sidebar.jpg"
	bloggerSidebarURL := "https://static0.srcdn.com/sidebar.jpg"

	if images[0].URL != headerURL && images[1].URL != headerURL {
		t.Fatalf("expected hero image %s to be present", headerURL)
	}

	if images[0].URL != bodyURL && images[1].URL != bodyURL {
		t.Fatalf("expected body image %s to be present", bodyURL)
	}

	for _, img := range images {
		if img.URL == portraitURL {
			t.Fatalf("portrait image %s should have been filtered out", portraitURL)
		}
		if img.URL == smallURL {
			t.Fatalf("small landscape image %s should have been filtered out", smallURL)
		}
		if img.URL == sidebarURL {
			t.Fatalf("sidebar image %s should have been filtered out", sidebarURL)
		}
		if img.URL == bloggerSidebarURL {
			t.Fatalf("blogger sidebar image %s should have been filtered out", bloggerSidebarURL)
		}
	}
}

// GeekPark-style page: og:image declared with name=, WordPress-style /uploads/
// paths, and body images whose only dimension is an inline-style width.
func TestExtractImagesFromHTMLGeekParkStyle(t *testing.T) {
	html := `
		<html><head>
		<meta content="https://imgslim.geekpark.net/uploads/image/file/49/77/497777880f3bedc8798f485d3d4078d3.jpg" name="og:image" />
		<meta content="website" property="og:type" />
		</head><body>
		<article>
			<div id="article-body">
				<img class="rich_pages wxw-img js_img_placeholder wx_img_placeholder" style="width: 661px !important; height: auto !important;" src="https://imgslim.geekpark.net/uploads/image/file/cf/bb/cfbb1dac95c022af3cf4deea43cea02b.jpeg" alt="Body" />
			</div>
		</article>
		</body></html>
	`

	ie := NewImageExtractor()
	images := ie.ExtractImagesFromHTML(html, "http://www.geekpark.net/news/371035")

	ogURL := "https://imgslim.geekpark.net/uploads/image/file/49/77/497777880f3bedc8798f485d3d4078d3.jpg"
	bodyURL := "https://imgslim.geekpark.net/uploads/image/file/cf/bb/cfbb1dac95c022af3cf4deea43cea02b.jpeg"

	if len(images) != 2 {
		for _, img := range images {
			t.Logf("image: %s (alt=%s)", img.URL, img.Alt)
		}
		t.Fatalf("expected og:image and body image, got %d images", len(images))
	}
	if images[0].URL != ogURL {
		t.Fatalf("expected og:image %s first, got %s", ogURL, images[0].URL)
	}
	if images[1].URL != bodyURL {
		t.Fatalf("expected body image %s second, got %s", bodyURL, images[1].URL)
	}
}

func TestExtractImagesFromHTMLAcceptsUppercaseExtension(t *testing.T) {
	// From http://www.geekpark.net/news/371040, whose only cover is a .JPEG
	html := `
		<html><head>
		<meta content="https://imgslim.geekpark.net/uploads/image/file/b1/29/b1298a4d5ffba57e09b0e7a23bbb4f77.JPEG" name="og:image" />
		</head><body>
		<article><div id="article-body"><p>Text only.</p></div></article>
		</body></html>
	`

	images := NewImageExtractor().ExtractImagesFromHTML(html, "http://www.geekpark.net/news/371040")

	want := "https://imgslim.geekpark.net/uploads/image/file/b1/29/b1298a4d5ffba57e09b0e7a23bbb4f77.JPEG"
	if len(images) != 1 || images[0].URL != want {
		t.Fatalf("expected only the .JPEG og:image %s, got %+v", want, images)
	}
}

func TestBadHintMatchesAdTermsOnlyAsTokens(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://technode.com/wp-content/uploads/2026/09/mo-new.webp", false},
		{"https://imgslim.geekpark.net/uploads/image/file/49/77/497777880f3bedc8798f485d3d4078d3.jpg", false},
		{"https://example.com/download/headline-thread-loading-gradient-broadcast.jpg", false},
		{"https://cdn.example.com/2026/3ad4e1f0c2b94a7d8e6f.jpg", false},
		{"https://example.com/ads/banner.jpg", true},
		{"https://example.com/img/ad-slot-top.png", true},
		{"https://example.com/img/ADS_300x250.gif", true},
		{"https://adserver.example.com/creative.jpg", true},
		{"https://example.com/logo.png", true},
	}

	badHint := NewImageExtractor().regexes["badHint"]
	for _, c := range cases {
		if got := badHint.MatchString(c.url); got != c.want {
			t.Errorf("badHint(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestExtractImagesFromHTMLEncodesNoImagesAsEmptyArray(t *testing.T) {
	ie := NewImageExtractor()
	images := ie.ExtractImagesFromHTML(`<html><body><article><p>No images here.</p></article></body></html>`, "https://example.com/post")

	body, err := json.Marshal(models.ScrapeResponse{Images: images})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"images":[]`) {
		t.Fatalf("expected \"images\":[] in %s", body)
	}
}
