package sqlite

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/engine"
)

// Rehydrate loads active resting orders from SQLite into the matching engine book.
func Rehydrate(ctx context.Context, orderRepo *OrderRepo, eng *engine.Engine, log *slog.Logger) (int, error) {
	orders, err := orderRepo.ActiveOrders()
	if err != nil {
		return 0, fmt.Errorf("read active orders: %w", err)
	}

	count := 0
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for _, o := range orders {
		if o.Remaining() <= 0 {
			continue
		}
		if o.Status != domain.StatusOpen && o.Status != domain.StatusPartial {
			continue
		}
		if err := eng.Restore(ctx, o); err != nil {
			log.Warn("failed to restore resting order", "order_id", o.ID, "symbol", o.Symbol, "err", err)
			continue
		}
		count++
	}

	log.Info("rehydration complete", "resting_orders_restored", count)
	return count, nil
}

