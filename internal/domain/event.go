package domain

import "time"

// Realtime channel names.
const (
	ChannelTicker      = "market.ticker"
	ChannelTrade       = "market.trade"
	ChannelOrderBook   = "market.orderbook"
	ChannelOrderUpdate = "order.update"
)

// Event is a realtime notification. Data must be an immutable snapshot because
// it is shared with many consumers.
type Event struct {
	Channel string
	Symbol  string
	UserID  string // only set for ChannelOrderUpdate
	Data    any
	Time    time.Time
}

// Outcome is the result of one engine command. The symbol worker hands it to a
// sink that persists it and publishes the resulting events, before replying to
// the caller, so state is visible as soon as the command returns.
type Outcome struct {
	Symbol  string
	Order   Order   // the order the command was about (taker, or the cancelled one)
	Updated []Order // resting (maker) orders changed by matching
	Trades  []Trade
	Book    BookSnapshot
}
