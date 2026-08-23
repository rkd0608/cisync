// Package domain defines sentinel errors shared across fleet components.
package domain

import "errors"

var (
	// ErrNotFound indicates no execution job matches the identifier.
	ErrNotFound = errors.New("job not found")
	// ErrFenceMismatch indicates a stale fence token was presented (I-11).
	ErrFenceMismatch = errors.New("fence token mismatch")
	// ErrAlreadyAccepted indicates a result was already accepted for this run
	// and attempt (at-most-once acceptance).
	ErrAlreadyAccepted = errors.New("result already accepted")
)
