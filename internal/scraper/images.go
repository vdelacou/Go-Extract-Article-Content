package scraper

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"extract-html-scraper/internal/config"
	"extract-html-scraper/internal/models"

	"github.com/PuerkitoBio/goquery"
)

type ImageExtractor struct {
	config  config.ImageConfig
	regexes map[string]*regexp.Regexp
}

func NewImageExtractor() *ImageExtractor {
	cfg := config.DefaultImageConfig()
	regexes := config.CompileRegexes()

	return &ImageExtractor{
		config:  cfg,
		regexes: regexes,
	}
}

// ExtractImagesFromHTML extracts and scores images from HTML content
// Only returns: 1) OG tag image, 2) JSON-LD image, 3) Images within article/main content
func (ie *ImageExtractor) ExtractImagesFromHTML(html, baseURL string) []models.Image {
	// Parse HTML once with goquery
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return []models.Image{}
	}

	// Pre-process HTML to remove unwanted sections
	ie.preprocessHTML(doc)

	// Extract candidates concurrently
	candidatesChan := make(chan []models.ImageCandidate, 3)
	var wg sync.WaitGroup

	// Extract og:image concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		ogImage := ie.extractOgImage(doc, baseURL)
		if ogImage != nil {
			candidatesChan <- []models.ImageCandidate{*ogImage}
		} else {
			candidatesChan <- []models.ImageCandidate{}
		}
	}()

	// Extract JSON-LD image concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		jsonldImage := ie.extractJSONLDImage(doc, baseURL)
		if jsonldImage != nil {
			candidatesChan <- []models.ImageCandidate{*jsonldImage}
		} else {
			candidatesChan <- []models.ImageCandidate{}
		}
	}()

	// Extract img tags from article content with strict filtering
	wg.Add(1)
	go func() {
		defer wg.Done()
		imgCandidates := ie.extractArticleImgTags(doc, baseURL)
		candidatesChan <- imgCandidates
	}()

	// Wait for all extractions to complete
	go func() {
		wg.Wait()
		close(candidatesChan)
	}()

	// Collect all candidates
	var allCandidates []models.ImageCandidate
	for candidates := range candidatesChan {
		allCandidates = append(allCandidates, candidates...)
	}

	// Filter and score candidates
	// STRICT: Only keep OG images or images that are in article scope
	filtered := ie.filterArticleImages(allCandidates)

	// Sort by score and area
	ie.sortCandidates(filtered)

	// Return top images (OG + JSON-LD + article body images)
	return ie.getTopImages(filtered, 10) // Allow article images but filter strictly
}

// preprocessHTML removes unwanted sections from the document before processing
func (ie *ImageExtractor) preprocessHTML(doc *goquery.Document) {
	// More aggressive selectors for common non-content areas
	selectors := []string{
		"aside",
		".sidebar",
		".right-rail",
		".side-bar",
		".related-posts",
		".related-articles",
		".recommended-posts",
		".popular-posts",
		"[class*='sidebar']",
		"[id*='sidebar']",
		"[class*='related']",
		"[id*='related']",
		"[class*='popular']",
		"[id*='popular']",
		"nav",
		"footer",
		".lg\\:col-span-1",
	}

	for _, selector := range selectors {
		doc.Find(selector).Remove()
	}

	// Preserve headers that are part of article/main content while removing site-level chrome
	doc.Find("header").Each(func(i int, s *goquery.Selection) {
		if s.ParentsFiltered("article, main").Length() == 0 {
			s.Remove()
		}
	})

	// Remove widget-marked elements only when they are outside article/main content
	doc.Find("[class*='widget'], [id*='widget']").Each(func(i int, s *goquery.Selection) {
		if s.Is("article, main") {
			return
		}
		if ie.isInArticleScope(s) {
			return
		}
		s.Remove()
	})
}

