// Package models defines typed errors for better error handling and context.
package models

import "fmt"

// CloudflareBlockError represents a Cloudflare blocking error
type CloudflareBlockError struct {
	Domain string
	Kind   string // "challenge" or "block"
	Status int    // HTTP status of the challenge/block response (0 if unknown, e.g. browser DOM)
	RayID  string
	Err    error
}

func (e *CloudflareBlockError) Error() string {
	return fmt.Sprintf("blocked by Cloudflare (%s, status %d, ray %s) on domain %s: %v", e.Kind, e.Status, e.RayID, e.Domain, e.Err)
}

func (e *CloudflareBlockError) Unwrap() error { return e.Err }

// TimeoutError represents a timeout error
type TimeoutError struct {
	Operation string
	Timeout   string
	Err       error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timeout during %s after %s: %v", e.Operation, e.Timeout, e.Err)
}

// InvalidURLError represents an invalid URL error
type InvalidURLError struct {
	URL string
	Err error
}

func (e *InvalidURLError) Error() string {
	return fmt.Sprintf("invalid URL %s: %v", e.URL, e.Err)
}

// HTTPError represents an HTTP-related error
type HTTPError struct {
	StatusCode int
	URL        string
	Err        error
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d for URL %s: %v", e.StatusCode, e.URL, e.Err)
}

// ContentExtractionError represents an error during content extraction
type ContentExtractionError struct {
	Step string
	Err  error
}

func (e *ContentExtractionError) Error() string {
	return fmt.Sprintf("content extraction failed at %s: %v", e.Step, e.Err)
}
