// Package main provides the Google Cloud Run HTTP handler for the web scraper service.
// It handles incoming requests, performs web scraping operations,
// and returns structured JSON responses with extracted article content.
// API key authentication is handled in this service.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"extract-html-scraper/internal/models"
	"extract-html-scraper/internal/scraper"
)

// CloudRunHandler handles Google Cloud Run requests
type CloudRunHandler struct {
	scraper  *scraper.Scraper
	apiKeys  []string
	keysLock sync.RWMutex
}

func NewCloudRunHandler() *CloudRunHandler {
	handler := &CloudRunHandler{
		scraper: scraper.NewScraper(),
	}

	// Load API keys on initialization
	handler.loadAPIKeys()

	return handler
}

// loadAPIKeys loads API keys from environment variable or Secret Manager
func (h *CloudRunHandler) loadAPIKeys() {
	h.keysLock.Lock()
	defer h.keysLock.Unlock()

	// Try Secret Manager first if configured
	secretName := os.Getenv("SCRAPER_API_KEY_SECRET")
	if secretName != "" {
		if keys, err := loadKeysFromSecretManager(secretName); err == nil && len(keys) > 0 {
			h.apiKeys = keys
			fmt.Printf("Loaded %d API key(s) from Secret Manager (secret: %s)\n", len(keys), secretName)
			return
		}
		fmt.Printf("Warning: Failed to load from Secret Manager, falling back to environment variable\n")
	}

	// Fall back to environment variable
	keysStr := os.Getenv("SCRAPER_API_KEYS")
	if keysStr == "" {
		fmt.Printf("Warning: No API keys configured (SCRAPER_API_KEYS not set)\n")
		h.apiKeys = []string{}
		return
	}

	// Parse comma-separated keys
	keys := strings.Split(keysStr, ",")
	h.apiKeys = make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key != "" {
			h.apiKeys = append(h.apiKeys, key)
		}
	}

	fmt.Printf("Loaded %d API key(s) from environment variable\n", len(h.apiKeys))
}

// loadKeysFromSecretManager loads API keys from Google Secret Manager
// Returns error if Secret Manager is not available or secret doesn't exist
func loadKeysFromSecretManager(secretName string) ([]string, error) {
	// For now, return error to indicate Secret Manager is not implemented
	// This can be enhanced later with the secretmanager client library
	// The structure is here to support future enhancement
	return nil, fmt.Errorf("Secret Manager integration not implemented - use SCRAPER_API_KEYS environment variable")
}

// validateAPIKey validates the API key from the request against configured keys
// Uses constant-time comparison to prevent timing attacks
func (h *CloudRunHandler) validateAPIKey(requestKey string) bool {
	h.keysLock.RLock()
	defer h.keysLock.RUnlock()

	// If no keys configured, allow all requests (development mode)
	if len(h.apiKeys) == 0 {
		return true
	}

	// Constant-time comparison against all valid keys
	for _, validKey := range h.apiKeys {
		if subtle.ConstantTimeCompare([]byte(requestKey), []byte(validKey)) == 1 {
			return true
		}
	}

	return false
}

