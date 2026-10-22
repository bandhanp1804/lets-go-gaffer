package fpl

import "fmt"

// StatusError is returned when the FPL API answers with an unexpected HTTP status.
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.StatusCode)
}

// RetryError is returned by the retry transport once it has given up.
// Err is the failure of the final attempt: a *StatusError or a transport error.
type RetryError struct {
	Attempts int
	Err      error
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("giving up after %d attempts: %v", e.Attempts, e.Err)
}

func (e *RetryError) Unwrap() error { return e.Err }