// extractJSONLDImage extracts featured image from JSON-LD structured data
func (ie *ImageExtractor) extractJSONLDImage(doc *goquery.Document, baseURL string) *models.ImageCandidate {
	imageURL := ExtractJSONLDImage(doc)
	if imageURL == "" {
		return nil
	}

	// Convert to absolute URL
	absURL, err := ie.toAbsoluteURL(imageURL, baseURL)
	if err != nil {
		return nil
	}
	absURL = ie.cleanImageURL(absURL)

	// Check if it's an image file
	if !ie.regexes["imageExt"].MatchString(absURL) {
		return nil
	}
	absURL = ie.cleanImageURL(absURL)

	// Try to get article title as alt text (better than nothing)
	alt := ""
	if headline, _, _, found := ExtractJSONLD(doc); found && headline != "" {
		alt = headline
	}

	return &models.ImageCandidate{
		URL:       absURL,
		Alt:       alt,
		Width:     0, // Dimensions usually not in JSON-LD
		Height:    0,
		InArticle: true, // JSON-LD image is the featured article image
		BadHint:   false,
		Source:    "jsonld",
	}
}

// extractOgImage extracts Open Graph image metadata
func (ie *ImageExtractor) extractOgImage(doc *goquery.Document, baseURL string) *models.ImageCandidate {
	var ogImageURL string
	var ogImageAlt string
	var width, height int

	// Find og:image meta tag
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		property, exists := s.Attr("property")
		if !exists {
			return
		}

		switch property {
		case "og:image", "og:image:secure_url":
			if content, exists := s.Attr("content"); exists {
				ogImageURL = content
			}
		case "og:image:alt":
			if content, exists := s.Attr("content"); exists {
				ogImageAlt = content
			}
		case "og:image:width":
			if content, exists := s.Attr("content"); exists {
				if w, err := strconv.Atoi(content); err == nil {
					width = w
				}
			}
		case "og:image:height":
			if content, exists := s.Attr("content"); exists {
				if h, err := strconv.Atoi(content); err == nil {
					height = h
				}
			}
		}
	})

	if ogImageURL == "" {
		return nil
	}

	// Convert to absolute URL
	absURL, err := ie.toAbsoluteURL(ogImageURL, baseURL)
	if err != nil {
		return nil
	}
	absURL = ie.cleanImageURL(absURL)

	// Check if it's an image file
	if !ie.regexes["imageExt"].MatchString(absURL) {
		return nil
	}

	// If dimensions not found in meta tags, try to extract from URL
	if width == 0 || height == 0 {
		urlWidth, urlHeight := ie.parseDimensionsFromURL(absURL)
		if width == 0 {
			width = urlWidth
		}
		if height == 0 {
			height = urlHeight
		}
	}

	return &models.ImageCandidate{
		URL:       absURL,
		Alt:       strings.TrimSpace(ogImageAlt),
		Width:     width,
		Height:    height,
		InArticle: true, // og:image is considered in-article
		BadHint:   false,
		Source:    "og",
	}
}

// extractArticleImgTags extracts img tags ONLY from article/main content areas
func (ie *ImageExtractor) extractArticleImgTags(doc *goquery.Document, baseURL string) []models.ImageCandidate {
	var candidates []models.ImageCandidate

	articleSelectors := []string{
		"main article",
		"article .article-body",
		"article #article-body",
		"section[itemprop='articleBody']",
		"[itemprop='articleBody']",
	}

	selector := strings.Join(articleSelectors, ",")
	doc.Find(selector).Each(func(i int, container *goquery.Selection) {
		if ie.isInExcludedSection(container) {
			return
		}
		container.Find("img").Each(func(j int, s *goquery.Selection) {
			candidate := ie.extractImgTag(s, baseURL)
			if candidate != nil {
				candidate.InArticle = true
				candidates = append(candidates, *candidate)
			}
		})
	})

	return candidates
}

// extractImgTags extracts all img tags from the document (legacy, not used in strict mode)
func (ie *ImageExtractor) extractImgTags(doc *goquery.Document, baseURL string) []models.ImageCandidate {
	var candidates []models.ImageCandidate

	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		candidate := ie.extractImgTag(s, baseURL)
		if candidate != nil {
			candidates = append(candidates, *candidate)
		}
	})

	return candidates
}

