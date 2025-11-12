package scraper

import (
	"fmt"
	"strings"

	"extract-html-scraper/internal/models"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"github.com/microcosm-cc/bluemonday"
)

type ArticleExtractor struct {
	sanitizer     *bluemonday.Policy
	htmlSanitizer *bluemonday.Policy
}

func NewArticleExtractor() *ArticleExtractor {
	// Configure bluemonday for HTML sanitization
	policy := bluemonday.StrictPolicy()

	// Configure HTML sanitizer for preserving structure
	htmlPolicy := bluemonday.UGCPolicy()
	htmlPolicy.AllowElements("p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "em", "blockquote", "ul", "ol", "li")

	return &ArticleExtractor{
		sanitizer:     policy,
		htmlSanitizer: htmlPolicy,
	}
}

// ExtractArticleWithOptions extracts content with configurable options
func (ae *ArticleExtractor) ExtractArticleWithOptions(html, baseURL string, options ExtractionOptions) models.ScrapeResponse {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return models.ScrapeResponse{
			Images: []models.Image{},
			Videos: []models.Video{},
		}
	}

	title := ae.extractTitle(doc)
	description := ae.extractDescription(doc)

	var content string
	if options.PreserveHTML {
		content = ae.extractContentAsHTML(doc)
	} else {
		content = ae.extractContent(doc)
	}

	// Extract images using the optimized image extractor
	imageExtractor := NewImageExtractor()
	images := imageExtractor.ExtractImagesFromHTML(html, baseURL)

	// Extract videos using the video extractor
	videoExtractor := NewVideoExtractor()
	videos := videoExtractor.ExtractVideosFromHTML(html, baseURL)

	// Extract metadata if requested
	var metadata models.ScrapeResponse
	if options.IncludeMetadata {
		metadata = ae.extractMetadataFromReadability(html)
	}

	// Calculate content quality metrics
	quality := ScoreContentQuality(content, html)

	response := models.ScrapeResponse{
		Title:       title,
		Description: description,
		Content:     content,
		Images:      images,
		Videos:      videos,
		Quality: models.Quality{
			Score:              quality.Score,
			TextToHTMLRatio:    quality.TextToHTMLRatio,
			ParagraphCount:     quality.ParagraphCount,
			AvgParagraphLength: quality.AvgParagraphLength,
			HasHeaders:         quality.HasHeaders,
			LinkDensity:        quality.LinkDensity,
			WordCount:          quality.WordCount,
		},
	}

	// Add metadata fields if requested
	if options.IncludeMetadata {
		response.Author = metadata.Author
		response.PublishDate = metadata.PublishDate
		response.Excerpt = metadata.Excerpt
		response.ReadingTime = metadata.ReadingTime
		response.Language = metadata.Language
		response.TextLength = metadata.TextLength
	}

	return response
}

// ExtractArticle extracts title, description, content, and images from HTML (backward compatibility)
func (ae *ArticleExtractor) ExtractArticle(html, baseURL string) models.ScrapeResponse {
	return ae.ExtractArticleWithOptions(html, baseURL, DefaultExtractionOptions())
}

// extractContentAsHTML extracts content preserving HTML structure
func (ae *ArticleExtractor) extractContentAsHTML(doc *goquery.Document) string {
	// First, try to use readability algorithm for better content extraction
	html, err := doc.Html()
	if err == nil {
		// Parse with readability, passing URL for better context
		article, err := readability.FromReader(strings.NewReader(html), nil)
		if err == nil && article.Content != "" {
			// Sanitize HTML content while preserving structure
			return ae.htmlSanitizer.Sanitize(article.Content)
		}
	}

	// Fallback to original selector-based approach if readability fails
	return ae.extractContentFallbackAsHTML(doc)
}

// extractContentFallbackAsHTML provides HTML-based content extraction fallback
func (ae *ArticleExtractor) extractContentFallbackAsHTML(doc *goquery.Document) string {
	// Find the main content container
	contentElement := FindContentContainer(doc)

	// Get HTML content and sanitize it
	htmlContent, err := contentElement.Html()
	if err != nil {
		return ""
	}

	return ae.htmlSanitizer.Sanitize(htmlContent)
}

// sanitizeText sanitizes text content
func (ae *ArticleExtractor) sanitizeText(text string) string {
	if text == "" {
		return ""
	}

	// Use bluemonday to sanitize HTML if present
	sanitized := ae.sanitizer.Sanitize(text)

	// Additional cleanup using our helper
	return CleanWhitespace(sanitized)
}

