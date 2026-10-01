package scraper

import (
	"strings"
	"testing"
)

func TestExtractArticleReturnsPlainTextWithoutEntities(t *testing.T) {
	paragraph := `<p>It&#39;s the &quot;final&quot; season, and Tom &amp; Jerry fans won&#8217;t be surprised by the trailer that dropped this week.</p>`
	cases := []struct {
		name            string
		html            string
		wantTitle       string
		wantDescription string
		wantContent     string
	}{
		{
			name: "og:title and body text",
			html: `<html><head>
				<meta property="og:title" content="&#039;Tulsa King&#039; Season 4 &amp; More" />
				<meta property="og:description" content="Sylvester Stallone&#039;s drama returns." />
				</head><body><article>` + strings.Repeat(paragraph, 6) + `</article></body></html>`,
			wantTitle:       "'Tulsa King' Season 4 & More",
			wantDescription: "Sylvester Stallone's drama returns.",
			wantContent:     `It's the "final" season, and Tom & Jerry fans won’t be surprised`,
		},
		{
			name: "JSON-LD strings that carry entities",
			html: `<html><head><script type="application/ld+json">{"@type": "NewsArticle",
				"headline": "&#8216;Paradise&#8217; Wraps Filming &#038; More",
				"description": "Hong Kong&apos;s premier newspaper",
				"articleBody": "` + strings.Repeat(`Hong Kong&apos;s premier English language newspaper has the city&#8217;s story. `, 20) + `"}</script>
				</head><body></body></html>`,
			wantTitle:       "‘Paradise’ Wraps Filming & More",
			wantDescription: "Hong Kong's premier newspaper",
			wantContent:     "Hong Kong's premier English language newspaper has the city’s story.",
		},
	}

	ae := NewArticleExtractor()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ae.ExtractArticleWithMultipleStrategies(tc.html, "https://example.com/story")

			if got.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tc.wantTitle)
			}
			if got.Description != tc.wantDescription {
				t.Errorf("description = %q, want %q", got.Description, tc.wantDescription)
			}
			if !strings.Contains(got.Content, tc.wantContent) {
				t.Errorf("content does not contain %q:\n%s", tc.wantContent, got.Content)
			}
			for _, field := range []string{got.Title, got.Description, got.Content} {
				if strings.Contains(field, "&#") || strings.Contains(field, "&amp;") || strings.Contains(field, "&apos;") {
					t.Errorf("entity left in %q", field)
				}
			}
		})
	}
}
