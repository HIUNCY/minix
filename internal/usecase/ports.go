package usecase

import (
	"context"

	"github.com/hiuncy/minix/internal/domain"
)

// OrderFilter selects orders in OrderRepository.List.
type OrderFilter struct {
	UserID string
	Symbol string
	Status domain.Status
	Limit  int
	Offset int
}

// Repositories and collaborators are defined by the consumer (this package)
// and implemented by the infrastructure layer.

type OrderRepository interface {
	Save(domain.Order)
	Get(id string) (domain.Order, error)
	List(OrderFilter) []domain.Order
}

type TradeRepository interface {
	Add(domain.Trade)
	List(symbol string, limit int) []domain.Trade
}

type TickerRepository interface {
	ApplyTrade(domain.Trade)
	Get(symbol string) (domain.Ticker, bool)
	All() []domain.Ticker
}

type BookRepository interface {
	Set(domain.BookSnapshot)
	Get(symbol string) (domain.BookSnapshot, bool)
}

// EventPublisher must never block the caller.
type EventPublisher interface {
	Publish(domain.Event)
}

// Matcher is the matching engine port.
type Matcher interface {
	Submit(ctx context.Context, o domain.Order) (domain.Order, error)
	Cancel(ctx context.Context, symbol, id string) (domain.Order, error)
}
