// Package engine runs the matching engine. Each symbol is owned by a single
// worker goroutine that serialises all commands for that symbol through a
// channel ("single writer"). This removes the need for locks around the order
// book and guarantees deterministic, race-free matching, while different
// symbols are processed in parallel.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hiuncy/minix/internal/domain"
)

// Sink receives the outcome of every command. It is called from the worker
// goroutine before the caller is answered, so implementations must be fast and
// must never block (storage writes and non-blocking publishes only).
type Sink interface {
	Apply(domain.Outcome)
}

type command struct {
	place    *domain.Order
	restore  *domain.Order
	cancelID string
	reply    chan reply
}

type reply struct {
	order domain.Order
	err   error
}

type worker struct {
	symbol  string
	book    *domain.OrderBook
	cmds    chan command
	sink    Sink
	log     *slog.Logger
	tradeID uint64
}

// Engine routes commands to per-symbol workers.
type Engine struct {
	ctx     context.Context
	workers map[string]*worker
	wg      sync.WaitGroup
}

// New starts one worker per instrument. Workers stop when ctx is cancelled.
// queueSize is the per-symbol command buffer used for backpressure.
func New(ctx context.Context, instruments []domain.Instrument, sink Sink, queueSize int, log *slog.Logger) *Engine {
	e := &Engine{ctx: ctx, workers: make(map[string]*worker, len(instruments))}
	for _, in := range instruments {
		w := &worker{
			symbol: in.Code,
			book:   domain.NewOrderBook(in.Code),
			cmds:   make(chan command, queueSize),
			sink:   sink,
			log:    log.With("symbol", in.Code),
		}
		e.workers[in.Code] = w
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			w.run(ctx)
		}()
	}
	return e
}

// Wait blocks until all workers exited (after the context was cancelled).
func (e *Engine) Wait() { e.wg.Wait() }

// Submit places a validated order and returns it in its post-match state.
//
// If ctx expires after the command was queued, the order may still be
// executed; callers can observe it through the order list.
func (e *Engine) Submit(ctx context.Context, o domain.Order) (domain.Order, error) {
	w, ok := e.workers[o.Symbol]
	if !ok {
		return domain.Order{}, domain.ErrUnknownSymbol
	}
	return e.do(ctx, w, command{place: &o})
}

// Restore adds an existing resting order back into the order book without matching.
func (e *Engine) Restore(ctx context.Context, o domain.Order) error {
	w, ok := e.workers[o.Symbol]
	if !ok {
		return domain.ErrUnknownSymbol
	}
	_, err := e.do(ctx, w, command{restore: &o})
	return err
}

// Cancel cancels a resting order of the given symbol.
func (e *Engine) Cancel(ctx context.Context, symbol, id string) (domain.Order, error) {
	w, ok := e.workers[symbol]
	if !ok {
		return domain.Order{}, domain.ErrUnknownSymbol
	}
	return e.do(ctx, w, command{cancelID: id})
}

func (e *Engine) do(ctx context.Context, w *worker, cmd command) (domain.Order, error) {
	cmd.reply = make(chan reply, 1) // buffered: the worker never blocks on a gone caller
	select {
	case w.cmds <- cmd:
	case <-e.ctx.Done():
		return domain.Order{}, domain.ErrShuttingDown
	default:
		return domain.Order{}, domain.ErrOverloaded
	}
	select {
	case r := <-cmd.reply:
		return r.order, r.err
	case <-ctx.Done():
		return domain.Order{}, ctx.Err()
	case <-e.ctx.Done():
		return domain.Order{}, domain.ErrShuttingDown
	}
}

func (w *worker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-w.cmds:
			w.handle(cmd)
		}
	}
}

func (w *worker) handle(cmd command) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("engine panic recovered", "panic", r)
			select {
			case cmd.reply <- reply{err: fmt.Errorf("internal error")}:
			default:
			}
		}
	}()

	now := time.Now()
	var out domain.Outcome
	var res reply

	switch {
	case cmd.place != nil:
		o := *cmd.place
		trades, updated := w.book.Match(&o, now, w.nextTradeID)
		out = domain.Outcome{Symbol: w.symbol, Order: o, Updated: updated, Trades: trades}
		res = reply{order: o}
	case cmd.restore != nil:
		o := *cmd.restore
		w.book.LoadResting(o)
		out = domain.Outcome{Symbol: w.symbol, Order: o}
		res = reply{order: o}
	default:
		o, ok := w.book.Cancel(cmd.cancelID, now)
		if !ok {
			cmd.reply <- reply{err: domain.ErrNotCancelable}
			return
		}
		out = domain.Outcome{Symbol: w.symbol, Order: o}
		res = reply{order: o}
	}

	out.Book = w.book.Snapshot(domain.BookDepth, now)
	w.sink.Apply(out)
	cmd.reply <- res
}

func (w *worker) nextTradeID() string {
	w.tradeID++
	return fmt.Sprintf("%s-%d", w.symbol, w.tradeID)
}