// extractImgTag extracts a single img tag
func (ie *ImageExtractor) extractImgTag(s *goquery.Selection, baseURL string) *models.ImageCandidate {
	// Get src attribute or data-src variants
	src := ""
	if dataSrc, exists := s.Attr("data-srcset"); exists {
		src = ie.pickFromSrcset(dataSrc)
	} else if srcAttr, exists := s.Attr("src"); exists {
		src = srcAttr
	} else if dataSrc, exists := s.Attr("data-src"); exists {
		src = dataSrc
	} else if dataOriginal, exists := s.Attr("data-original"); exists {
		src = dataOriginal
	} else if dataLazySrc, exists := s.Attr("data-lazy-src"); exists {
		src = dataLazySrc
	}

	// Try srcset if no src found
	if src == "" {
		if srcset, exists := s.Attr("srcset"); exists {
			src = ie.pickFromSrcset(srcset)
		}
	}

	if src == "" {
		return nil
	}

	// Convert to absolute URL
	absURL, err := ie.toAbsoluteURL(src, baseURL)
	if err != nil {
		return nil
	}

	// Check if it's an image file
	if !ie.regexes["imageExt"].MatchString(absURL) {
		return nil
	}

	originalURL := absURL
	absURL = ie.cleanImageURL(absURL)

	absURL = ie.cleanImageURL(absURL)

	// Extract alt text
	alt, _ := s.Attr("alt")
	alt = strings.TrimSpace(alt)

	// Extract dimensions
	width, height := ie.extractDimensions(s)

	// If dimensions not found in attributes, try URL
	if width == 0 || height == 0 {
		urlWidth, urlHeight := ie.parseDimensionsFromURL(originalURL)
		if width == 0 {
			width = urlWidth
		}
		if height == 0 {
			height = urlHeight
		}
	}

	// Check if in article scope
	inArticle := ie.isInArticleScope(s)

	// Check for bad hints
	badHint := ie.hasBadHint(s, absURL)

	return &models.ImageCandidate{
		URL:       absURL,
		Alt:       alt,
		Width:     width,
		Height:    height,
		InArticle: inArticle,
		BadHint:   badHint,
		Source:    "img",
	}
}

// extractDimensions extracts width and height from img tag
func (ie *ImageExtractor) extractDimensions(s *goquery.Selection) (int, int) {
	width := 0
	height := 0

	// Try width attribute
	if wAttr, exists := s.Attr("width"); exists {
		if w, err := strconv.Atoi(strings.TrimSpace(wAttr)); err == nil {
			width = w
		}
	}

	// Try height attribute
	if hAttr, exists := s.Attr("height"); exists {
		if h, err := strconv.Atoi(strings.TrimSpace(hAttr)); err == nil {
			height = h
		}
	}

	// Try style attribute
	if style, exists := s.Attr("style"); exists {
		widthMatch := ie.regexes["widthStyle"].FindStringSubmatch(style)
		if len(widthMatch) > 1 {
			if w, err := strconv.ParseFloat(widthMatch[1], 64); err == nil {
				width = int(w)
			}
		}

		heightMatch := ie.regexes["heightStyle"].FindStringSubmatch(style)
		if len(heightMatch) > 1 {
			if h, err := strconv.ParseFloat(heightMatch[1], 64); err == nil {
				height = int(h)
			}
		}
	}

	return width, height
}

// parseDimensionsFromURL extracts dimensions from URL patterns
func (ie *ImageExtractor) parseDimensionsFromURL(url string) (int, int) {
	// Try pattern like 300x400
	matches := ie.regexes["dimensionsFromUrl"].FindStringSubmatch(url)
	if len(matches) > 2 {
		if w, err := strconv.Atoi(matches[1]); err == nil {
			if h, err := strconv.Atoi(matches[2]); err == nil {
				return w, h
			}
		}
	}

	// Try separate width and height parameters
	widthMatch := ie.regexes["widthFromUrl"].FindStringSubmatch(url)
	heightMatch := ie.regexes["heightFromUrl"].FindStringSubmatch(url)

	width := 0
	height := 0

	if len(widthMatch) > 1 {
		if w, err := strconv.Atoi(widthMatch[1]); err == nil {
			width = w
		}
	}

	if len(heightMatch) > 1 {
		if h, err := strconv.Atoi(heightMatch[1]); err == nil {
			height = h
		}
	}

	return width, height
}

