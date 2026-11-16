// Package memory contains the in-memory storage adapters. Each type is safe
// for concurrent use; they sit behind the interfaces of the usecase package so
// they can be replaced by Redis or a database.
package memory

import (
	"sync"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/usecase"
)

// OrderRepo stores orders in insertion order.
type OrderRepo struct {
	mu    sync.RWMutex
	byID  map[string]domain.Order
	order []string
}

func NewOrderRepo() *OrderRepo { return &OrderRepo{byID: make(map[string]domain.Order)} }

func (r *OrderRepo) Save(o domain.Order) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[o.ID]; !ok {
		r.order = append(r.order, o.ID)
	}
	r.byID[o.ID] = o
}

func (r *OrderRepo) Get(id string) (domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.byID[id]
	if !ok {
		return domain.Order{}, domain.ErrOrderNotFound
	}
	return o, nil
}

// List returns orders newest first, applying filter, offset and limit.
func (r *OrderRepo) List(f usecase.OrderFilter) []domain.Order {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Order, 0, min(f.Limit, 64))
	skipped := 0
	for i := len(r.order) - 1; i >= 0 && len(out) < f.Limit; i-- {
		o := r.byID[r.order[i]]
		if (f.UserID != "" && o.UserID != f.UserID) ||
			(f.Symbol != "" && o.Symbol != f.Symbol) ||
			(f.Status != "" && o.Status != f.Status) {
			continue
		}
		if skipped < f.Offset {
			skipped++
			continue
		}
		out = append(out, o)
	}
	return out
}

// TradeRepo stores trades in execution order.
type TradeRepo struct {
	mu     sync.RWMutex
	trades []domain.Trade
}

func NewTradeRepo() *TradeRepo { return &TradeRepo{} }

func (r *TradeRepo) Add(t domain.Trade) {
	r.mu.Lock()
	r.trades = append(r.trades, t)
	r.mu.Unlock()
}

// List returns up to limit trades newest first; empty symbol means all.
func (r *TradeRepo) List(symbol string, limit int) []domain.Trade {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Trade, 0, min(limit, 64))
	for i := len(r.trades) - 1; i >= 0 && len(out) < limit; i-- {
		if symbol == "" || r.trades[i].Symbol == symbol {
			out = append(out, r.trades[i])
		}
	}
	return out
}

// MarketRepo stores tickers and the latest order book snapshots.
type MarketRepo struct {
	mu      sync.RWMutex
	tickers map[string]domain.Ticker
	codes   []string
	books   map[string]domain.BookSnapshot
}

func NewMarketRepo(ins []domain.Instrument) *MarketRepo {
	m := &MarketRepo{tickers: make(map[string]domain.Ticker), books: make(map[string]domain.BookSnapshot)}
	for _, in := range ins {
		m.tickers[in.Code] = domain.NewTicker(in.Code, in.RefPrice)
		m.codes = append(m.codes, in.Code)
	}
	return m
}

func (m *MarketRepo) ApplyTrade(t domain.Trade) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tk := m.tickers[t.Symbol]
	tk.ApplyTrade(t)
	m.tickers[t.Symbol] = tk
}

func (m *MarketRepo) Get(symbol string) (domain.Ticker, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tk, ok := m.tickers[symbol]
	return tk, ok
}

func (m *MarketRepo) All() []domain.Ticker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.Ticker, 0, len(m.codes))
	for _, c := range m.codes {
		out = append(out, m.tickers[c])
	}
	return out
}

// Books returns a BookRepository view backed by the same lock.
func (m *MarketRepo) Books() usecase.BookRepository { return bookView{m} }

type bookView struct{ m *MarketRepo }

func (b bookView) Set(s domain.BookSnapshot) {
	b.m.mu.Lock()
	b.m.books[s.Symbol] = s
	b.m.mu.Unlock()
}

func (b bookView) Get(symbol string) (domain.BookSnapshot, bool) {
	b.m.mu.RLock()
	defer b.m.mu.RUnlock()
	s, ok := b.m.books[symbol]
	return s, ok
}
