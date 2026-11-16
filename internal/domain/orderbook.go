package domain

import (
	"container/list"
	"sort"
	"time"
)

// BookDepth is the number of price levels kept in published snapshots.
const BookDepth = 20

// PriceLevel aggregates the resting orders at one price.
type PriceLevel struct {
	Price    int64 `json:"price"`
	Quantity int64 `json:"quantity"`
	Orders   int   `json:"orders"`
}

// BookSnapshot is an immutable view of the best levels of an order book.
type BookSnapshot struct {
	Symbol    string       `json:"symbol"`
	Bids      []PriceLevel `json:"bids"`
	Asks      []PriceLevel `json:"asks"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// bookSide holds one side of the book. prices is sorted best-first, and each
// price level is a FIFO queue (time priority).
type bookSide struct {
	isBid  bool
	prices []int64
	levels map[int64]*list.List
}

func newBookSide(isBid bool) *bookSide {
	return &bookSide{isBid: isBid, levels: make(map[int64]*list.List)}
}

// better reports whether price a has strictly higher priority than b.
func (s *bookSide) better(a, b int64) bool {
	if s.isBid {
		return a > b
	}
	return a < b
}

// index returns the position of the first price that is not better than p.
func (s *bookSide) index(p int64) int {
	return sort.Search(len(s.prices), func(i int) bool { return !s.better(s.prices[i], p) })
}

func (s *bookSide) bestLevel() (*list.List, bool) {
	if len(s.prices) == 0 {
		return nil, false
	}
	return s.levels[s.prices[0]], true
}

func (s *bookSide) add(o *Order) *list.Element {
	lvl, ok := s.levels[o.Price]
	if !ok {
		lvl = list.New()
		s.levels[o.Price] = lvl
		i := s.index(o.Price)
		s.prices = append(s.prices, 0)
		copy(s.prices[i+1:], s.prices[i:])
		s.prices[i] = o.Price
	}
	return lvl.PushBack(o)
}

func (s *bookSide) remove(e *list.Element, o *Order) {
	lvl := s.levels[o.Price]
	lvl.Remove(e)
	if lvl.Len() == 0 {
		delete(s.levels, o.Price)
		i := s.index(o.Price)
		s.prices = append(s.prices[:i], s.prices[i+1:]...)
	}
}

func (s *bookSide) snapshot(depth int) []PriceLevel {
	n := min(depth, len(s.prices))
	out := make([]PriceLevel, 0, n)
	for _, p := range s.prices[:n] {
		lvl := s.levels[p]
		pl := PriceLevel{Price: p, Orders: lvl.Len()}
		for e := lvl.Front(); e != nil; e = e.Next() {
			pl.Quantity += e.Value.(*Order).Remaining()
		}
		out = append(out, pl)
	}
	return out
}

// OrderBook is a price-time priority limit order book for one symbol.
//
// It is NOT safe for concurrent use: it is owned by exactly one goroutine (the
// symbol worker in the engine package), which is what makes matching
// race-free without locks.
type OrderBook struct {
	symbol  string
	bids    *bookSide
	asks    *bookSide
	resting map[string]*list.Element // order ID -> element in its price level
}

// NewOrderBook creates an empty book.
func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol:  symbol,
		bids:    newBookSide(true),
		asks:    newBookSide(false),
		resting: make(map[string]*list.Element),
	}
}

func (b *OrderBook) sides(taker Side) (own, opposite *bookSide) {
	if taker == SideBuy {
		return b.bids, b.asks
	}
	return b.asks, b.bids
}

// Match matches taker against the opposite side, mutating taker's fill/status
// and resting any unfilled remainder in the book. It returns the generated
// trades (executed at the resting order's price) and copies of the resting
// orders that were changed.
func (b *OrderBook) Match(taker *Order, now time.Time, newTradeID func() string) (trades []Trade, updated []Order) {
	own, opp := b.sides(taker.Side)

	for taker.Remaining() > 0 {
		lvl, ok := opp.bestLevel()
		if !ok {
			break
		}
		front := lvl.Front()
		maker := front.Value.(*Order)
		if (taker.Side == SideBuy && maker.Price > taker.Price) ||
			(taker.Side == SideSell && maker.Price < taker.Price) {
			break
		}

		qty := min(taker.Remaining(), maker.Remaining())
		taker.FilledQty += qty
		maker.FilledQty += qty
		maker.UpdatedAt = now

		tr := Trade{
			ID: newTradeID(), Symbol: b.symbol, Price: maker.Price, Quantity: qty,
			TakerSide: taker.Side, Timestamp: now,
		}
		if taker.Side == SideBuy {
			tr.BuyOrderID, tr.SellOrderID = taker.ID, maker.ID
		} else {
			tr.BuyOrderID, tr.SellOrderID = maker.ID, taker.ID
		}
		trades = append(trades, tr)

		if maker.Remaining() == 0 {
			maker.Status = StatusFilled
			opp.remove(front, maker)
			delete(b.resting, maker.ID)
		} else {
			maker.Status = StatusPartial
		}
		updated = append(updated, *maker)
	}

	taker.UpdatedAt = now
	switch {
	case taker.Remaining() == 0:
		taker.Status = StatusFilled
	case taker.FilledQty > 0:
		taker.Status = StatusPartial
	default:
		taker.Status = StatusOpen
	}
	if taker.Remaining() > 0 {
		cp := *taker
		b.resting[taker.ID] = own.add(&cp)
	}
	return trades, updated
}

// LoadResting inserts an existing OPEN or PARTIAL order into the order book without matching.
// Used for rehydration during server boot.
func (b *OrderBook) LoadResting(o Order) {
	if o.Remaining() <= 0 {
		return
	}
	own, _ := b.sides(o.Side)
	cp := o
	b.resting[o.ID] = own.add(&cp)
}

// Cancel removes a resting order. It returns false if the order is not in the
// book (unknown, already filled or already cancelled).
func (b *OrderBook) Cancel(id string, now time.Time) (Order, bool) {
	e, ok := b.resting[id]
	if !ok {
		return Order{}, false
	}
	o := e.Value.(*Order)
	own, _ := b.sides(o.Side)
	own.remove(e, o)
	delete(b.resting, id)
	o.Status = StatusCancelled
	o.UpdatedAt = now
	return *o, true
}

// Snapshot returns the best depth levels of both sides.
func (b *OrderBook) Snapshot(depth int, now time.Time) BookSnapshot {
	return BookSnapshot{
		Symbol: b.symbol, Bids: b.bids.snapshot(depth), Asks: b.asks.snapshot(depth), UpdatedAt: now,
	}
}
