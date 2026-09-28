package scraper

import (
	"testing"

	"extract-html-scraper/internal/models"
)

func TestExtractVideosFromHTML(t *testing.T) {
	cases := []struct {
		name string
		html string
		want []models.Video
	}{
		{
			name: "og:video takes og:title as its title",
			html: `<head>
				<meta property="og:title" content="Launch day" />
				<meta property="og:video" content="https://www.youtube.com/embed/dQw4w9WgXcQ" />
			</head>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "og", Title: "Launch day"},
			},
		},
		{
			name: "og:video declared with name=",
			html: `<head><meta name="og:video" content="https://www.youtube.com/embed/dQw4w9WgXcQ" /></head>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "og"},
			},
		},
		{
			name: "twitter:player",
			html: `<head><meta name="twitter:player" content="https://player.vimeo.com/video/76979871" /></head>`,
			want: []models.Video{
				{URL: "https://player.vimeo.com/video/76979871", Provider: "vimeo", Type: "twitter"},
			},
		},
		{
			name: "JSON-LD VideoObject in @graph prefers contentUrl and name",
			html: `<script type="application/ld+json">{"@context": "https://schema.org", "@graph": [
				{"@type": "NewsArticle", "headline": "Story"},
				{"@type": "VideoObject", "name": "Demo reel", "contentUrl": "https://cdn.example.com/media/demo.mp4", "embedUrl": "https://www.youtube.com/embed/abcdefghijk"}
			]}</script>`,
			want: []models.Video{
				{URL: "https://cdn.example.com/media/demo.mp4", Provider: "html5", Type: "jsonld", Title: "Demo reel"},
			},
		},
		{
			name: "JSON-LD @type array falls back to embedUrl and headline",
			html: `<script type="application/ld+json">{"@type": ["VideoObject", "CreativeWork"], "headline": "Clip", "embedUrl": "https://www.dailymotion.com/embed/video/x8abcd1"}</script>`,
			want: []models.Video{
				{URL: "https://www.dailymotion.com/embed/video/x8abcd1", Provider: "dailymotion", Type: "jsonld", Title: "Clip"},
			},
		},
		{
			name: "JSON-LD block holding a top-level array",
			html: `<script type="application/ld+json">[
				{"@type": "NewsArticle", "headline": "Story"},
				{"@type": "VideoObject", "name": "Array clip", "contentUrl": "https://cdn.example.com/media/array.mp4"}
			]</script>`,
			want: []models.Video{
				{URL: "https://cdn.example.com/media/array.mp4", Provider: "html5", Type: "jsonld", Title: "Array clip"},
			},
		},
		{
			name: "JSON-LD VideoObject nested as NewsArticle.video",
			html: `<script type="application/ld+json">{"@type": "NewsArticle", "headline": "Story",
				"video": {"@type": "VideoObject", "name": "Nested clip", "embedUrl": "https://www.youtube.com/embed/abcdefghijk"}}</script>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/abcdefghijk", Provider: "youtube", Type: "jsonld", Title: "Nested clip"},
			},
		},
		{
			name: "JSON-LD VideoGame and VideoGallery are not videos",
			html: `<script type="application/ld+json">{"@type": "VideoGame", "name": "Game", "url": "https://example.com/game"}</script>
				<script type="application/ld+json">{"@type": "VideoGallery", "name": "Gallery", "url": "https://example.com/videos"}</script>`,
			want: nil,
		},
		{
			name: "article iframe resolves protocol-relative src and keeps its title",
			html: `<body><article><iframe src="//www.youtube.com/embed/dQw4w9WgXcQ" title="Keynote"></iframe></article></body>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "embedded", Title: "Keynote"},
			},
		},
		{
			name: "lazy iframes use data-src or data-lazy-src when src is a placeholder",
			html: `<body><article>
				<iframe data-src="https://www.youtube.com/embed/dQw4w9WgXcQ"></iframe>
				<iframe src="about:blank" data-lazy-src="https://player.vimeo.com/video/76979871"></iframe>
			</article></body>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "embedded"},
				{URL: "https://player.vimeo.com/video/76979871", Provider: "vimeo", Type: "embedded"},
			},
		},
		{
			name: "iframes outside the article or not from a video host are ignored",
			html: `<body>
				<div class="sidebar"><iframe src="https://www.youtube.com/embed/sidebar0001"></iframe></div>
				<article><iframe src="https://www.google.com/maps/embed?pb=1"></iframe></article>
			</body>`,
			want: nil,
		},
		{
			name: "HTML5 video src and source children resolve relative URLs",
			html: `<body><main>
				<video src="/media/intro.mp4"></video>
				<video><source src="clips/outro.webm" type="video/webm" /></video>
			</main></body>`,
			want: []models.Video{
				{URL: "https://example.com/media/intro.mp4", Provider: "html5", Type: "html5"},
				{URL: "https://example.com/news/clips/outro.webm", Provider: "html5", Type: "html5"},
			},
		},
		{
			name: "same URL from og:video and an iframe is listed once, as og",
			html: `<head><meta property="og:video" content="https://www.youtube.com/embed/dQw4w9WgXcQ" /></head>
				<body><article><iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ" title="Keynote"></iframe></article></body>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "og"},
			},
		},
		{
			name: "sources come back in og, twitter, JSON-LD, embed, HTML5 order",
			html: `<head>
				<meta property="og:video" content="https://www.youtube.com/embed/dQw4w9WgXcQ" />
				<meta name="twitter:player" content="https://player.vimeo.com/video/76979871" />
				<script type="application/ld+json">{"@type": "VideoObject", "contentUrl": "https://cdn.example.com/media/demo.mp4"}</script>
			</head>
			<body><article>
				<video src="https://cdn.example.com/media/outro.webm"></video>
				<iframe src="https://www.dailymotion.com/embed/video/x8abcd1"></iframe>
			</article></body>`,
			want: []models.Video{
				{URL: "https://www.youtube.com/embed/dQw4w9WgXcQ", Provider: "youtube", Type: "og"},
				{URL: "https://player.vimeo.com/video/76979871", Provider: "vimeo", Type: "twitter"},
				{URL: "https://cdn.example.com/media/demo.mp4", Provider: "html5", Type: "jsonld"},
				{URL: "https://www.dailymotion.com/embed/video/x8abcd1", Provider: "dailymotion", Type: "embedded"},
				{URL: "https://cdn.example.com/media/outro.webm", Provider: "html5", Type: "html5"},
			},
		},
		{
			name: "page without videos",
			html: `<body><article><p>Text only.</p></article></body>`,
			want: nil,
		},
	}

	ve := NewVideoExtractor()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ve.ExtractVideosFromHTML(tc.html, "https://example.com/news/post")

			if len(got) != len(tc.want) {
				for _, v := range got {
					t.Logf("video: %+v", v)
				}
				t.Fatalf("expected %d videos, got %d", len(tc.want), len(got))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("video %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestVideoProviderDetection(t *testing.T) {
	cases := []struct {
		url      string
		embed    bool
		provider string
	}{
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", true, "youtube"},
		{"https://youtu.be/dQw4w9WgXcQ", true, "youtube"},
		{"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", true, "youtube"},
		{"https://player.vimeo.com/video/76979871", true, "vimeo"},
		{"https://www.dailymotion.com/embed/video/x8abcd1", true, "dailymotion"},
		{"https://player.twitch.tv/?video=123456", true, "twitch"},
		{"https://www.facebook.com/plugins/video.php?href=https%3A%2F%2Fwww.facebook.com%2Fwatch", true, "facebook"},
		{"https://www.facebook.com/plugins/post.php?href=https%3A%2F%2Fwww.facebook.com%2Fpost", false, "facebook"},
		{"https://www.tiktok.com/embed/v2/7123456789", true, "tiktok"},
		{"https://cdn.example.com/media/clip.mp4", true, "html5"},
		{"https://cdn.example.com/media/clip.webm?token=abc", true, "html5"},
		{"https://www.google.com/maps/embed?pb=1", false, "unknown"},
	}

	ve := NewVideoExtractor()
	for _, c := range cases {
		if got := ve.isVideoEmbed(c.url); got != c.embed {
			t.Errorf("isVideoEmbed(%q) = %v, want %v", c.url, got, c.embed)
		}
		if got := ve.detectProvider(c.url); got != c.provider {
			t.Errorf("detectProvider(%q) = %q, want %q", c.url, got, c.provider)
		}
	}
}
