package scraper

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"extract-html-scraper/internal/models"

	"github.com/PuerkitoBio/goquery"
)

type VideoExtractor struct {
	videoExtRegex *regexp.Regexp
}

func NewVideoExtractor() *VideoExtractor {
	return &VideoExtractor{
		videoExtRegex: regexp.MustCompile(`\.(mp4|webm|ogg|mov|avi|mkv)(?:\?|$)`),
	}
}

// ExtractVideosFromHTML extracts videos from HTML content
func (ve *VideoExtractor) ExtractVideosFromHTML(html, baseURL string) []models.Video {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return []models.Video{}
	}

	var videos []models.Video
	seen := make(map[string]bool)

	// Extract from multiple sources
	videos = append(videos, ve.extractOGVideos(doc, baseURL, seen)...)
	videos = append(videos, ve.extractTwitterPlayer(doc, baseURL, seen)...)
	videos = append(videos, ve.extractJSONLDVideos(doc, baseURL, seen)...)
	videos = append(videos, ve.extractEmbeddedVideos(doc, baseURL, seen)...)
	videos = append(videos, ve.extractHTML5Videos(doc, baseURL, seen)...)

	return videos
}

// extractOGVideos extracts videos from Open Graph meta tags
func (ve *VideoExtractor) extractOGVideos(doc *goquery.Document, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video
	var videoURL, videoTitle string

	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		// OpenGraph specifies property=, but many pages emit name= instead
		property, exists := s.Attr("property")
		if !exists {
			property, exists = s.Attr("name")
		}
		if !exists {
			return
		}

		switch property {
		case "og:video", "og:video:url", "og:video:secure_url":
			if content, exists := s.Attr("content"); exists {
				videoURL = content
			}
		case "og:title":
			if content, exists := s.Attr("content"); exists && videoTitle == "" {
				videoTitle = content
			}
		}
	})

	if videoURL != "" {
		absURL, err := ve.toAbsoluteURL(videoURL, baseURL)
		if err == nil && !seen[absURL] {
			seen[absURL] = true
			provider := ve.detectProvider(absURL)
			videos = append(videos, models.Video{
				URL:      absURL,
				Provider: provider,
				Type:     "og",
				Title:    videoTitle,
			})
		}
	}

	return videos
}

// extractTwitterPlayer extracts videos from Twitter player meta tags
func (ve *VideoExtractor) extractTwitterPlayer(doc *goquery.Document, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video
	var playerURL string

	doc.Find("meta[name='twitter:player']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists {
			playerURL = content
		}
	})

	if playerURL != "" {
		absURL, err := ve.toAbsoluteURL(playerURL, baseURL)
		if err == nil && !seen[absURL] {
			seen[absURL] = true
			provider := ve.detectProvider(absURL)
			videos = append(videos, models.Video{
				URL:      absURL,
				Provider: provider,
				Type:     "twitter",
			})
		}
	}

	return videos
}

// extractJSONLDVideos extracts videos from JSON-LD structured data
func (ve *VideoExtractor) extractJSONLDVideos(doc *goquery.Document, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video

	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		jsonText := s.Text()
		// A block holds either one object or an array of them
		var data interface{}
		if err := json.Unmarshal([]byte(jsonText), &data); err != nil {
			return
		}

		videos = append(videos, ve.extractVideoFromJSONLD(data, baseURL, seen)...)
	})

	return videos
}

// extractVideoFromJSONLD recursively extracts every VideoObject from JSON-LD:
// top-level arrays, @graph entries and nested properties such as NewsArticle.video
func (ve *VideoExtractor) extractVideoFromJSONLD(value interface{}, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video

	switch node := value.(type) {
	case []interface{}:
		for _, item := range node {
			videos = append(videos, ve.extractVideoFromJSONLD(item, baseURL, seen)...)
		}
	case map[string]interface{}:
		if video, ok := ve.videoFromJSONLDObject(node, baseURL, seen); ok {
			videos = append(videos, video)
		}

		// Visit properties in sorted order so nested videos keep a stable order
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			videos = append(videos, ve.extractVideoFromJSONLD(node[key], baseURL, seen)...)
		}
	}

	return videos
}

// isVideoObjectType reports whether a JSON-LD @type names a VideoObject,
// including forms such as schema:VideoObject and MusicVideoObject. Matching
// on VideoObject rather than Video rejects VideoGame and VideoGallery.
func isVideoObjectType(typeVal interface{}) bool {
	switch t := typeVal.(type) {
	case string:
		return strings.Contains(t, "VideoObject")
	case []interface{}:
		for _, item := range t {
			if s, ok := item.(string); ok && strings.Contains(s, "VideoObject") {
				return true
			}
		}
	}
	return false
}

