package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/hiuncy/minix/internal/auth"
	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/engine"
	"github.com/hiuncy/minix/internal/infra/memory"
	httpapi "github.com/hiuncy/minix/internal/transport/http"
	"github.com/hiuncy/minix/internal/transport/ws"
	"github.com/hiuncy/minix/internal/usecase"
)

type app struct {
	srv       *httptest.Server
	hub       *ws.Hub
	orders    *usecase.OrderService
	trades    *memory.TradeRepo
	orderRepo *memory.OrderRepo
	stop      func()
}

func newApp(t *testing.T, sendBuf int) *app { return newAppAuth(t, sendBuf, nil) }

func newAppAuth(t *testing.T, sendBuf int, authSvc *auth.Service) *app {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ins := []domain.Instrument{{Code: "BBCA", RefPrice: 9000, Tick: 25}, {Code: "GOTO", RefPrice: 70, Tick: 1}}
	ctx, cancel := context.WithCancel(context.Background())

	orders, trades, market := memory.NewOrderRepo(), memory.NewTradeRepo(), memory.NewMarketRepo(ins)
	hubOpts := ws.Options{SendBuffer: sendBuf}
	if authSvc != nil {
		hubOpts.Authenticate = authSvc.WSAuthenticator()
	}
	hub := ws.NewHub(ctx, ins, hubOpts, log)
	eng := engine.New(ctx, ins, usecase.NewProjector(orders, trades, market, market.Books(), hub), 1024, log)
	orderSvc := usecase.NewOrderService(eng, orders, ins)
	marketSvc := usecase.NewMarketService(market, market.Books(), trades, ins)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); hub.Run() }()

	srv := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Orders: orderSvc, Market: marketSvc, WS: hub, Log: log, Auth: authSvc}))
	return &app{srv: srv, hub: hub, orders: orderSvc, trades: trades, orderRepo: orders, stop: func() {
		srv.Close()
		cancel()
		wg.Wait()
		eng.Wait()
		hub.Wait()
	}}
}

