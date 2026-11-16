package domain

import (
	"fmt"
	"time"
)

// Side is the direction of an order.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// Valid reports whether s is a known side.
func (s Side) Valid() bool { return s == SideBuy || s == SideSell }

// Status is the lifecycle state of an order.
type Status string

const (
	StatusOpen      Status = "OPEN"
	StatusPartial   Status = "PARTIAL"
	StatusFilled    Status = "FILLED"
	StatusCancelled Status = "CANCELLED"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusPartial, StatusFilled, StatusCancelled:
		return true
	}
	return false
}

// Limits protecting the engine from absurd input.
const (
	MaxPrice    int64 = 1_000_000_000_000
	MaxQuantity int64 = 1_000_000_000
)

// Order is a LIMIT order. Price is an integer in the smallest currency unit
// (no floating point arithmetic anywhere in the engine).
type Order struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Symbol    string    `json:"symbol"`
	Side      Side      `json:"side"`
	Price     int64     `json:"price"`
	Quantity  int64     `json:"quantity"`
	FilledQty int64     `json:"filled_quantity"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Remaining is the quantity still unfilled.
func (o Order) Remaining() int64 { return o.Quantity - o.FilledQty }

// Validate checks the static invariants of a new order.
func (o Order) Validate() error {
	switch {
	case o.Symbol == "":
		return fmt.Errorf("%w: symbol is required", ErrInvalidOrder)
	case !o.Side.Valid():
		return fmt.Errorf("%w: side must be BUY or SELL", ErrInvalidOrder)
	case o.Price <= 0 || o.Price > MaxPrice:
		return fmt.Errorf("%w: price must be between 1 and %d", ErrInvalidOrder, MaxPrice)
	case o.Quantity <= 0 || o.Quantity > MaxQuantity:
		return fmt.Errorf("%w: quantity must be between 1 and %d", ErrInvalidOrder, MaxQuantity)
	}
	return nil
}
