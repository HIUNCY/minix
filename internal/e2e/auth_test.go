package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/hiuncy/minix/internal/auth"
)

func newAuthApp(t *testing.T) *app {
	t.Helper()
	svc, err := auth.New("0123456789abcdef0123456789abcdef", time.Minute,
		map[string]string{"alice": "alice-pass", "bob": "bob-pass"})
	if err != nil {
		t.Fatal(err)
	}
	return newAppAuth(t, 256, svc)
}

func do(t *testing.T, a *app, method, path, token, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func login(t *testing.T, a *app, user, pass string) string {
	t.Helper()
	code, b := do(t, a, "POST", "/api/v1/auth/login", "", `{"username":"`+user+`","password":"`+pass+`"}`)
	if code != 200 {
		t.Fatalf("login %s: %d %s", user, code, b)
	}
	var raw struct {
		Data struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
		}
	}
	if err := json.Unmarshal(b, &raw); err != nil || raw.Data.AccessToken == "" || raw.Data.TokenType != "Bearer" {
		t.Fatalf("bad login response %s", b)
	}
	return raw.Data.AccessToken
}

func TestAuthLoginFailures(t *testing.T) {
	a := newAuthApp(t)
	defer a.stop()
	for _, body := range []string{
		`{"username":"alice","password":"wrong"}`,
		`{"username":"nobody","password":"x"}`,
		`{"username":"","password":""}`,
	} {
		if code, _ := do(t, a, "POST", "/api/v1/auth/login", "", body); code != 401 {
			t.Errorf("%s: status %d", body, code)
		}
	}
}

func TestAuthProtectsOrderEndpoints(t *testing.T) {
	a := newAuthApp(t)
	defer a.stop()
	order := `{"symbol":"BBCA","side":"BUY","price":9000,"quantity":1}`

	if code, _ := do(t, a, "POST", "/api/v1/orders", "", order); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := do(t, a, "POST", "/api/v1/orders", "garbage", order); code != 401 {
		t.Errorf("bad token: %d", code)
	}
	// The legacy header must NOT grant access when JWT is on.
	req, _ := http.NewRequest("POST", a.srv.URL+"/api/v1/orders", strings.NewReader(order))
	req.Header.Set("X-User-ID", "alice")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("X-User-ID header accepted: %d", resp.StatusCode)
	}

	tok := login(t, a, "alice", "alice-pass")
	code, b := do(t, a, "POST", "/api/v1/orders", tok, order)
	if code != 201 || !strings.Contains(string(b), `"user_id":"alice"`) {
		t.Fatalf("authorized create: %d %s", code, b)
	}
}

func TestAuthUserCannotActAsAnotherUser(t *testing.T) {
	a := newAuthApp(t)
	defer a.stop()
	alice := login(t, a, "alice", "alice-pass")
	bob := login(t, a, "bob", "bob-pass")

	_, b := do(t, a, "POST", "/api/v1/orders", alice, `{"symbol":"GOTO","side":"BUY","price":50,"quantity":5}`)
	var created struct{ Data struct{ ID string } }
	_ = json.Unmarshal(b, &created)

	if code, _ := do(t, a, "DELETE", "/api/v1/orders/"+created.Data.ID, bob, ""); code != 403 {
		t.Errorf("bob cancelling alice's order: %d", code)
	}
	// Order lists are always scoped to the token's user.
	_, b = do(t, a, "GET", "/api/v1/orders", bob, "")
	if strings.Contains(string(b), created.Data.ID) {
		t.Errorf("bob sees alice's order: %s", b)
	}
	_, b = do(t, a, "GET", "/api/v1/orders", alice, "")
	if !strings.Contains(string(b), created.Data.ID) {
		t.Errorf("alice does not see her order: %s", b)
	}
	if code, _ := do(t, a, "DELETE", "/api/v1/orders/"+created.Data.ID, alice, ""); code != 200 {
		t.Errorf("alice cancel: %d", code)
	}
}

func TestAuthMarketDataStaysPublic(t *testing.T) {
	a := newAuthApp(t)
	defer a.stop()
	for _, p := range []string{"/healthz", "/api/v1/market/tickers", "/api/v1/market/orderbook/BBCA", "/api/v1/trades"} {
		if code, _ := do(t, a, "GET", p, "", ""); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestAuthWebSocket(t *testing.T) {
	a := newAuthApp(t)
	defer a.stop()
	wsURL := "ws" + strings.TrimPrefix(a.srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Invalid token: rejected before the upgrade.
	if _, resp, err := websocket.Dial(ctx, wsURL+"?token=garbage", nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("invalid token must give 401, err=%v resp=%v", err, resp)
	}
	// ?user= must not be trusted anymore: anonymous cannot get order.update.
	anon, _, err := websocket.Dial(ctx, wsURL+"?user=alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer anon.CloseNow()
	_ = wsjson.Write(ctx, anon, map[string]any{"op": "subscribe", "channel": "order.update"})
	if m := read(t, anon, ctx); m.Type != "error" || m.Code != "UNAUTHENTICATED" {
		t.Fatalf("anonymous order.update: %+v", m)
	}
	// Anonymous clients still get public market channels.
	_ = wsjson.Write(ctx, anon, map[string]any{"op": "subscribe", "channel": "market.trade", "symbols": []string{"BBCA"}})
	if m := read(t, anon, ctx); m.Type != "ack" {
		t.Fatalf("anonymous market subscribe: %+v", m)
	}

	// Valid token (query and header): order.update works for the token's user.
	tok := login(t, a, "alice", "alice-pass")
	authed, _, err := websocket.Dial(ctx, wsURL+"?token="+tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer authed.CloseNow()
	_ = wsjson.Write(ctx, authed, map[string]any{"op": "subscribe", "channel": "order.update"})
	if m := read(t, authed, ctx); m.Type != "ack" {
		t.Fatalf("authed order.update: %+v", m)
	}
	do(t, a, "POST", "/api/v1/orders", tok, `{"symbol":"BBCA","side":"BUY","price":9000,"quantity":1}`)
	if m := read(t, authed, ctx); m.Type != "event" || m.Channel != "order.update" {
		t.Fatalf("expected order.update event, got %+v", m)
	}

	hdr := http.Header{"Authorization": {"Bearer " + tok}}
	viaHeader, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("bearer header dial: %v", err)
	}
	viaHeader.CloseNow()
}