func post(t *testing.T, a *app, user string, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/orders", strings.NewReader(body))
	req.Header.Set("X-User-ID", user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestRESTCreateMatchAndSnapshots(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()

	if resp, _ := post(t, a, "alice", `{"symbol":"BBCA","side":"SELL","price":9000,"quantity":10}`); resp.StatusCode != 201 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, body := post(t, a, "bob", `{"symbol":"bbca","side":"buy","price":9100,"quantity":4}`)
	if resp.StatusCode != 201 || !bytes.Contains(body, []byte(`"FILLED"`)) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}

	get := func(path string, v any) {
		r, err := http.Get(a.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	var tr struct{ Data []domain.Trade }
	get("/api/v1/trades?symbol=BBCA", &tr)
	if len(tr.Data) != 1 || tr.Data[0].Price != 9000 || tr.Data[0].Quantity != 4 {
		t.Fatalf("trades %+v", tr.Data)
	}
	var tk struct{ Data domain.Ticker }
	get("/api/v1/market/tickers/BBCA", &tk)
	if tk.Data.Last != 9000 || tk.Data.Volume != 4 {
		t.Fatalf("ticker %+v", tk.Data)
	}
	var ob struct{ Data domain.BookSnapshot }
	get("/api/v1/market/orderbook/BBCA", &ob)
	if len(ob.Data.Asks) != 1 || ob.Data.Asks[0].Quantity != 6 {
		t.Fatalf("book %+v", ob.Data)
	}
	var ol struct{ Data []domain.Order }
	get("/api/v1/orders?symbol=BBCA&status=PARTIAL", &ol)
	if len(ol.Data) != 1 || ol.Data[0].UserID != "alice" {
		t.Fatalf("orders %+v", ol.Data)
	}
}

func TestRESTValidationAndErrors(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()
	for name, body := range map[string]string{
		"bad symbol": `{"symbol":"NOPE","side":"BUY","price":1,"quantity":1}`,
		"bad side":   `{"symbol":"BBCA","side":"HOLD","price":1,"quantity":1}`,
		"zero price": `{"symbol":"BBCA","side":"BUY","price":0,"quantity":1}`,
		"bad json":   `{`,
		"unknown":    `{"symbol":"BBCA","side":"BUY","price":1,"quantity":1,"x":1}`,
	} {
		if resp, b := post(t, a, "u", body); resp.StatusCode != 400 {
			t.Errorf("%s: status %d body %s", name, resp.StatusCode, b)
		}
	}
	r, _ := http.Get(a.srv.URL + "/api/v1/market/orderbook/NOPE")
	if r.StatusCode != 404 {
		t.Errorf("unknown symbol status %d", r.StatusCode)
	}
}

func TestCancel(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()
	_, b := post(t, a, "alice", `{"symbol":"GOTO","side":"BUY","price":50,"quantity":5}`)
	var created struct{ Data domain.Order }
	_ = json.Unmarshal(b, &created)

	del := func(user string) int {
		req, _ := http.NewRequest("DELETE", a.srv.URL+"/api/v1/orders/"+created.Data.ID, nil)
		req.Header.Set("X-User-ID", user)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := del("mallory"); c != 403 {
		t.Errorf("other user cancel = %d", c)
	}
	if c := del("alice"); c != 200 {
		t.Errorf("cancel = %d", c)
	}
	if c := del("alice"); c != 409 {
		t.Errorf("second cancel = %d", c)
	}
}

// Many goroutines hammer the same symbol. Run with -race. Invariants: every
// trade quantity is booked on both sides, and the fills of all orders equal
// twice the traded volume.
func TestConcurrentSubmissionsAreConsistent(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()

	const workers, perWorker = 16, 200
	var wg sync.WaitGroup
	var rejected atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				side := "BUY"
				if (w+i)%2 == 0 {
					side = "SELL"
				}
				_, err := a.orders.Create(context.Background(), usecase.CreateOrderInput{
					UserID: "u", Symbol: "BBCA", Side: side, Price: int64(9000 + (i%5)*25), Quantity: int64(1 + i%7),
				})
				if err != nil {
					rejected.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	var traded int64
	for _, tr := range a.trades.List("BBCA", 1<<30) {
		traded += tr.Quantity
	}
	var filled int64
	// Read the repository directly: the service caps page size at MaxLimit.
	for _, o := range a.orderRepo.List(usecase.OrderFilter{Limit: 1 << 30}) {
		filled += o.FilledQty
		if o.FilledQty > o.Quantity {
			t.Fatalf("overfilled order %+v", o)
		}
	}
	if filled != 2*traded {
		t.Fatalf("filled=%d traded=%d (rejected %d)", filled, traded, rejected.Load())
	}
	if traded == 0 {
		t.Fatal("expected some trades")
	}
}

func dial(t *testing.T, a *app, query string) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(a.srv.URL, "http")+"/ws"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, ctx
}

type msg struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel"`
	Symbol  string          `json:"symbol"`
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
}

func read(t *testing.T, c *websocket.Conn, ctx context.Context) msg {
	t.Helper()
	var m msg
	if err := wsjson.Read(ctx, c, &m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func TestWebSocketSubscribeAndEvents(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()
	conn, ctx := dial(t, a, "?user=alice")

	for _, ch := range []string{"market.trade", "market.ticker"} {
		_ = wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "channel": ch, "symbols": []string{"BBCA"}})
		if m := read(t, conn, ctx); m.Type != "ack" {
			t.Fatalf("expected ack, got %+v", m)
		}
	}
	_ = wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "channel": "order.update"})
	if m := read(t, conn, ctx); m.Type != "ack" {
		t.Fatalf("expected ack, got %+v", m)
	}

	post(t, a, "alice", `{"symbol":"BBCA","side":"SELL","price":9000,"quantity":3}`)
	post(t, a, "bob", `{"symbol":"BBCA","side":"BUY","price":9000,"quantity":3}`)

	seen := map[string]bool{}
	for len(seen) < 3 {
		m := read(t, conn, ctx)
		if m.Type == "event" {
			seen[m.Channel] = true
		}
	}
	for _, ch := range []string{"market.trade", "market.ticker", "order.update"} {
		if !seen[ch] {
			t.Errorf("missing event on %s", ch)
		}
	}
}

func TestWebSocketRejectsBadSubscriptions(t *testing.T) {
	a := newApp(t, 256)
	defer a.stop()
	conn, ctx := dial(t, a, "")

	cases := []struct {
		in   map[string]any
		code string
	}{
		{map[string]any{"op": "subscribe", "channel": "market.trade", "symbols": []string{"NOPE"}}, "INVALID_SYMBOL"},
		{map[string]any{"op": "subscribe", "channel": "nope", "symbols": []string{"BBCA"}}, "INVALID_CHANNEL"},
		{map[string]any{"op": "subscribe", "channel": "order.update"}, "UNAUTHENTICATED"},
		{map[string]any{"op": "dance"}, "INVALID_OP"},
		{map[string]any{"op": "subscribe", "channel": "market.trade"}, "INVALID_SYMBOLS"},
	}
	for _, c := range cases {
		_ = wsjson.Write(ctx, conn, c.in)
		if m := read(t, conn, ctx); m.Type != "error" || m.Code != c.code {
			t.Errorf("%v: got %+v want %s", c.in, m, c.code)
		}
	}
}

// A client that never reads must not stall the system: it gets disconnected
// while REST keeps working at full speed.
func TestSlowClientDoesNotBlock(t *testing.T) {
	a := newApp(t, 4) // tiny buffer
	defer a.stop()
	conn, ctx := dial(t, a, "")
	_ = wsjson.Write(ctx, conn, map[string]any{"op": "subscribe", "channel": "market.trade", "symbols": []string{"BBCA"}})
	read(t, conn, ctx) // ack, then stop reading

	start := time.Now()
	for i := 0; i < 400; i++ {
		side := "BUY"
		if i%2 == 0 {
			side = "SELL"
		}
		if _, err := a.orders.Create(context.Background(), usecase.CreateOrderInput{
			UserID: "u", Symbol: "BBCA", Side: side, Price: 9000, Quantity: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("submissions were slowed down by a slow client: %v", d)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, kicked := a.hub.Stats(); kicked >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("slow client was not disconnected")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
