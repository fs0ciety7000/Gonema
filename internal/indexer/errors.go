package indexer

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors, matched with errors.Is. Each *Error matches exactly one.
var (
	ErrBadURL       = errors.New("the indexer URL is not valid: it must start with http:// or https:// and contain a host name")
	ErrUnreachable  = errors.New("the indexer could not be reached: check the address and that the server is online")
	ErrTLS          = errors.New("the secure connection (HTTPS) to the indexer failed: the certificate is invalid or the port does not speak HTTPS")
	ErrTimeout      = errors.New("the indexer did not answer in time")
	ErrUnauthorized = errors.New("the indexer rejected the API key: check that it was copied in full")
	ErrSuspended    = errors.New("the indexer account is suspended")
	ErrForbidden    = errors.New("the indexer account is not allowed to use this function")
	ErrRateLimited  = errors.New("the indexer request limit is reached")
	ErrNotIndexer   = errors.New("the address does not answer like a Newznab or Torznab indexer: check the URL and the API path")
	ErrBadResponse  = errors.New("the indexer sent an invalid response")
	ErrUnsupported  = errors.New("the indexer does not support this function")
	ErrBadRequest   = errors.New("the indexer refused the request parameters")
	ErrDisabled     = errors.New("the indexer API is disabled")
	ErrServer       = errors.New("the indexer reported an internal error")
)

// Error is a failure of an indexer call. Kind is one of the sentinel errors.
// Message never contains credentials.
type Error struct {
	Kind       error
	Indexer    string        // indexer name, may be empty
	HTTPStatus int           // 0 when the failure is not an HTTP answer
	Code       int           // Newznab error code, 0 if none
	Message    string        // detail from the indexer or the transport, redacted
	RetryAfter time.Duration // advised delay (rate limits), 0 if none
	Err        error         // underlying error (net, context, xml), may be nil
}

func (e *Error) Error() string {
	s := e.Kind.Error()
	if e.Indexer != "" {
		s = e.Indexer + ": " + s
	}
	switch {
	case e.Code != 0 && e.Message != "":
		s += fmt.Sprintf(" (code %d: %s)", e.Code, e.Message)
	case e.Code != 0:
		s += fmt.Sprintf(" (code %d)", e.Code)
	case e.HTTPStatus != 0 && e.Message != "":
		s += fmt.Sprintf(" (HTTP %d: %s)", e.HTTPStatus, e.Message)
	case e.HTTPStatus != 0:
		s += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	case e.Message != "":
		s += " (" + e.Message + ")"
	}
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(", retry after %s", e.RetryAfter)
	}
	return s
}

// Unwrap returns the kind and the underlying error.
func (e *Error) Unwrap() []error {
	if e.Err != nil {
		return []error{e.Kind, e.Err}
	}
	return []error{e.Kind}
}

// RetryAfter returns the delay advised by a rate-limited indexer in err's chain,
// or 0.
func RetryAfter(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}
