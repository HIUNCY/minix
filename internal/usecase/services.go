package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hiuncy/minix/internal/domain"
)

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

func clampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return min(n, MaxLimit)
}

func symbolSet(ins []domain.Instrument) map[string]struct{} {
	m := make(map[string]struct{}, len(ins))
	for _, i := range ins {
		m[i.Code] = struct{}{}
	}
	return m
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// CreateOrderInput is the user supplied data of a new order.
type CreateOrderInput struct {
	UserID   string
	Symbol   string
	Side     string
	Price    int64
	Quantity int64
}

// OrderService implements the order use cases.
type OrderService struct {
	matcher Matcher
	orders  OrderRepository
	symbols map[string]struct{}
	timeout time.Duration
}

func NewOrderService(m Matcher, r OrderRepository, ins []domain.Instrument) *OrderService {
	return &OrderService{matcher: m, orders: r, symbols: symbolSet(ins), timeout: 2 * time.Second}
}

// Create validates and submits a new LIMIT order.
func (s *OrderService) Create(ctx context.Context, in CreateOrderInput) (domain.Order, error) {
	symbol := strings.ToUpper(strings.TrimSpace(in.Symbol))
	if _, ok := s.symbols[symbol]; !ok {
		return domain.Order{}, fmt.Errorf("%w: unknown symbol %q", domain.ErrInvalidOrder, in.Symbol)
	}
	now := time.Now()
	o := domain.Order{
		ID:        newID("ORD-"),
		UserID:    in.UserID,
		Symbol:    symbol,
		Side:      domain.Side(strings.ToUpper(strings.TrimSpace(in.Side))),
		Price:     in.Price,
		Quantity:  in.Quantity,
		Status:    domain.StatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := o.Validate(); err != nil {
		return domain.Order{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.matcher.Submit(ctx, o)
}

// Get returns one order.
func (s *OrderService) Get(id string) (domain.Order, error) { return s.orders.Get(id) }

// List returns orders newest first.
func (s *OrderService) List(f OrderFilter) []domain.Order {
	f.Symbol = strings.ToUpper(f.Symbol)
	f.Limit = clampLimit(f.Limit)
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.orders.List(f)
}

// Cancel cancels an order owned by userID.
func (s *OrderService) Cancel(ctx context.Context, userID, id string) (domain.Order, error) {
	o, err := s.orders.Get(id)
	if err != nil {
		return domain.Order{}, err
	}
	if o.UserID != userID {
		return domain.Order{}, domain.ErrForbidden
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.matcher.Cancel(ctx, o.Symbol, id)
}

// MarketService implements the read-only market data use cases.
type MarketService struct {
	tickers TickerRepository
	books   BookRepository
	trades  TradeRepository
	symbols map[string]struct{}
}

func NewMarketService(tk TickerRepository, b BookRepository, tr TradeRepository, ins []domain.Instrument) *MarketService {
	return &MarketService{tickers: tk, books: b, trades: tr, symbols: symbolSet(ins)}
}

func (s *MarketService) check(symbol string) (string, error) {
	symbol = strings.ToUpper(symbol)
	if _, ok := s.symbols[symbol]; !ok {
		return "", domain.ErrUnknownSymbol
	}
	return symbol, nil
}

func (s *MarketService) Tickers() []domain.Ticker { return s.tickers.All() }

func (s *MarketService) Ticker(symbol string) (domain.Ticker, error) {
	symbol, err := s.check(symbol)
	if err != nil {
		return domain.Ticker{}, err
	}
	tk, _ := s.tickers.Get(symbol)
	return tk, nil
}

// OrderBook returns the best depth levels of both sides.
func (s *MarketService) OrderBook(symbol string, depth int) (domain.BookSnapshot, error) {
	symbol, err := s.check(symbol)
	if err != nil {
		return domain.BookSnapshot{}, err
	}
	snap, ok := s.books.Get(symbol)
	if !ok {
		return domain.BookSnapshot{Symbol: symbol, Bids: []domain.PriceLevel{}, Asks: []domain.PriceLevel{}, UpdatedAt: time.Now()}, nil
	}
	if depth <= 0 {
		depth = 10
	}
	if depth < len(snap.Bids) {
		snap.Bids = snap.Bids[:depth]
	}
	if depth < len(snap.Asks) {
		snap.Asks = snap.Asks[:depth]
	}
	return snap, nil
}

// Trades returns the latest trades, newest first. An empty symbol means all.
func (s *MarketService) Trades(symbol string, limit int) ([]domain.Trade, error) {
	if symbol != "" {
		var err error
		if symbol, err = s.check(symbol); err != nil {
			return nil, err
		}
	}
	return s.trades.List(symbol, clampLimit(limit)), nil
}