// Handler is the main Cloud Run handler function
func (h *CloudRunHandler) Handler(w http.ResponseWriter, r *http.Request) {
	// Set up CORS headers
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET,OPTIONS")

	// Handle preflight OPTIONS request
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Only allow GET requests
	if r.Method != "GET" {
		h.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Log the request
	fmt.Printf("Request received: %s %s\n", r.Method, r.URL.String())

	// Validate API key
	requestKey := r.URL.Query().Get("key")
	if !h.validateAPIKey(requestKey) {
		h.errorResponse(w, http.StatusUnauthorized, "Invalid or missing API key")
		return
	}

	// Validate URL parameter
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		h.errorResponse(w, http.StatusBadRequest, "Missing \"url\" query parameter")
		return
	}

	// Validate URL format
	if _, err := url.Parse(targetURL); err != nil {
		h.errorResponse(w, http.StatusBadRequest, "Invalid URL format")
		return
	}

	fmt.Printf("Starting scrape for: %s\n", targetURL)

	// Calculate timeout (Cloud Run has 5 minute max)
	timeoutStr := r.URL.Query().Get("timeout")
	timeoutMs := 300000 // Default 5 minutes
	if timeoutStr != "" {
		if parsedTimeout, err := strconv.Atoi(timeoutStr); err == nil {
			timeoutMs = parsedTimeout
		}
	}

	// Cap at 4 minutes (240 seconds) to be safe
	if timeoutMs > 240000 {
		timeoutMs = 240000
	}
	if timeoutMs < 1000 {
		timeoutMs = 1000
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	fmt.Printf("Request timeout: %dms\n", timeoutMs)

	start := time.Now()

	// Perform scraping
	result, err := h.scraper.ScrapeSmartWithTimeout(ctx, targetURL, timeoutMs)

	duration := time.Since(start)
	fmt.Printf("✓ Scraped in %dms\n", duration.Milliseconds())

	// Handle Cloudflare blocking
	if cfErr, ok := err.(*models.CloudflareBlockError); ok {
		blockedResponse := models.BlockedResponse{
			Error:    "Blocked by site protection",
			Provider: "cloudflare",
			Domain:   cfErr.Domain,
			Metadata: models.Metadata{
				URL:        targetURL,
				ScrapedAt:  time.Now(),
				DurationMs: duration.Milliseconds(),
			},
		}

		w.WriteHeader(http.StatusUnavailableForLegalReasons)
		json.NewEncoder(w).Encode(blockedResponse)
		return
	}

	// Handle timeout
	if err != nil && strings.Contains(err.Error(), "context deadline exceeded") {
		fmt.Printf("Error: Scraping timeout after %dms for URL: %s\n", duration.Milliseconds(), targetURL)
		h.errorResponse(w, http.StatusGatewayTimeout, "Scrape took too long")
		return
	}

	// Handle other errors
	if err != nil {
		// Log full error details for debugging
		fmt.Printf("Error processing request for URL: %s\n", targetURL)
		fmt.Printf("Error type: %T\n", err)
		fmt.Printf("Error message: %v\n", err)
		fmt.Printf("Request timeout was: %dms, actual duration: %dms\n", timeoutMs, duration.Milliseconds())

		// Create sanitized error message for response
		errorMsg := sanitizeErrorMessage(err)
		finalMsg := fmt.Sprintf("Failed to scrape: %s", errorMsg)

		h.errorResponse(w, http.StatusInternalServerError, finalMsg)
		return
	}

	// Add metadata to successful response
	result.Metadata.URL = targetURL
	result.Metadata.ScrapedAt = time.Now()
	result.Metadata.DurationMs = duration.Milliseconds()

	// Return successful response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// sanitizeErrorMessage sanitizes error messages for public responses
// Truncates long messages, removes sensitive info, but keeps enough detail for debugging
func sanitizeErrorMessage(err error) string {
	if err == nil {
		return "unknown error"
	}

	errorMsg := err.Error()

	// Check if verbose errors are enabled
	verboseErrors := os.Getenv("VERBOSE_ERRORS") == "true" || os.Getenv("DEBUG") == "true"

	// If verbose errors are enabled, return full message (up to limit)
	if verboseErrors {
		if len(errorMsg) > 500 {
			return errorMsg[:500] + "..."
		}
		return errorMsg
	}

	// Otherwise, sanitize and truncate
	// Remove potential sensitive paths
	errorMsg = strings.ReplaceAll(errorMsg, "/app/", "")
	errorMsg = strings.ReplaceAll(errorMsg, "/tmp/", "")

	// Extract key error types
	if strings.Contains(errorMsg, "context deadline exceeded") || strings.Contains(errorMsg, "timeout") {
		return "timeout: request took too long"
	}
	if strings.Contains(errorMsg, "HTTP 403") || strings.Contains(errorMsg, "403") {
		return "access denied: site blocked the request"
	}
	if strings.Contains(errorMsg, "HTTP 404") || strings.Contains(errorMsg, "404") {
		return "not found: URL does not exist"
	}
	if strings.Contains(errorMsg, "network") || strings.Contains(errorMsg, "connection") {
		return "network error: could not connect to target site"
	}
	if strings.Contains(errorMsg, "all URLs failed") || strings.Contains(errorMsg, "all alternate URLs") {
		return "all scraping attempts failed"
	}

	// Truncate to 200 chars for generic errors
	if len(errorMsg) > 200 {
		return errorMsg[:200] + "..."
	}

	return errorMsg
}

// errorResponse creates an error response
func (h *CloudRunHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	errorResp := models.ErrorResponse{
		Error: message,
	}

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(errorResp)
}

// main function
func main() {
	handler := NewCloudRunHandler()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Starting server on port %s\n", port)
	http.HandleFunc("/", handler.Handler)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Server failed to start: %v\n", err)
		os.Exit(1)
	}
}
