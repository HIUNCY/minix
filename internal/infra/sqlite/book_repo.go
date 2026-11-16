package sqlite

import (
	"sync"

	"github.com/hiuncy/minix/internal/domain"
)

// BookRepo keeps the latest aggregated order book snapshot in memory for instant read access.
type BookRepo struct {
	mu    sync.RWMutex
	books map[string]domain.BookSnapshot
}

// NewBookRepo creates a new in-memory BookRepo.
func NewBookRepo() *BookRepo {
	return &BookRepo{
		books: make(map[string]domain.BookSnapshot),
	}
}

// Set stores the latest snapshot of an order book.
func (r *BookRepo) Set(s domain.BookSnapshot) {
	r.mu.Lock()
	r.books[s.Symbol] = s
	r.mu.Unlock()
}

// Get retrieves the current snapshot of an order book.
func (r *BookRepo) Get(symbol string) (domain.BookSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.books[symbol]
	return s, ok
}

