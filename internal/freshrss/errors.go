package freshrss

import (
	"errors"
	"fmt"
)

// Sentinel errors, usable with errors.Is.
var (
	ErrUnauthorized    = errors.New("freshRSS rejected the credentials")
	ErrNotFound        = errors.New("freshRSS resource not found")
	ErrBadRequest      = errors.New("freshRSS rejected the request")
	ErrUpstream        = errors.New("freshRSS returned a server error")
	ErrTimeout         = errors.New("freshRSS did not answer in time")
	ErrTransport       = errors.New("cannot reach freshRSS")
	ErrInvalidResponse = errors.New("freshRSS returned an invalid response")
	ErrTooLarge        = errors.New("freshRSS response exceeded the configured limit")
)

// APIError is returned for every failed upstream interaction. It never
// carries credentials or authentication headers.
type APIError struct {
	Op      string
	Status  int
	Kind    error
	Message string
}

func (e *APIError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("freshrss: %s: %s (http %d)", e.Op, e.Message, e.Status)
	}
	return fmt.Sprintf("freshrss: %s: %s", e.Op, e.Message)
}

func (e *APIError) Unwrap() error { return e.Kind }

// Kind returns a stable, machine readable error category for the UI.
func Kind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrBadRequest):
		return "bad_request"
	case errors.Is(err, ErrTimeout):
		return "timeout"
	case errors.Is(err, ErrTransport):
		return "unreachable"
	case errors.Is(err, ErrInvalidResponse):
		return "invalid_response"
	case errors.Is(err, ErrTooLarge):
		return "response_too_large"
	case errors.Is(err, ErrUpstream):
		return "upstream_error"
	default:
		return "error"
	}
}

func apiErr(op string, status int, kind error, msg string) *APIError {
	return &APIError{Op: op, Status: status, Kind: kind, Message: msg}
}