// extractTitle extracts the page title with fallback strategies
func (ae *ArticleExtractor) extractTitle(doc *goquery.Document) string {
	// Try Open Graph title first
	if title := FindMetaTag(doc, OGTitle, ""); title != "" {
		return ae.sanitizeText(title)
	}

	// Try Twitter card title
	if title := FindMetaTag(doc, "", TwitterTitle); title != "" {
		return ae.sanitizeText(title)
	}

	// Try h1 tag
	var title string
	doc.Find("h1").First().Each(func(i int, s *goquery.Selection) {
		title = strings.TrimSpace(s.Text())
	})
	if title != "" {
		return ae.sanitizeText(title)
	}

	// Try title tag as last resort
	doc.Find("title").Each(func(i int, s *goquery.Selection) {
		title = strings.TrimSpace(s.Text())
	})

	return ae.sanitizeText(title)
}

// extractDescription extracts the page description with fallback strategies
func (ae *ArticleExtractor) extractDescription(doc *goquery.Document) string {
	// Try Open Graph description first
	if desc := FindMetaTag(doc, OGDescription, ""); desc != "" {
		return ae.sanitizeText(desc)
	}

	// Try Twitter card description
	if desc := FindMetaTag(doc, "", TwitterDesc); desc != "" {
		return ae.sanitizeText(desc)
	}

	// Try meta description
	if desc := FindMetaTag(doc, "", MetaDesc); desc != "" {
		return ae.sanitizeText(desc)
	}

	// Try to extract from first paragraph
	if desc := ExtractDescriptionFromParagraph(doc); desc != "" {
		return ae.sanitizeText(desc)
	}

	return ""
}

// extractContent extracts the main article content using readability algorithm
func (ae *ArticleExtractor) extractContent(doc *goquery.Document) string {
	// First, try to use readability algorithm for better content extraction
	html, err := doc.Html()
	if err == nil {
		// Parse with readability, passing URL for better context
		article, err := readability.FromReader(strings.NewReader(html), nil)
		if err == nil && article.Content != "" {
			// Convert readability's HTML content to structured text
			return ae.convertHTMLToStructuredText(article.Content)
		}
	}

	// Fallback to original selector-based approach if readability fails
	return ae.extractContentFallback(doc)
}

// convertHTMLToStructuredText converts HTML content to structured text
func (ae *ArticleExtractor) convertHTMLToStructuredText(htmlContent string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return ae.sanitizeText(htmlContent)
	}

	// Extract structured text
	content := ExtractTextFromElements(doc.Selection, TextElements)

	// If no structured content found, extract all text
	if content == "" {
		content = ExtractFallbackText(doc.Selection)
	}

	// Clean up whitespace and remove noise
	content = CleanTextContent(content)
	return ae.sanitizeText(content)
}

// extractContentFallback provides the original selector-based content extraction
func (ae *ArticleExtractor) extractContentFallback(doc *goquery.Document) string {
	// Find the main content container
	contentElement := FindContentContainer(doc)

	// Extract structured text from the container
	content := ExtractTextFromElements(contentElement, TextElements)

	// If no structured content found, extract all text
	if content == "" {
		content = ExtractFallbackText(contentElement)
	}

	// Clean up whitespace and remove noise
	content = CleanTextContent(content)
	return ae.sanitizeText(content)
}

// extractMetadataFromReadability extracts additional metadata using readability
func (ae *ArticleExtractor) extractMetadataFromReadability(html string) models.ScrapeResponse {
	article, err := readability.FromReader(strings.NewReader(html), nil)
	if err != nil {
		return models.ScrapeResponse{}
	}

	// Calculate reading time (average 200 words per minute, but we'll use character count)
	readingTime := 0
	if article.Length > 0 {
		// Estimate reading time based on character count (roughly 5 chars per word, 200 words per minute)
		readingTime = int(article.Length / 1000) // characters / 1000 chars per minute
		if readingTime < 1 {
			readingTime = 1
		}
	}

	// Convert publish date to string
	publishDate := ""
	if article.PublishedTime != nil {
		publishDate = article.PublishedTime.Format("2006-01-02T15:04:05Z")
	}

	return models.ScrapeResponse{
		Author:      article.Byline,
		PublishDate: publishDate,
		Excerpt:     article.Excerpt,
		ReadingTime: readingTime,
		Language:    article.Language,
		TextLength:  article.Length,
	}
}

