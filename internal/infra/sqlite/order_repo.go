package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/usecase"
)

// OrderRepo implements usecase.OrderRepository using SQLite.
type OrderRepo struct {
	db *sql.DB
}

// NewOrderRepo creates a new SQLite-backed OrderRepo.
func NewOrderRepo(db *sql.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// Save inserts or updates an order in SQLite.
func (r *OrderRepo) Save(o domain.Order) {
	query := `
		INSERT INTO orders (id, user_id, symbol, side, price, quantity, filled_quantity, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			filled_quantity = excluded.filled_quantity,
			status = excluded.status,
			updated_at = excluded.updated_at
	`
	_, _ = r.db.Exec(query,
		o.ID,
		o.UserID,
		o.Symbol,
		string(o.Side),
		o.Price,
		o.Quantity,
		o.FilledQty,
		string(o.Status),
		o.CreatedAt.UTC(),
		o.UpdatedAt.UTC(),
	)
}

// Get fetches a single order by ID.
func (r *OrderRepo) Get(id string) (domain.Order, error) {
	query := `
		SELECT id, user_id, symbol, side, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders WHERE id = ?
	`
	row := r.db.QueryRow(query, id)

	var o domain.Order
	var side, status string
	var createdAt, updatedAt time.Time

	err := row.Scan(
		&o.ID,
		&o.UserID,
		&o.Symbol,
		&side,
		&o.Price,
		&o.Quantity,
		&o.FilledQty,
		&status,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Order{}, domain.ErrOrderNotFound
		}
		return domain.Order{}, fmt.Errorf("get order %s: %w", id, err)
	}

	o.Side = domain.Side(side)
	o.Status = domain.Status(status)
	o.CreatedAt = createdAt
	o.UpdatedAt = updatedAt
	return o, nil
}

// List returns filtered orders, ordered newest first.
func (r *OrderRepo) List(f usecase.OrderFilter) []domain.Order {
	var conditions []string
	var args []any

	if f.UserID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, f.UserID)
	}
	if f.Symbol != "" {
		conditions = append(conditions, "symbol = ?")
		args = append(args, strings.ToUpper(f.Symbol))
	}
	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(f.Status))
	}

	query := `
		SELECT id, user_id, symbol, side, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders
	`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return []domain.Order{}
	}
	defer rows.Close()

	out := make([]domain.Order, 0, min(limit, 64))
	for rows.Next() {
		var o domain.Order
		var side, status string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(
			&o.ID,
			&o.UserID,
			&o.Symbol,
			&side,
			&o.Price,
			&o.Quantity,
			&o.FilledQty,
			&status,
			&createdAt,
			&updatedAt,
		); err != nil {
			continue
		}

		o.Side = domain.Side(side)
		o.Status = domain.Status(status)
		o.CreatedAt = createdAt
		o.UpdatedAt = updatedAt
		out = append(out, o)
	}

	return out
}

// ActiveOrders retrieves all OPEN and PARTIAL orders ordered by created_at ASC for rehydration.
func (r *OrderRepo) ActiveOrders() ([]domain.Order, error) {
	query := `
		SELECT id, user_id, symbol, side, price, quantity, filled_quantity, status, created_at, updated_at
		FROM orders
		WHERE status IN ('OPEN', 'PARTIAL')
		ORDER BY created_at ASC, id ASC
	`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query active orders: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var o domain.Order
		var side, status string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(
			&o.ID,
			&o.UserID,
			&o.Symbol,
			&side,
			&o.Price,
			&o.Quantity,
			&o.FilledQty,
			&status,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan active order: %w", err)
		}

		o.Side = domain.Side(side)
		o.Status = domain.Status(status)
		o.CreatedAt = createdAt
		o.UpdatedAt = updatedAt
		orders = append(orders, o)
	}

	return orders, nil
}