// pickFromSrcset selects the best image from srcset
func (ie *ImageExtractor) pickFromSrcset(srcset string) string {
	items := strings.Split(srcset, ",")
	var candidates []struct {
		url string
		w   int
	}

	for _, item := range items {
		item = strings.TrimSpace(item)
		matches := ie.regexes["srcsetItem"].FindStringSubmatch(item)
		if len(matches) > 2 {
			if w, err := strconv.Atoi(matches[2]); err == nil {
				candidates = append(candidates, struct {
					url string
					w   int
				}{matches[1], w})
			}
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	// Find closest to TargetImageWidth, preferring larger images
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		candidateDiff := absInt(candidate.w - TargetImageWidth)
		bestDiff := absInt(best.w - TargetImageWidth)
		if candidateDiff < bestDiff ||
			(candidateDiff == bestDiff && candidate.w > best.w) {
			best = candidate
		}
	}

	return best.url
}

// isInArticleScope checks if the img tag is within article or main tags
func (ie *ImageExtractor) isInArticleScope(s *goquery.Selection) bool {
	// Check if any parent is within recognized article containers
	articleSelectors := []string{
		"article",
		"main",
		"section[itemprop='articleBody']",
		"[itemprop='articleBody']",
		"section.article-body",
		"#article-body",
	}

	selector := strings.Join(articleSelectors, ",")
	parents := s.Parents()
	for i := 0; i < parents.Length(); i++ {
		parent := parents.Eq(i)
		if ie.isInExcludedSection(parent) {
			return false
		}
		if parent.Is(selector) {
			return true
		}
	}

	return false
}

// hasBadHint checks if the image has bad hints (ads, icons, etc.)
func (ie *ImageExtractor) hasBadHint(s *goquery.Selection, url string) bool {
	// Check URL for bad patterns
	if ie.regexes["badHint"].MatchString(url) {
		return true
	}

	// Check img tag attributes and classes
	html, _ := s.Html()
	return ie.regexes["badHint"].MatchString(html)
}

// isExcludedSection checks if a container should be excluded from image extraction
func (ie *ImageExtractor) isExcludedSection(s *goquery.Selection) bool {
	// Get all class names and IDs
	classes, _ := s.Attr("class")
	id, _ := s.Attr("id")
	dataAttrs := ""

	// Collect all data-* attributes
	for _, attr := range []string{"data-widget", "data-component", "data-type", "data-section"} {
		if val, exists := s.Attr(attr); exists {
			dataAttrs += val + " "
		}
	}

	combined := strings.ToLower(classes + " " + id + " " + dataAttrs)

	// Patterns that indicate non-article sections
	exclusionPatterns := []string{
		"sidebar",
		"side-bar",
		"side_bar",
		"related", "related-posts", "related-articles", "related-content", "related-stories",
		"featured", "featured-posts", "featured-articles", "featured-content", "featured-stories",
		"popular", "trending", "most-read", "most-popular",
		"recommended", "recommendation", "you-may-like",
		"widget",
		"banner",
		"advertisement", "ad-", "advert",
		"navigation", "nav-", "navbar",
		"menu",
		"footer",
		"header",
		"carousel",
		"slider",
		"gallery", // Unless it's the article gallery
		"aside",
		"promo", "promotion",
		"sponsored",
		"more-stories", "more-articles", "more-news",
		"latest-posts", "latest-articles", "latest-news", "latest-stories",
		"recent-posts", "recent-articles", "recent-news",
		"top-stories", "top-posts",
		"highlights",
		"must-read",
		"dont-miss",
		"editor-pick", "editor-choice",
		"read-more", "read-next",
		"story-list", "post-list",
		"grid-items", "list-items",
	}

	for _, pattern := range exclusionPatterns {
		if strings.Contains(combined, pattern) {
			return true
		}
	}

	return false
}

// isInExcludedSection checks if an element is within an excluded section
func (ie *ImageExtractor) isInExcludedSection(s *goquery.Selection) bool {
	// Check if any parent matches exclusion criteria
	parents := s.Parents()
	for i := 0; i < parents.Length(); i++ {
		parent := parents.Eq(i)
		if ie.isExcludedSection(parent) {
			return true
		}
	}

	// Additional check: Exclude images that are inside link-heavy containers
	// This catches "related posts" sections that are lists of linked thumbnails
	if ie.isInLinkList(s) {
		return true
	}

	return false
}

// isInLinkList checks if an image is in a list of links (common for related posts)
func (ie *ImageExtractor) isInLinkList(s *goquery.Selection) bool {
	// Look up the tree for a container with multiple links (thumbnail grid pattern)
	parents := s.Parents()
	for i := 0; i < parents.Length() && i < 5; i++ { // Check up to 5 levels up
		parent := parents.Eq(i)

		// Count links in this container
		linkCount := parent.Find("a").Length()

		// Count images in this container
		imgCount := parent.Find("img").Length()

		// If there are multiple links and multiple images, it's likely a thumbnail grid
		// Skip if this looks like a "related posts" grid (3+ links, 2+ images)
		if linkCount >= 3 && imgCount >= 2 {
			// Extra check: if the container has list-like structure
			tagName := goquery.NodeName(parent)
			if tagName == "ul" || tagName == "ol" || tagName == "nav" {
				return true // Definitely a navigation/list
			}

			// Check if images are small (thumbnails vs article images)
			// This prevents false positives on article galleries
			hasLargeImage := false
			smallImageCount := 0

			parent.Find("img").Each(func(j int, img *goquery.Selection) {
				// Check width attribute
				if width, exists := img.Attr("width"); exists {
					if w, err := strconv.Atoi(width); err == nil {
						if w > 400 {
							hasLargeImage = true
						} else if w > 0 {
							smallImageCount++
						}
					}
				}
				// Also check style attribute for width
				if !hasLargeImage {
					if style, exists := img.Attr("style"); exists {
						widthMatch := ie.regexes["widthStyle"].FindStringSubmatch(style)
						if len(widthMatch) > 1 {
							if w, err := strconv.ParseFloat(widthMatch[1], 64); err == nil && w > 400 {
								hasLargeImage = true
							}
						}
					}
				}
			})

			// Only exclude if:
			// 1. Container has list-like structure (already checked above), OR
			// 2. All images are explicitly small (width <= 400) AND high link density
			// If ANY image is large, or no width info, assume it's article content
			if !hasLargeImage && smallImageCount >= 2 && linkCount >= 6 {
				// High threshold: 6+ links and 2+ small images = related posts
				return true
			}
		}
	}

	return false
}

// filterArticleImages strictly filters to only OG/JSON-LD images or images within article content
func (ie *ImageExtractor) filterArticleImages(candidates []models.ImageCandidate) []models.ImageCandidate {
	var filtered []models.ImageCandidate

	for _, c := range candidates {
		if !c.InArticle {
			continue
		}

		// Apply basic quality filters
		if !ie.passesBasicFilters(c) {
			continue
		}

		// Calculate score
		c.Score = ie.calculateScore(c)
		c.Area = c.Width * c.Height
		filtered = append(filtered, c)
	}

	return filtered
}

// filterAndScoreCandidates filters and scores image candidates (legacy)
func (ie *ImageExtractor) filterAndScoreCandidates(candidates []models.ImageCandidate) []models.ImageCandidate {
	var filtered []models.ImageCandidate

	for _, c := range candidates {
		if !ie.passesFilters(c) {
			continue
		}

		// Calculate score
		c.Score = ie.calculateScore(c)
		c.Area = c.Width * c.Height
		filtered = append(filtered, c)
	}

	return filtered
}

func (ie *ImageExtractor) passesBasicFilters(c models.ImageCandidate) bool {
	// For article images, apply minimal filtering
	if c.Width > 0 && c.Height > 0 {
		if c.Width <= c.Height {
			return false
		}
		if c.Width < 600 {
			return false
		}
		// Only filter out very small images (likely icons)
		shortSide := min(c.Width, c.Height)
		if shortSide < 100 {
			return false
		}

		// Filter out obvious ad sizes
		if ie.isAdSize(c.Width, c.Height) {
			return false
		}

		// Filter bad hints only for very small images
		if c.BadHint && shortSide < 200 {
			return false
		}
	} else {
		// Unknown dimensions - only filter if has bad hints
		if c.BadHint {
			return false
		}
	}

	return true
}

// passesFilters checks if a candidate passes all filters (legacy, stricter)
func (ie *ImageExtractor) passesFilters(c models.ImageCandidate) bool {
	if c.Width > 0 && c.Height > 0 {
		shortSide := min(c.Width, c.Height)
		area := c.Width * c.Height

		// Size filters
		if shortSide < ie.config.MinShortSide {
			return false
		}
		if area < ie.config.MinArea {
			return false
		}

		// Aspect ratio filter
		if !ie.hasGoodAspectRatio(c.Width, c.Height) {
			return false
		}

		// Ad size filter
		if ie.isAdSize(c.Width, c.Height) {
			return false
		}

		// Bad hint filter with exceptions
		if c.BadHint && !(shortSide >= 400 && area >= 300000) {
			return false
		}
	} else if c.BadHint {
		return false
	}

	return true
}

// hasGoodAspectRatio checks if the aspect ratio is acceptable
func (ie *ImageExtractor) hasGoodAspectRatio(width, height int) bool {
	if width == 0 || height == 0 {
		return false
	}

	aspect := float64(width) / float64(height)

	// Check if within general bounds
	if aspect >= ie.config.MinAspect && aspect <= ie.config.MaxAspect {
		return true
	}

	// Check whitelist ratios
	for _, ratio := range ie.config.RatioWhitelist {
		if abs(aspect-ratio) <= ie.config.RatioTol {
			return true
		}
	}

	return false
}

// isAdSize checks if dimensions match common ad sizes
func (ie *ImageExtractor) isAdSize(width, height int) bool {
	if width == 0 || height == 0 {
		return false
	}

	sizeKey := fmt.Sprintf("%dx%d", width, height)
	return ie.config.AdSizes[sizeKey]
}

// calculateScore calculates the score for a candidate
func (ie *ImageExtractor) calculateScore(c models.ImageCandidate) float64 {
	score := 0.0
	area := float64(c.Width * c.Height)

	// Article boost
	if c.InArticle {
		score += 2.0
	}

	// Structured data boost (OG or JSON-LD)
	if c.Source == "og" || c.Source == "jsonld" {
		score += 1.5 // Higher priority for publisher-curated images
	}

	// Aspect ratio bonus
	if c.Width > 0 && c.Height > 0 {
		aspect := float64(c.Width) / float64(c.Height)
		for _, ratio := range ie.config.RatioWhitelist {
			if abs(aspect-ratio) <= ie.config.RatioTol {
				score += 1.0
				break
			}
		}
	}

	// Area bonus (logarithmic)
	if area > 0 {
		score += log10(max(1, area))
	}

	return score
}

// sortCandidates sorts candidates by score and area
func (ie *ImageExtractor) sortCandidates(candidates []models.ImageCandidate) {
	// Simple bubble sort for small arrays
	n := len(candidates)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if candidates[j].Score < candidates[j+1].Score ||
				(candidates[j].Score == candidates[j+1].Score && candidates[j].Area < candidates[j+1].Area) {
				candidates[j], candidates[j+1] = candidates[j+1], candidates[j]
			}
		}
	}
}

