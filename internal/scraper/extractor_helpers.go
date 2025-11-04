// Package scraper provides helper functions for article content extraction.
package scraper

import (
	"encoding/json"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// FindMetaTag searches for a meta tag with the given property or name
func FindMetaTag(doc *goquery.Document, property, name string) string {
	var value string

	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		if value != "" {
			return // Already found
		}

		// Check property attribute
		if property != "" {
			if prop, exists := s.Attr("property"); exists && prop == property {
				if content, exists := s.Attr("content"); exists {
					value = strings.TrimSpace(content)
					return
				}
			}
		}

		// Check name attribute
		if name != "" {
			if n, exists := s.Attr("name"); exists && n == name {
				if content, exists := s.Attr("content"); exists {
					value = strings.TrimSpace(content)
					return
				}
			}
		}
	})

	return value
}

// ExtractTextFromElements extracts text content preserving structure from HTML elements
func ExtractTextFromElements(selection *goquery.Selection, elements string) string {
	var content strings.Builder

	selection.Find(elements).Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			return
		}

		tagName := goquery.NodeName(s)
		switch tagName {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			if content.Len() > 0 {
				content.WriteString(DoubleNewline)
			}
			content.WriteString(text)
			content.WriteString(SingleNewline)
		case "p", "li", "blockquote":
			if content.Len() > 0 {
				content.WriteString(SingleNewline)
			}
			content.WriteString(text)
		}
	})

	return content.String()
}

// ExtractFallbackText extracts all text content when structured extraction fails
func ExtractFallbackText(selection *goquery.Selection) string {
	// Remove non-content elements
	selection.Find(NonContentTags).Remove()

	// Extract all text
	text := strings.TrimSpace(selection.Text())
	return text
}

// FindContentContainer finds the main content container using common selectors
func FindContentContainer(doc *goquery.Document) *goquery.Selection {
	selectors := strings.Split(ContentSelectors, ", ")

	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if doc.Find(selector).Length() > 0 {
			return doc.Find(selector).First()
		}
	}

	// Fallback to body
	return doc.Find("body")
}

// ExtractDescriptionFromParagraph extracts description from first suitable paragraph
func ExtractDescriptionFromParagraph(doc *goquery.Document) string {
	var description string

	doc.Find("p").First().Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if len(text) > MinDescriptionLen && len(text) < MaxDescriptionLen {
			description = text
		}
	})

	return description
}

// JSONLDArticle represents schema.org Article or NewsArticle JSON-LD data
type JSONLDArticle struct {
	Type            interface{} `json:"@type"`
	Headline        string      `json:"headline"`
	ArticleBody     string      `json:"articleBody"`
	Description     string      `json:"description"`
	Author          interface{} `json:"author"`
	DatePublished   string      `json:"datePublished"`
	DateModified    string      `json:"dateModified"`
	Image           interface{} `json:"image"`
	Publisher       interface{} `json:"publisher"`
	ArticleSection  string      `json:"articleSection"`
	Keywords        interface{} `json:"keywords"`
	WordCount       int         `json:"wordCount"`
	InLanguage      string      `json:"inLanguage"`
	IsAccessibleFor interface{} `json:"isAccessibleForFree"`
}

// ExtractJSONLD attempts to extract article content from schema.org JSON-LD structured data
// Returns headline, body content, and description if found
func ExtractJSONLD(doc *goquery.Document) (headline, body, description string, found bool) {
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		if found {
			return // Already found valid data
		}

		jsonText := s.Text()
		if jsonText == "" {
			return
		}

		// Try parsing as single object first
		var article JSONLDArticle
		if err := json.Unmarshal([]byte(jsonText), &article); err == nil {
			if isArticleType(article.Type) && article.Headline != "" {
				headline = article.Headline
				body = article.ArticleBody
				description = article.Description
				found = true
				return
			}
		}

		// Try parsing as array of objects
		var articles []JSONLDArticle
		if err := json.Unmarshal([]byte(jsonText), &articles); err == nil {
			for _, article := range articles {
				if isArticleType(article.Type) && article.Headline != "" {
					headline = article.Headline
					body = article.ArticleBody
					description = article.Description
					found = true
					return
				}
			}
		}
	})

	return
}

// isArticleType checks if the @type field indicates an article
func isArticleType(typeField interface{}) bool {
	if typeField == nil {
		return false
	}

	// Handle string type
	if typeStr, ok := typeField.(string); ok {
		return strings.Contains(strings.ToLower(typeStr), "article") ||
			strings.Contains(strings.ToLower(typeStr), "newsarticle") ||
			strings.Contains(strings.ToLower(typeStr), "blogposting")
	}

	// Handle array of types
	if typeArr, ok := typeField.([]interface{}); ok {
		for _, t := range typeArr {
			if typeStr, ok := t.(string); ok {
				if strings.Contains(strings.ToLower(typeStr), "article") ||
					strings.Contains(strings.ToLower(typeStr), "newsarticle") ||
					strings.Contains(strings.ToLower(typeStr), "blogposting") {
					return true
				}
			}
		}
	}

	return false
}
