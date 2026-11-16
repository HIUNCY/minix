// Package domain contains the pure business entities and rules of the mini
// exchange. It has no dependency on transport, storage or concurrency.
package domain

import "errors"

// Sentinel errors. Transport layers map them to protocol-specific codes.
var (
	ErrInvalidOrder  = errors.New("invalid order")
	ErrUnknownSymbol = errors.New("unknown symbol")
	ErrOrderNotFound = errors.New("order not found")
	ErrForbidden     = errors.New("forbidden")
	ErrNotCancelable = errors.New("order is not cancelable")
	ErrOverloaded    = errors.New("system overloaded, retry later")
	ErrShuttingDown  = errors.New("system shutting down")
)