// getTopImages returns the top N unique images with URL and alt text
func (ie *ImageExtractor) getTopImages(candidates []models.ImageCandidate, limit int) []models.Image {
	urlToImage := make(map[string]models.Image)
	var orderedURLs []string

	for _, c := range candidates {
		if _, exists := urlToImage[c.URL]; !exists {
			urlToImage[c.URL] = models.Image{
				URL: c.URL,
				Alt: c.Alt,
			}
			orderedURLs = append(orderedURLs, c.URL)
		} else if urlToImage[c.URL].Alt == "" && c.Alt != "" {
			// If the existing image has no alt text, but this one does, update it.
			img := urlToImage[c.URL]
			img.Alt = c.Alt
			urlToImage[c.URL] = img
		}
	}

	var result []models.Image
	for i, url := range orderedURLs {
		if i >= limit {
			break
		}
		result = append(result, urlToImage[url])
	}

	return result
}

// toAbsoluteURL converts a relative URL to absolute
func (ie *ImageExtractor) toAbsoluteURL(relativeURL, baseURL string) (string, error) {
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

// cleanImageURL removes common resizing/cropping parameters from the query string to keep canonical image URLs.
func (ie *ImageExtractor) cleanImageURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	parsed.RawQuery = ""
	return parsed.String()
}

func normalizeParamKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if unicode.IsLetter(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Helper functions
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func log10(x float64) float64 {
	// Simple log10 approximation for scoring
	if x <= 0 {
		return 0
	}

	// Count digits
	count := 0
	for x >= 10 {
		x /= 10
		count++
	}

	return float64(count)
}
