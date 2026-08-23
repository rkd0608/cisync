// Package domain defines sentinel errors shared across ingest components.
package domain

import "errors"

var (
	// ErrDuplicateDelivery indicates the (source, ext_delivery_id) pair already exists.
	ErrDuplicateDelivery = errors.New("duplicate delivery")
	// ErrNotFound indicates no delivery row matches the lookup.
	ErrNotFound = errors.New("delivery not found")
)
