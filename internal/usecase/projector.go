package usecase

import (
	"time"

	"github.com/hiuncy/minix/internal/domain"
)

// Projector applies engine outcomes: it persists orders/trades/ticker/book and
// publishes realtime events. It implements engine.Sink and runs on the symbol
// worker goroutine, so every operation here is non-blocking.
type Projector struct {
	orders  OrderRepository
	trades  TradeRepository
	tickers TickerRepository
	books   BookRepository
	pub     EventPublisher
}

func NewProjector(o OrderRepository, t TradeRepository, tk TickerRepository, b BookRepository, p EventPublisher) *Projector {
	return &Projector{orders: o, trades: t, tickers: tk, books: b, pub: p}
}

// Apply stores the outcome and emits events in a deterministic order:
// order updates, trades, ticker, order book.
func (p *Projector) Apply(out domain.Outcome) {
	now := time.Now()

	p.orders.Save(out.Order)
	p.pub.Publish(domain.Event{Channel: domain.ChannelOrderUpdate, Symbol: out.Symbol, UserID: out.Order.UserID, Data: out.Order, Time: now})
	for _, u := range out.Updated {
		p.orders.Save(u)
		p.pub.Publish(domain.Event{Channel: domain.ChannelOrderUpdate, Symbol: out.Symbol, UserID: u.UserID, Data: u, Time: now})
	}

	for _, tr := range out.Trades {
		p.trades.Add(tr)
		p.tickers.ApplyTrade(tr)
		p.pub.Publish(domain.Event{Channel: domain.ChannelTrade, Symbol: out.Symbol, Data: tr, Time: tr.Timestamp})
	}
	if len(out.Trades) > 0 {
		if tk, ok := p.tickers.Get(out.Symbol); ok {
			p.pub.Publish(domain.Event{Channel: domain.ChannelTicker, Symbol: out.Symbol, Data: tk, Time: now})
		}
	}

	p.books.Set(out.Book)
	p.pub.Publish(domain.Event{Channel: domain.ChannelOrderBook, Symbol: out.Symbol, Data: out.Book, Time: now})
}
