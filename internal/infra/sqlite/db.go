// Package sqlite provides SQLite-backed repositories for orders, trades,
// tickers, and users.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps *sql.DB and handles schema migrations and configurations.
type DB struct {
	*sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL CHECK(side IN ('BUY', 'SELL')),
    price INTEGER NOT NULL,
    quantity INTEGER NOT NULL,
    filled_quantity INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK(status IN ('OPEN', 'PARTIAL', 'FILLED', 'CANCELLED')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_symbol_status ON orders(symbol, status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at DESC);

CREATE TABLE IF NOT EXISTS trades (
    id TEXT PRIMARY KEY,
    symbol TEXT NOT NULL,
    price INTEGER NOT NULL,
    quantity INTEGER NOT NULL,
    buy_order_id TEXT NOT NULL,
    sell_order_id TEXT NOT NULL,
    taker_side TEXT NOT NULL CHECK(taker_side IN ('BUY', 'SELL')),
    timestamp DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_trades_symbol_timestamp ON trades(symbol, timestamp DESC);

CREATE TABLE IF NOT EXISTS tickers (
    symbol TEXT PRIMARY KEY,
    last_price INTEGER NOT NULL,
    open_price INTEGER NOT NULL,
    high_price INTEGER NOT NULL,
    low_price INTEGER NOT NULL,
    change INTEGER NOT NULL DEFAULT 0,
    change_pct REAL NOT NULL DEFAULT 0.0,
    volume INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL
);
`

// Open initializes SQLite connection with WAL mode and executes DDL migrations.
func Open(path string) (*DB, error) {
	if path != ":memory:" && !strings.Contains(path, "?mode=memory") {
		dir := filepath.Dir(path)
		if dir != "." && dir != "/" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create db directory: %w", err)
			}
		}
	}

	// Busy timeout 5000ms helps avoid "database is locked" on concurrent access.
	dsn := path
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Optimize connection pooling for SQLite.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &DB{DB: db}, nil
}