// videoFromJSONLDObject converts a single JSON-LD VideoObject into a video
func (ve *VideoExtractor) videoFromJSONLDObject(data map[string]interface{}, baseURL string, seen map[string]bool) (models.Video, bool) {
	if !isVideoObjectType(data["@type"]) {
		return models.Video{}, false
	}

	// Extract video URL
	var videoURL, videoTitle string

	// Try different URL fields
	if contentURL, ok := data["contentUrl"].(string); ok {
		videoURL = contentURL
	} else if embedURL, ok := data["embedUrl"].(string); ok {
		videoURL = embedURL
	} else if url, ok := data["url"].(string); ok {
		videoURL = url
	}

	// Extract title
	if name, ok := data["name"].(string); ok {
		videoTitle = name
	} else if headline, ok := data["headline"].(string); ok {
		videoTitle = headline
	}

	if videoURL == "" {
		return models.Video{}, false
	}

	absURL, err := ve.toAbsoluteURL(videoURL, baseURL)
	if err != nil || seen[absURL] {
		return models.Video{}, false
	}
	seen[absURL] = true
	provider := ve.detectProvider(absURL)
	return models.Video{
		URL:      absURL,
		Provider: provider,
		Type:     "jsonld",
		Title:    videoTitle,
	}, true
}

// extractEmbeddedVideos extracts videos from iframe embeds
func (ve *VideoExtractor) extractEmbeddedVideos(doc *goquery.Document, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video

	// Look for iframes in article content
	articleSelectors := []string{
		"article",
		"main",
		"[itemprop='articleBody']",
		"section.article-body",
		"#article-body",
	}

	selector := strings.Join(articleSelectors, ",")
	doc.Find(selector).Find("iframe").Each(func(i int, s *goquery.Selection) {
		// Lazy loaders keep the real URL in data-src or data-lazy-src and put
		// a placeholder such as about:blank in src, so take the first video URL
		for _, attr := range []string{"src", "data-src", "data-lazy-src"} {
			src, exists := s.Attr(attr)
			if !exists {
				continue
			}
			absURL, err := ve.toAbsoluteURL(src, baseURL)
			if err != nil || !ve.isVideoEmbed(absURL) {
				continue
			}
			if !seen[absURL] {
				seen[absURL] = true
				provider := ve.detectProvider(absURL)
				title, _ := s.Attr("title")
				videos = append(videos, models.Video{
					URL:      absURL,
					Provider: provider,
					Type:     "embedded",
					Title:    title,
				})
			}
			break
		}
	})

	return videos
}

// extractHTML5Videos extracts HTML5 video tags
func (ve *VideoExtractor) extractHTML5Videos(doc *goquery.Document, baseURL string, seen map[string]bool) []models.Video {
	var videos []models.Video

	// Look for video tags in article content
	articleSelectors := []string{
		"article",
		"main",
		"[itemprop='articleBody']",
		"section.article-body",
		"#article-body",
	}

	selector := strings.Join(articleSelectors, ",")
	doc.Find(selector).Find("video").Each(func(i int, s *goquery.Selection) {
		// Try to get video source from src attribute
		if src, exists := s.Attr("src"); exists {
			absURL, err := ve.toAbsoluteURL(src, baseURL)
			if err == nil && !seen[absURL] {
				seen[absURL] = true
				videos = append(videos, models.Video{
					URL:      absURL,
					Provider: "html5",
					Type:     "html5",
				})
			}
		}

		// Also check source tags within video element
		s.Find("source").Each(func(j int, source *goquery.Selection) {
			if src, exists := source.Attr("src"); exists {
				absURL, err := ve.toAbsoluteURL(src, baseURL)
				if err == nil && !seen[absURL] {
					seen[absURL] = true
					videos = append(videos, models.Video{
						URL:      absURL,
						Provider: "html5",
						Type:     "html5",
					})
				}
			}
		})
	})

	return videos
}

// isVideoEmbed checks if a URL is a known video embed
func (ve *VideoExtractor) isVideoEmbed(url string) bool {
	lowerURL := strings.ToLower(url)
	return strings.Contains(lowerURL, "youtube.com") ||
		strings.Contains(lowerURL, "youtube-nocookie.com") ||
		strings.Contains(lowerURL, "youtu.be") ||
		strings.Contains(lowerURL, "vimeo.com") ||
		strings.Contains(lowerURL, "dailymotion.com") ||
		strings.Contains(lowerURL, "twitch.tv") ||
		strings.Contains(lowerURL, "facebook.com/plugins/video") ||
		strings.Contains(lowerURL, "tiktok.com") ||
		ve.videoExtRegex.MatchString(url)
}

// detectProvider identifies the video provider from the URL
func (ve *VideoExtractor) detectProvider(url string) string {
	lowerURL := strings.ToLower(url)

	if strings.Contains(lowerURL, "youtube.com") || strings.Contains(lowerURL, "youtube-nocookie.com") || strings.Contains(lowerURL, "youtu.be") {
		return "youtube"
	}
	if strings.Contains(lowerURL, "vimeo.com") {
		return "vimeo"
	}
	if strings.Contains(lowerURL, "dailymotion.com") {
		return "dailymotion"
	}
	if strings.Contains(lowerURL, "twitch.tv") {
		return "twitch"
	}
	if strings.Contains(lowerURL, "facebook.com") {
		return "facebook"
	}
	if strings.Contains(lowerURL, "tiktok.com") {
		return "tiktok"
	}
	if ve.videoExtRegex.MatchString(url) {
		return "html5"
	}

	return "unknown"
}

// toAbsoluteURL converts a relative URL to absolute
func (ve *VideoExtractor) toAbsoluteURL(relativeURL, baseURL string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	rel, err := url.Parse(relativeURL)
	if err != nil {
		return "", err
	}

	abs := base.ResolveReference(rel)
	return abs.String(), nil
}
