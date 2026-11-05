package metadata

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors, matched with errors.Is.
var (
	ErrNotFound     = errors.New("metadata: not found")
	ErrUnauthorized = errors.New("metadata: credential rejected")
	ErrRateLimited  = errors.New("metadata: rate limited")
)

// RateLimitError is returned when the provider keeps answering "too many
// requests". It matches ErrRateLimited.
type RateLimitError struct {
	// RetryAfter is the delay advised by the provider; 0 if it gave none.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("metadata: rate limited, retry after %s", e.RetryAfter)
	}
	return "metadata: rate limited"
}

// Is reports whether target is ErrRateLimited.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// APIError is an error answered by the provider (HTTP status and provider message).
// It wraps ErrNotFound or ErrUnauthorized when the status means so.
type APIError struct {
	Provider   string // "tmdb"
	HTTPStatus int
	Code       int    // provider-specific status code, 0 if absent
	Message    string // provider message, never contains credentials
	kind       error  // ErrNotFound, ErrUnauthorized or nil
}

// NewAPIError builds an APIError; kind is ErrNotFound, ErrUnauthorized or nil.
func NewAPIError(provider string, httpStatus, code int, message string, kind error) *APIError {
	return &APIError{Provider: provider, HTTPStatus: httpStatus, Code: code, Message: message, kind: kind}
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "no message"
	}
	return fmt.Sprintf("metadata: %s answered HTTP %d: %s", e.Provider, e.HTTPStatus, msg)
}

// Unwrap returns the sentinel this error stands for, if any.
func (e *APIError) Unwrap() error { return e.kind }
