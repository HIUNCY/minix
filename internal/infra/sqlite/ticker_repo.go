package sqlite

import (
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/hiuncy/minix/internal/domain"
)

// TickerRepo implements usecase.TickerRepository using SQLite with an in-memory cache
// for sub-millisecond read access.
type TickerRepo struct {
	db      *sql.DB
	mu      sync.RWMutex
	tickers map[string]domain.Ticker
	codes   []string
}

// NewTickerRepo initializes the ticker repository from DB or default instruments.
func NewTickerRepo(db *sql.DB, ins []domain.Instrument) *TickerRepo {
	r := &TickerRepo{
		db:      db,
		tickers: make(map[string]domain.Ticker),
	}

	// 1. Initialize default values
	for _, in := range ins {
		r.tickers[in.Code] = domain.NewTicker(in.Code, in.RefPrice)
		r.codes = append(r.codes, in.Code)
	}

	// 2. Load existing persisted tickers from SQLite if available
	rows, err := db.Query(`
		SELECT symbol, last_price, open_price, high_price, low_price, change, change_pct, volume, updated_at
		FROM tickers
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t domain.Ticker
			var updatedAt time.Time
			if err := rows.Scan(
				&t.Symbol,
				&t.Last,
				&t.Open,
				&t.High,
				&t.Low,
				&t.Change,
				&t.ChangePct,
				&t.Volume,
				&updatedAt,
			); err == nil {
				t.UpdatedAt = updatedAt
				r.tickers[t.Symbol] = t
			}
		}
	}

	return r
}

// ApplyTrade updates the in-memory cache and asynchronously persists the ticker to SQLite.
func (r *TickerRepo) ApplyTrade(t domain.Trade) {
	r.mu.Lock()
	symbol := t.Symbol
	tk, ok := r.tickers[symbol]
	if !ok {
		tk = domain.NewTicker(symbol, t.Price)
	}
	tk.ApplyTrade(t)
	r.tickers[symbol] = tk
	r.mu.Unlock()

	// Persist to SQLite
	query := `
		INSERT INTO tickers (symbol, last_price, open_price, high_price, low_price, change, change_pct, volume, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol) DO UPDATE SET
			last_price = excluded.last_price,
			open_price = excluded.open_price,
			high_price = excluded.high_price,
			low_price = excluded.low_price,
			change = excluded.change,
			change_pct = excluded.change_pct,
			volume = excluded.volume,
			updated_at = excluded.updated_at
	`
	_, _ = r.db.Exec(query,
		tk.Symbol,
		tk.Last,
		tk.Open,
		tk.High,
		tk.Low,
		tk.Change,
		tk.ChangePct,
		tk.Volume,
		tk.UpdatedAt.UTC(),
	)
}

// Get returns the ticker for a single symbol.
func (r *TickerRepo) Get(symbol string) (domain.Ticker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tk, ok := r.tickers[strings.ToUpper(symbol)]
	return tk, ok
}

// All returns all tickers.
func (r *TickerRepo) All() []domain.Ticker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Ticker, 0, len(r.codes))
	for _, c := range r.codes {
		out = append(out, r.tickers[c])
	}
	return out
}

