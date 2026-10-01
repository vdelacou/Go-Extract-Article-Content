// Package scraper provides text processing utilities for content extraction.
package scraper

import (
	"errors"
	"strings"

	"extract-html-scraper/internal/models"
)

// CleanWhitespace removes excessive whitespace from text content
func CleanWhitespace(text string) string {
	if text == "" {
		return ""
	}

	// Remove excessive whitespace
	cleaned := strings.ReplaceAll(text, TripleNewline, DoubleNewline)
	cleaned = strings.ReplaceAll(cleaned, DoubleSpace, SingleSpace)
	cleaned = strings.TrimSpace(cleaned)

	return cleaned
}

// CleanTextContent removes common noise patterns from text content
func CleanTextContent(text string) string {
	if text == "" {
		return ""
	}

	// Remove very short lines that are likely UI elements
	lines := strings.Split(text, "\n")
	var cleanedLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Keep lines that are longer than 20 characters or are empty (for spacing)
		if len(line) == 0 || len(line) > 20 {
			cleanedLines = append(cleanedLines, line)
		}
	}

	cleaned := strings.Join(cleanedLines, "\n")
	return CleanWhitespace(cleaned)
}

// CalculateContentMetrics calculates basic content quality metrics
func CalculateContentMetrics(content string) (wordCount, paragraphCount, avgParagraphLength int) {
	if content == "" {
		return 0, 0, 0
	}

	// Count paragraphs (non-empty lines)
	lines := strings.Split(content, "\n")
	paragraphCount = 0
	totalChars := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 0 {
			paragraphCount++
			totalChars += len(line)
		}
	}

	// Rough word count estimation (5 chars per word average)
	wordCount = len(strings.ReplaceAll(content, " ", "")) / 5

	// Average paragraph length
	if paragraphCount > 0 {
		avgParagraphLength = totalChars / paragraphCount
	}

	return wordCount, paragraphCount, avgParagraphLength
}

// ContainsAny checks if a string contains any of the substrings (case-insensitive)
func ContainsAny(s string, substrings []string) bool {
	sLower := strings.ToLower(s)
	for _, substr := range substrings {
		if strings.Contains(sLower, strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

// IsCloudflareBlock reports whether err carries a Cloudflare challenge/block verdict.
// It is type-based: error *strings* such as "HTTP 403" or anything containing the word
// "cloudflare" are no longer treated as a Cloudflare block.
func IsCloudflareBlock(err error) bool {
	var cf *models.CloudflareBlockError
	return errors.As(err, &cf)
}

// BuildStructuredText extracts text content preserving structure from HTML elements
func BuildStructuredText(doc interface{}, elements string) string {
	// This will be implemented with goquery integration
	// For now, return empty string as placeholder
	return ""
}