// ExtractArticleSimple is a simpler version for basic content extraction
func (ae *ArticleExtractor) ExtractArticleSimple(html, baseURL string) models.ScrapeResponse {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return models.ScrapeResponse{
			Images: []models.Image{},
			Videos: []models.Video{},
		}
	}

	// Simple title extraction
	title := ""
	doc.Find("title").Each(func(i int, s *goquery.Selection) {
		title = strings.TrimSpace(s.Text())
	})

	// Simple description extraction
	description := ""
	doc.Find("meta[name='description']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists {
			description = strings.TrimSpace(content)
		}
	})

	// Simple content extraction - just get all text
	content := ""
	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		// Remove script and style elements
		s.Find("script, style, nav, header, footer").Remove()
		content = strings.TrimSpace(s.Text())
	})

	// Extract images
	imageExtractor := NewImageExtractor()
	images := imageExtractor.ExtractImagesFromHTML(html, baseURL)

	// Extract videos
	videoExtractor := NewVideoExtractor()
	videos := videoExtractor.ExtractVideosFromHTML(html, baseURL)

	return models.ScrapeResponse{
		Title:       ae.sanitizeText(title),
		Description: ae.sanitizeText(description),
		Content:     ae.sanitizeText(content),
		Images:      images,
		Videos:      videos,
	}
}

// ExtractArticleWithMultipleStrategies tries multiple extraction strategies and returns the best result
// Strategies tried in order:
// 1. JSON-LD structured data extraction (fastest, most reliable for news sites)
// 2. Full extraction with readability (ExtractArticle)
// 3. Simple extraction (ExtractArticleSimple)
// 4. Metadata-only extraction (at least get title/description)
// Returns the result with highest quality score
func (ae *ArticleExtractor) ExtractArticleWithMultipleStrategies(html, baseURL string) models.ScrapeResponse {
	var results []models.ScrapeResponse
	var strategies []string

	// Parse HTML once for all strategies
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		fmt.Printf("Failed to parse HTML: %v\n", err)
		return models.ScrapeResponse{Images: []models.Image{}, Videos: []models.Video{}}
	}

	// Strategy 0: Try JSON-LD structured data first (best for news sites like SCMP)
	fmt.Printf("Extraction strategy 0: JSON-LD structured data\n")
	if headline, body, description, found := ExtractJSONLD(doc); found {
		// Extract images using the optimized image extractor
		imageExtractor := NewImageExtractor()
		images := imageExtractor.ExtractImagesFromHTML(html, baseURL)

		// Extract videos using the video extractor
		videoExtractor := NewVideoExtractor()
		videos := videoExtractor.ExtractVideosFromHTML(html, baseURL)

		// If articleBody is available, use it
		content := body
		if content == "" {
			// Fallback to description if no body
			content = description
		}

		// Calculate quality
		quality := ScoreContentQuality(content, html)

		result0 := models.ScrapeResponse{
			Title:       ae.sanitizeText(headline),
			Description: ae.sanitizeText(description),
			Content:     ae.sanitizeText(content),
			Images:      images,
			Videos:      videos,
			Quality: models.Quality{
				Score:              quality.Score + 10, // Bonus for structured data
				TextToHTMLRatio:    quality.TextToHTMLRatio,
				ParagraphCount:     quality.ParagraphCount,
				AvgParagraphLength: quality.AvgParagraphLength,
				HasHeaders:         quality.HasHeaders,
				LinkDensity:        quality.LinkDensity,
				WordCount:          quality.WordCount,
			},
		}
		results = append(results, result0)
		strategies = append(strategies, "jsonld")
		fmt.Printf("Strategy 0 result: title=%d chars, content=%d chars, quality=%d\n",
			len(result0.Title), len(result0.Content), result0.Quality.Score)

		// If JSON-LD has good content, might be sufficient, but continue for comparison
	}

	// Strategy 1: Full extraction with readability
	fmt.Printf("Extraction strategy 1: Full extraction with readability\n")
	result1 := ae.ExtractArticle(html, baseURL)
	results = append(results, result1)
	strategies = append(strategies, "readability")
	fmt.Printf("Strategy 1 result: title=%d chars, content=%d chars, quality=%d\n",
		len(result1.Title), len(result1.Content), result1.Quality.Score)

	// Strategy 2: Simple extraction (fallback if readability fails)
	if len(result1.Content) == 0 || len(result1.Title) == 0 || result1.Quality.Score < 30 {
		fmt.Printf("Extraction strategy 2: Simple extraction\n")
		result2 := ae.ExtractArticleSimple(html, baseURL)
		results = append(results, result2)
		strategies = append(strategies, "simple")
		fmt.Printf("Strategy 2 result: title=%d chars, content=%d chars\n",
			len(result2.Title), len(result2.Content))
	}

	// Strategy 3: Metadata-only (last resort - at least get title/description)
	allEmpty := true
	for _, r := range results {
		if len(r.Title) > 0 || len(r.Content) > 0 {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		fmt.Printf("Extraction strategy 3: Metadata-only extraction\n")
		result3 := ae.ExtractMetadataOnly(html, baseURL)
		results = append(results, result3)
		strategies = append(strategies, "metadata-only")
		fmt.Printf("Strategy 3 result: title=%d chars, description=%d chars\n",
			len(result3.Title), len(result3.Description))
	}

	// Select best result based on quality score and content length
	best := ae.selectBestResult(results, strategies)
	fmt.Printf("Selected best result: strategy=%s, quality=%d, title=%d chars, content=%d chars\n",
		best.Strategy, best.Result.Quality.Score, len(best.Result.Title), len(best.Result.Content))
	return best.Result
}

