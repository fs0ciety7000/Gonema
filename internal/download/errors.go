package download

import (
	"errors"
	"fmt"
)

// Sentinel errors, matched with errors.Is. Their text is plain language: it is
// shown as is during onboarding.
var (
	ErrUnreachable    = errors.New("cannot reach the download client: check the host, the port and that it is running")
	ErrAuth           = errors.New("the download client rejected the credentials")
	ErrURLBase        = errors.New("the download client answered \"not found\": check the URL base (reverse proxy path)")
	ErrTLS            = errors.New("secure connection failed: check whether the client uses HTTPS and that its certificate is trusted")
	ErrAPIDisabled    = errors.New("the API of the download client is disabled or not accessible")
	ErrUnexpected     = errors.New("unexpected answer: is this the right kind of download client?")
	ErrRejected       = errors.New("the download client refused the request")
	ErrNotFound       = errors.New("download not found in the client")
	ErrUnsupported    = errors.New("not supported by this download client")
	ErrInvalidRequest = errors.New("invalid request")
	ErrUnknownRef     = errors.New("the release was added but the client did not report its identifier")
)

// Error is returned by every client. It matches its Kind sentinel and the
// context error when the call was canceled or timed out.
type Error struct {
	Client Kind
	Op     string // test, add, items, remove, pause, resume, categories
	Kind   error  // one of the sentinels above
	Detail string // extra plain-language detail, never contains credentials
	cause  error  // redacted cause (message only) matched by Is; may be nil
}

// NewError builds an Error. cause must already be redacted: its message is
// included in Error().
func NewError(client Kind, op string, kind error, detail string, cause error) *Error {
	return &Error{Client: client, Op: op, Kind: kind, Detail: detail, cause: cause}
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s: %s: %v", e.Client, e.Op, e.Kind)
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	if e.cause != nil {
		s += ": " + e.cause.Error()
	}
	return s
}

// Unwrap returns the kind sentinel and the cause.
func (e *Error) Unwrap() []error {
	if e.cause == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.cause}
}
