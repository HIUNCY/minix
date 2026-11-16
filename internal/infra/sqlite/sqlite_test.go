package sqlite_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/engine"
	"github.com/hiuncy/minix/internal/infra/sqlite"
	"github.com/hiuncy/minix/internal/usecase"
)

type dummySink struct{}

func (d dummySink) Apply(domain.Outcome) {}

func setupTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOrderRepo(t *testing.T) {
	db := setupTestDB(t)
	repo := sqlite.NewOrderRepo(db.DB)

	now := time.Now().Truncate(time.Millisecond)
	o1 := domain.Order{
		ID:        "ORD-1",
		UserID:    "alice",
		Symbol:    "BBCA",
		Side:      domain.SideBuy,
		Price:     9000,
		Quantity:  10,
		FilledQty: 0,
		Status:    domain.StatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}

	repo.Save(o1)

	got, err := repo.Get("ORD-1")
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if got.ID != o1.ID || got.UserID != o1.UserID || got.Price != o1.Price {
		t.Fatalf("unexpected order: %+v", got)
	}

	// Update order
	o1.FilledQty = 5
	o1.Status = domain.StatusPartial
	repo.Save(o1)

	gotUpdated, err := repo.Get("ORD-1")
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if gotUpdated.FilledQty != 5 || gotUpdated.Status != domain.StatusPartial {
		t.Fatalf("update failed: %+v", gotUpdated)
	}

	// List filter
	list := repo.List(usecase.OrderFilter{UserID: "alice", Symbol: "BBCA", Status: domain.StatusPartial})
	if len(list) != 1 || list[0].ID != "ORD-1" {
		t.Fatalf("list mismatch: %+v", list)
	}

	// Active orders for rehydration
	active, err := repo.ActiveOrders()
	if err != nil || len(active) != 1 {
		t.Fatalf("active orders: %v %+v", err, active)
	}
}

func TestTradeRepo(t *testing.T) {
	db := setupTestDB(t)
	repo := sqlite.NewTradeRepo(db.DB)

	now := time.Now().Truncate(time.Millisecond)
	tr := domain.Trade{
		ID:          "TR-1",
		Symbol:      "BBCA",
		Price:       9000,
		Quantity:    10,
		BuyOrderID:  "ORD-1",
		SellOrderID: "ORD-2",
		TakerSide:   domain.SideBuy,
		Timestamp:   now,
	}

	repo.Add(tr)

	list := repo.List("BBCA", 10)
	if len(list) != 1 || list[0].ID != "TR-1" || list[0].Price != 9000 {
		t.Fatalf("trade list mismatch: %+v", list)
	}
}

func TestTickerRepo(t *testing.T) {
	db := setupTestDB(t)
	ins := []domain.Instrument{{Code: "BBCA", RefPrice: 9000, Tick: 25}}
	repo := sqlite.NewTickerRepo(db.DB, ins)

	tk, ok := repo.Get("BBCA")
	if !ok || tk.Last != 9000 {
		t.Fatalf("initial ticker mismatch: %+v", tk)
	}

	repo.ApplyTrade(domain.Trade{
		ID:        "TR-1",
		Symbol:    "BBCA",
		Price:     9100,
		Quantity:  50,
		Timestamp: time.Now(),
	})

	tkAfter, ok := repo.Get("BBCA")
	if !ok || tkAfter.Last != 9100 || tkAfter.Volume != 50 {
		t.Fatalf("updated ticker mismatch: %+v", tkAfter)
	}
}

func TestUserRepo(t *testing.T) {
	db := setupTestDB(t)
	repo := sqlite.NewUserRepo(db.DB)

	err := repo.SeedUsers("alice:secret123,bob:pass456")
	if err != nil {
		t.Fatalf("seed users: %v", err)
	}

	uid, err := repo.Authenticate("alice", "secret123")
	if err != nil || uid != "alice" {
		t.Fatalf("auth alice failed: %v", err)
	}

	_, err = repo.Authenticate("alice", "wrongpass")
	if err == nil {
		t.Fatal("expected error on wrong password")
	}
}

func TestRehydration(t *testing.T) {
	db := setupTestDB(t)
	orderRepo := sqlite.NewOrderRepo(db.DB)

	now := time.Now()
	orderRepo.Save(domain.Order{
		ID:        "ORD-RESTING-1",
		UserID:    "alice",
		Symbol:    "BBCA",
		Side:      domain.SideSell,
		Price:     9500,
		Quantity:  10,
		Status:    domain.StatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ins := []domain.Instrument{{Code: "BBCA", RefPrice: 9000, Tick: 25}}
	eng := engine.New(ctx, ins, dummySink{}, 1024, log)

	count, err := sqlite.Rehydrate(ctx, orderRepo, eng, log)
	if err != nil || count != 1 {
		t.Fatalf("rehydrate error: %v, count=%d", err, count)
	}
}