// ExtractionResult wraps a result with its strategy name for selection
type extractionResultWithStrategy struct {
	Result   models.ScrapeResponse
	Strategy string
}

// selectBestResult chooses the best extraction result based on multiple criteria
func (ae *ArticleExtractor) selectBestResult(results []models.ScrapeResponse, strategies []string) extractionResultWithStrategy {
	if len(results) == 0 {
		return extractionResultWithStrategy{
			Result:   models.ScrapeResponse{Images: []models.Image{}, Videos: []models.Video{}},
			Strategy: "none",
		}
	}

	var best extractionResultWithStrategy
	bestScore := -1

	for i, result := range results {
		strategy := "unknown"
		if i < len(strategies) {
			strategy = strategies[i]
		}

		// Calculate composite score: quality score + content bonus
		score := result.Quality.Score

		// Bonus for having both title and content
		if len(result.Title) > 0 && len(result.Content) > 0 {
			score += 20
		}

		// Bonus for substantial content (more is better, up to a point)
		contentBonus := len(result.Content) / 100 // 1 point per 100 chars, max 50
		if contentBonus > 50 {
			contentBonus = 50
		}
		score += contentBonus

		// Prefer readability strategy if scores are close (within 10 points)
		if strategy == "readability" && score >= bestScore-10 && len(result.Content) > 0 {
			score += 5 // Small bonus for preferred strategy
		}

		if score > bestScore || (score == bestScore && len(result.Content) > len(best.Result.Content)) {
			bestScore = score
			best = extractionResultWithStrategy{
				Result:   result,
				Strategy: strategy,
			}
		}
	}

	return best
}

// ExtractMetadataOnly extracts only title, description, and metadata (no content body)
// Useful as last resort when content extraction fails
func (ae *ArticleExtractor) ExtractMetadataOnly(html, baseURL string) models.ScrapeResponse {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return models.ScrapeResponse{Images: []models.Image{}, Videos: []models.Video{}}
	}

	// Extract title
	title := ""
	doc.Find("title").Each(func(i int, s *goquery.Selection) {
		title = strings.TrimSpace(s.Text())
	})

	// Extract description
	description := ""
	doc.Find("meta[name='description']").Each(func(i int, s *goquery.Selection) {
		if content, exists := s.Attr("content"); exists {
			description = strings.TrimSpace(content)
		}
	})

	// Try to get some metadata from readability
	metadata := ae.extractMetadataFromReadability(html)

	// Extract images
	imageExtractor := NewImageExtractor()
	images := imageExtractor.ExtractImagesFromHTML(html, baseURL)

	// Extract videos
	videoExtractor := NewVideoExtractor()
	videos := videoExtractor.ExtractVideosFromHTML(html, baseURL)

	return models.ScrapeResponse{
		Title:       ae.sanitizeText(title),
		Description: ae.sanitizeText(description),
		Content:     "", // No content body in metadata-only mode
		Images:      images,
		Videos:      videos,
		Author:      metadata.Author,
		PublishDate: metadata.PublishDate,
		Excerpt:     metadata.Excerpt,
		ReadingTime: metadata.ReadingTime,
		Language:    metadata.Language,
		TextLength:  metadata.TextLength,
		Quality: models.Quality{
			Score:              10, // Low score since no content
			TextToHTMLRatio:    0,
			ParagraphCount:     0,
			AvgParagraphLength: 0,
			HasHeaders:          false,
			LinkDensity:        0,
			WordCount:          0,
		},
	}
}
