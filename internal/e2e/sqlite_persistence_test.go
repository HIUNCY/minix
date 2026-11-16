package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/engine"
	"github.com/hiuncy/minix/internal/infra/sqlite"
	httpapi "github.com/hiuncy/minix/internal/transport/http"
	"github.com/hiuncy/minix/internal/transport/ws"
	"github.com/hiuncy/minix/internal/usecase"
)

type sqliteApp struct {
	srv    *httptest.Server
	db     *sqlite.DB
	orders *usecase.OrderService
	market *usecase.MarketService
	stop   func()
}

func newSQLiteApp(t *testing.T, dbPath string) *sqliteApp {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ins := []domain.Instrument{{Code: "BBCA", RefPrice: 9000, Tick: 25}}
	ctx, cancel := context.WithCancel(context.Background())

	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	orders := sqlite.NewOrderRepo(db.DB)
	trades := sqlite.NewTradeRepo(db.DB)
	tickers := sqlite.NewTickerRepo(db.DB, ins)
	books := sqlite.NewBookRepo()

	hub := ws.NewHub(ctx, ins, ws.Options{SendBuffer: 64}, log)
	projector := usecase.NewProjector(orders, trades, tickers, books, hub)
	eng := engine.New(ctx, ins, projector, 1024, log)

	// Rehydrate resting orders
	if _, err := sqlite.Rehydrate(ctx, orders, eng, log); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}

	orderSvc := usecase.NewOrderService(eng, orders, ins)
	marketSvc := usecase.NewMarketService(tickers, books, trades, ins)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); hub.Run() }()

	srv := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Orders: orderSvc, Market: marketSvc, WS: hub, Log: log}))

	return &sqliteApp{
		srv:    srv,
		db:     db,
		orders: orderSvc,
		market: marketSvc,
		stop: func() {
			srv.Close()
			cancel()
			wg.Wait()
			eng.Wait()
			hub.Wait()
			_ = db.Close()
		},
	}
}

func TestSQLitePersistenceAndRestartRehydration(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "persist_test.db")

	// ==========================================
	// RUN 1: Place a resting order, then shutdown
	// ==========================================
	app1 := newSQLiteApp(t, dbFile)

	postOrder := func(app *sqliteApp, body string) (int, string) {
		req, _ := http.NewRequest("POST", app.srv.URL+"/api/v1/orders", strings.NewReader(body))
		req.Header.Set("X-User-ID", "alice")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := postOrder(app1, `{"symbol":"BBCA","side":"SELL","price":9500,"quantity":100}`)
	if code != 201 {
		t.Fatalf("run1 place order failed: %d %s", code, body)
	}

	// Verify order is present in OrderBook
	ob1, err := app1.market.OrderBook("BBCA", 5)
	if err != nil || len(ob1.Asks) != 1 || ob1.Asks[0].Quantity != 100 {
		t.Fatalf("run1 orderbook mismatch: %+v", ob1)
	}

	// Shutdown RUN 1
	app1.stop()

	// ==========================================
	// RUN 2: Start new server instance from same SQLite DB file
	// ==========================================
	app2 := newSQLiteApp(t, dbFile)
	defer app2.stop()

	// 1. Verify that the order was REHYDRATED into the active order book on boot!
	ob2, err := app2.market.OrderBook("BBCA", 5)
	if err != nil || len(ob2.Asks) != 1 || ob2.Asks[0].Price != 9500 || ob2.Asks[0].Quantity != 100 {
		t.Fatalf("rehydrated orderbook mismatch: %+v (expected 100 qty @ 9500)", ob2)
	}

	// 2. Submit a crossing BUY order on RUN 2
	code, _ = postOrder(app2, `{"symbol":"BBCA","side":"BUY","price":9500,"quantity":40}`)
	if code != 201 {
		t.Fatalf("run2 buy order failed: %d", code)
	}

	// 3. Verify match occurred against the rehydrated order!
	trades, err := app2.market.Trades("BBCA", 10)
	if err != nil || len(trades) != 1 || trades[0].Price != 9500 || trades[0].Quantity != 40 {
		t.Fatalf("trades after restart match: %+v", trades)
	}

	// 4. Verify resting order book now has remaining 60 quantity!
	ob3, err := app2.market.OrderBook("BBCA", 5)
	if err != nil || len(ob3.Asks) != 1 || ob3.Asks[0].Quantity != 60 {
		t.Fatalf("orderbook after partial fill mismatch: %+v (expected 60 remaining)", ob3)
	}

	// 5. Verify order status in DB is PARTIAL with filled_quantity 40
	var orderList struct {
		Data []domain.Order `json:"data"`
	}
	resp, err := http.Get(app2.srv.URL + "/api/v1/orders?symbol=BBCA")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(resp.Body).Decode(&orderList)

	found := false
	for _, o := range orderList.Data {
		if o.Side == domain.SideSell && o.Price == 9500 {
			found = true
			if o.Status != domain.StatusPartial || o.FilledQty != 40 {
				t.Fatalf("rehydrated order post-match state mismatch: %+v", o)
			}
		}
	}
	if !found {
		t.Fatal("original resting order not found in orders list after restart")
	}
}

