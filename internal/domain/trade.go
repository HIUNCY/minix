package domain

import (
	"math"
	"time"
)

// Trade is an executed match between a BUY and a SELL order.
type Trade struct {
	ID          string    `json:"id"`
	Symbol      string    `json:"symbol"`
	Price       int64     `json:"price"`
	Quantity    int64     `json:"quantity"`
	BuyOrderID  string    `json:"buy_order_id"`
	SellOrderID string    `json:"sell_order_id"`
	TakerSide   Side      `json:"taker_side"`
	Timestamp   time.Time `json:"timestamp"`
}

// Ticker is the rolling market summary of a symbol.
type Ticker struct {
	Symbol    string    `json:"symbol"`
	Last      int64     `json:"last"`
	Open      int64     `json:"open"`
	High      int64     `json:"high"`
	Low       int64     `json:"low"`
	Change    int64     `json:"change"`
	ChangePct float64   `json:"change_pct"`
	Volume    int64     `json:"volume"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewTicker returns a ticker whose open/last price is the reference price.
func NewTicker(symbol string, open int64) Ticker {
	return Ticker{Symbol: symbol, Last: open, Open: open, High: open, Low: open, UpdatedAt: time.Now()}
}

// ApplyTrade updates the ticker with an executed trade.
func (t *Ticker) ApplyTrade(tr Trade) {
	t.Last = tr.Price
	if tr.Price > t.High {
		t.High = tr.Price
	}
	if tr.Price < t.Low {
		t.Low = tr.Price
	}
	t.Volume += tr.Quantity
	t.Change = t.Last - t.Open
	if t.Open != 0 {
		t.ChangePct = math.Round(float64(t.Change)/float64(t.Open)*10000) / 100
	}
	t.UpdatedAt = tr.Timestamp
}

// Instrument is a tradable symbol with its reference price and tick size
// (the tick size is only used by the price simulator).
type Instrument struct {
	Code     string
	RefPrice int64
	Tick     int64
}

// DefaultInstruments returns the symbols served by the exchange.
func DefaultInstruments() []Instrument {
	return []Instrument{
		{Code: "BBCA", RefPrice: 9000, Tick: 25},
		{Code: "BBRI", RefPrice: 4500, Tick: 10},
		{Code: "TLKM", RefPrice: 3200, Tick: 10},
		{Code: "ASII", RefPrice: 5000, Tick: 25},
		{Code: "GOTO", RefPrice: 70, Tick: 1},
	}
}
