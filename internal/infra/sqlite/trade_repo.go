package sqlite

import (
	"database/sql"
	"strings"
	"time"

	"github.com/hiuncy/minix/internal/domain"
)

// TradeRepo implements usecase.TradeRepository using SQLite.
type TradeRepo struct {
	db *sql.DB
}

// NewTradeRepo creates a new SQLite-backed TradeRepo.
func NewTradeRepo(db *sql.DB) *TradeRepo {
	return &TradeRepo{db: db}
}

// Add persists a trade execution.
func (r *TradeRepo) Add(t domain.Trade) {
	query := `
		INSERT OR IGNORE INTO trades (id, symbol, price, quantity, buy_order_id, sell_order_id, taker_side, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, _ = r.db.Exec(query,
		t.ID,
		t.Symbol,
		t.Price,
		t.Quantity,
		t.BuyOrderID,
		t.SellOrderID,
		string(t.TakerSide),
		t.Timestamp.UTC(),
	)
}

// List returns trades newest first, filtered optionally by symbol.
func (r *TradeRepo) List(symbol string, limit int) []domain.Trade {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, symbol, price, quantity, buy_order_id, sell_order_id, taker_side, timestamp
		FROM trades
	`
	var args []any
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol != "" {
		query += " WHERE symbol = ?"
		args = append(args, symbol)
	}
	query += " ORDER BY timestamp DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return []domain.Trade{}
	}
	defer rows.Close()

	out := make([]domain.Trade, 0, min(limit, 64))
	for rows.Next() {
		var t domain.Trade
		var takerSide string
		var ts time.Time

		if err := rows.Scan(
			&t.ID,
			&t.Symbol,
			&t.Price,
			&t.Quantity,
			&t.BuyOrderID,
			&t.SellOrderID,
			&takerSide,
			&ts,
		); err != nil {
			continue
		}

		t.TakerSide = domain.Side(takerSide)
		t.Timestamp = ts
		out = append(out, t)
	}

	return out
}

