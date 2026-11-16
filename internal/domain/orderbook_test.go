package domain

import (
	"fmt"
	"testing"
	"time"
)

func newOrder(id string, side Side, price, qty int64) *Order {
	return &Order{ID: id, Symbol: "TEST", Side: side, Price: price, Quantity: qty, Status: StatusOpen}
}

func tradeIDs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("T%d", n) }
}

func TestMatch_NoCrossRestsInBook(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	id := tradeIDs()

	b.Match(newOrder("s1", SideSell, 105, 10), now, id)
	trades, _ := b.Match(newOrder("b1", SideBuy, 100, 10), now, id)
	if len(trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(trades))
	}
	snap := b.Snapshot(5, now)
	if len(snap.Bids) != 1 || len(snap.Asks) != 1 || snap.Bids[0].Price != 100 || snap.Asks[0].Price != 105 {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
}

func TestMatch_FullFillAtMakerPrice(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	id := tradeIDs()

	b.Match(newOrder("s1", SideSell, 100, 10), now, id)
	taker := newOrder("b1", SideBuy, 110, 10)
	trades, updated := b.Match(taker, now, id)

	if len(trades) != 1 || trades[0].Price != 100 || trades[0].Quantity != 10 {
		t.Fatalf("unexpected trades %+v", trades)
	}
	if taker.Status != StatusFilled {
		t.Fatalf("taker status = %s", taker.Status)
	}
	if len(updated) != 1 || updated[0].Status != StatusFilled {
		t.Fatalf("unexpected updated %+v", updated)
	}
	if s := b.Snapshot(5, now); len(s.Asks) != 0 || len(s.Bids) != 0 {
		t.Fatalf("book should be empty: %+v", s)
	}
}

func TestMatch_PartialFillAndRemainderRests(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	id := tradeIDs()

	b.Match(newOrder("s1", SideSell, 100, 4), now, id)
	taker := newOrder("b1", SideBuy, 100, 10)
	trades, _ := b.Match(taker, now, id)

	if len(trades) != 1 || trades[0].Quantity != 4 {
		t.Fatalf("unexpected trades %+v", trades)
	}
	if taker.Status != StatusPartial || taker.FilledQty != 4 {
		t.Fatalf("taker = %+v", taker)
	}
	s := b.Snapshot(5, now)
	if len(s.Bids) != 1 || s.Bids[0].Quantity != 6 {
		t.Fatalf("remainder should rest with qty 6: %+v", s)
	}
}

func TestMatch_PriceThenTimePriority(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	id := tradeIDs()

	b.Match(newOrder("s-late-cheap", SideSell, 99, 5), now, id)
	b.Match(newOrder("s-first", SideSell, 100, 5), now, id)
	b.Match(newOrder("s-second", SideSell, 100, 5), now, id)

	trades, _ := b.Match(newOrder("b1", SideBuy, 100, 12), now, id)
	if len(trades) != 3 {
		t.Fatalf("expected 3 trades, got %+v", trades)
	}
	want := []string{"s-late-cheap", "s-first", "s-second"}
	for i, w := range want {
		if trades[i].SellOrderID != w {
			t.Errorf("trade %d matched %s, want %s", i, trades[i].SellOrderID, w)
		}
	}
	if trades[2].Quantity != 2 {
		t.Errorf("last fill qty = %d, want 2", trades[2].Quantity)
	}
}

func TestMatch_SellTakerHitsBestBidFirst(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	id := tradeIDs()

	b.Match(newOrder("b-low", SideBuy, 98, 5), now, id)
	b.Match(newOrder("b-high", SideBuy, 100, 5), now, id)

	trades, _ := b.Match(newOrder("s1", SideSell, 98, 7), now, id)
	if len(trades) != 2 || trades[0].BuyOrderID != "b-high" || trades[0].Price != 100 || trades[1].Price != 98 {
		t.Fatalf("unexpected trades %+v", trades)
	}
}

func TestCancel(t *testing.T) {
	b := NewOrderBook("TEST")
	now := time.Now()
	b.Match(newOrder("b1", SideBuy, 100, 5), now, tradeIDs())

	o, ok := b.Cancel("b1", now)
	if !ok || o.Status != StatusCancelled {
		t.Fatalf("cancel failed: %+v %v", o, ok)
	}
	if _, ok := b.Cancel("b1", now); ok {
		t.Fatal("second cancel must fail")
	}
	if s := b.Snapshot(5, now); len(s.Bids) != 0 {
		t.Fatalf("book should be empty: %+v", s)
	}
}

func TestOrderValidate(t *testing.T) {
	cases := map[string]Order{
		"no symbol":  {Side: SideBuy, Price: 1, Quantity: 1},
		"bad side":   {Symbol: "A", Side: "X", Price: 1, Quantity: 1},
		"zero price": {Symbol: "A", Side: SideBuy, Price: 0, Quantity: 1},
		"neg qty":    {Symbol: "A", Side: SideBuy, Price: 1, Quantity: -1},
		"huge qty":   {Symbol: "A", Side: SideBuy, Price: 1, Quantity: MaxQuantity + 1},
		"huge price": {Symbol: "A", Side: SideSell, Price: MaxPrice + 1, Quantity: 1},
	}
	for name, o := range cases {
		if err := o.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	ok := Order{Symbol: "A", Side: SideBuy, Price: 1, Quantity: 1}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid order rejected: %v", err)
	}
}
