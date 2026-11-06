package acquire

import (
	"errors"
	"fmt"

	"github.com/fs0ciety7000/gonema/internal/decision"
	"github.com/fs0ciety7000/gonema/internal/store"
)

// Error kinds, matched with errors.Is. Not-found errors also match
// store.ErrNotFound and conflicts store.ErrConflict.
var (
	// ErrInvalid: an input was rejected (bad URL, unknown kind...).
	ErrInvalid = errors.New("acquire: invalid input")
	// ErrRefused: a grab was refused because the release is rejected and the
	// request did not ask for an override.
	ErrRefused = errors.New("acquire: release rejected")
	// ErrUnavailable: nothing can serve the request (no indexer or download
	// client configured for it).
	ErrUnavailable = errors.New("acquire: not configured")
	// ErrUpstream: an indexer or a download client failed.
	ErrUpstream = errors.New("acquire: upstream failure")
)

// Error is an error whose message is a plain-language sentence for the user.
// It matches each of its kinds.
type Error struct {
	Kinds []error
	Msg   string
	Err   error // cause, may be nil; its text is never shown to the user
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// UserMessage returns the plain-language message.
func (e *Error) UserMessage() string { return e.Msg }

// Is reports whether target is one of the kinds.
func (e *Error) Is(target error) bool {
	for _, k := range e.Kinds {
		if k == target {
			return true
		}
	}
	return false
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Err }

func newError(kind, cause error, format string, args ...any) *Error {
	return &Error{Kinds: []error{kind}, Msg: fmt.Sprintf(format, args...), Err: cause}
}

func invalid(format string, args ...any) error { return newError(ErrInvalid, nil, format, args...) }

func notFound(format string, args ...any) error {
	return newError(store.ErrNotFound, nil, format, args...)
}

func conflict(format string, args ...any) error {
	return &Error{Kinds: []error{ErrInvalid, store.ErrConflict}, Msg: fmt.Sprintf(format, args...)}
}

// RefusedError is returned when a rejected release is grabbed without override.
type RefusedError struct {
	Title      string
	Rejections []decision.Rejection
}

func (e *RefusedError) Error() string { return e.UserMessage() }

// UserMessage lists the reasons.
func (e *RefusedError) UserMessage() string {
	msg := "This release is rejected"
	for i, r := range e.Rejections {
		if i == 0 {
			msg += ": "
		} else {
			msg += "; "
		}
		msg += r.Message
	}
	return msg + ". Grab it anyway with an explicit override."
}

// Is matches ErrRefused.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// mapNotFound turns store.ErrNotFound into a not-found error with a message.
func mapNotFound(err error, format string, args ...any) error {
	if errors.Is(store.MapError(err), store.ErrNotFound) {
		return notFound(format, args...)
	}
	return store.MapError(err)
}

// unusableProfile reports decision settings the engine refuses (an invalid
// quality profile or scoring rule).
func unusableProfile(err error) error {
	return newError(ErrInvalid, err, "The quality profile of this movie cannot be used: %s. Fix it in Settings > Quality.", err.Error())
}
