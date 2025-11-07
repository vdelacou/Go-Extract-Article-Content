package scraper

import "testing"

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
