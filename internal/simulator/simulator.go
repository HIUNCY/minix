// Package simulator generates a realistic stream of market activity. Per
// symbol it keeps a mean-reverting random-walk reference price and a "market
// maker" bot that submits limit orders around it through the normal order
// service, so every price movement comes from real matches in the engine.
package simulator

import (
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/usecase"
)

// BotUser is the user id of simulated orders.
const BotUser = "bot"

// OrderCreator is the subset of the order service used by the simulator.
type OrderCreator interface {
	Create(ctx context.Context, in usecase.CreateOrderInput) (domain.Order, error)
}

// Config tunes the simulation.
type Config struct {
	Interval   time.Duration // time between rounds (each round: 1 buy + 1 sell per symbol)
	Volatility float64       // std-dev of a price step, in ticks
	Reversion  float64       // 0..1 pull towards the reference price per round
	Seed       uint64
}

// Simulator is the price/order generator.
type Simulator struct {
	svc  OrderCreator
	ins  []domain.Instrument
	cfg  Config
	log  *slog.Logger
	rng  *rand.Rand
	refs []float64
}

func New(svc OrderCreator, ins []domain.Instrument, cfg Config, log *slog.Logger) *Simulator {
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.Volatility <= 0 {
		cfg.Volatility = 1
	}
	if cfg.Reversion <= 0 {
		cfg.Reversion = 0.05
	}
	refs := make([]float64, len(ins))
	for i, in := range ins {
		refs[i] = float64(in.RefPrice)
	}
	return &Simulator{
		svc: svc, ins: ins, cfg: cfg, log: log, refs: refs,
		rng: rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)),
	}
}

// Run generates orders until ctx is cancelled.
func (s *Simulator) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for i := range s.ins {
				s.step(ctx, i)
			}
		}
	}
}

func (s *Simulator) step(ctx context.Context, i int) {
	in := s.ins[i]
	tick := float64(in.Tick)

	// Mean-reverting random walk.
	s.refs[i] += s.cfg.Reversion*(float64(in.RefPrice)-s.refs[i]) + s.rng.NormFloat64()*s.cfg.Volatility*tick
	s.refs[i] = math.Max(s.refs[i], tick)
	mid := int64(math.Round(s.refs[i]/tick)) * in.Tick

	// Offsets in [-1, 2] ticks: negative/zero offsets cross the spread and trade.
	buy := mid - int64(s.rng.IntN(4)-1)*in.Tick
	sell := mid + int64(s.rng.IntN(4)-1)*in.Tick
	qty := func() int64 { return int64(1+s.rng.IntN(10)) * 10 }

	s.place(ctx, in.Code, "BUY", max(buy, in.Tick), qty())
	s.place(ctx, in.Code, "SELL", max(sell, in.Tick), qty())
}

func (s *Simulator) place(ctx context.Context, symbol, side string, price, qty int64) {
	_, err := s.svc.Create(ctx, usecase.CreateOrderInput{
		UserID: BotUser, Symbol: symbol, Side: side, Price: price, Quantity: qty,
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("simulator order rejected", "symbol", symbol, "err", err)
	}
}
