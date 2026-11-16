// Package ws implements the realtime WebSocket gateway.
//
// Concurrency model:
//   - One Hub goroutine owns all subscription state (no locks).
//   - Every connection has a readPump (handler goroutine) and a writePump.
//   - The Hub never blocks on a client: it hands messages over through a
//     buffered per-client channel with a non-blocking send. A full buffer
//     means the client is slow: ticker/orderbook messages are dropped (the next
//     one carries fresher data), anything else disconnects the client.
//   - Every goroutine ends when its client context or the hub context ends.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/hiuncy/minix/internal/domain"
)

const (
	maxSymbolsPerClient = 5
	readLimitBytes      = 4096
	writeTimeout        = 5 * time.Second
	pingInterval        = 20 * time.Second
	maxMsgsPerSecond    = 20
)

// outMsg is the single server -> client wire format. Fields are optional.
type outMsg struct {
	Type    string   `json:"type"` // event | ack | error
	Op      string   `json:"op,omitempty"`
	Channel string   `json:"channel,omitempty"`
	Symbol  string   `json:"symbol,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
	TS      int64    `json:"ts,omitempty"`
	Data    any      `json:"data,omitempty"`
	Code    string   `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

// clientMsg is the client -> server wire format.
type clientMsg struct {
	Op      string   `json:"op"` // subscribe | unsubscribe
	Channel string   `json:"channel"`
	Symbols []string `json:"symbols"`
}

type cmdKind int

const (
	cmdRegister cmdKind = iota
	cmdUnregister
	cmdOp
)

type hubCmd struct {
	kind cmdKind
	c    *Client
	msg  clientMsg
}

// Options configures a Hub.
type Options struct {
	SendBuffer  int // per-client outbound queue size
	EventBuffer int // hub inbound event queue size
	// Authenticate resolves the user of a connection request before the
	// upgrade. It returns "" for an anonymous (market data only) client and an
	// error to reject the connection with 401. When nil, the legacy
	// X-User-ID header / ?user= query is trusted.
	Authenticate func(*http.Request) (userID string, err error)
}

// Hub fans events out to subscribed clients.
type Hub struct {
	ctx     context.Context
	log     *slog.Logger
	symbols map[string]struct{}
	sendBuf int
	authn   func(*http.Request) (string, error)
	events  chan domain.Event
	cmds    chan hubCmd

	// owned by the hub goroutine
	clients map[*Client]struct{}
	subs    map[string]map[*Client]struct{}

	wg      sync.WaitGroup
	dropped atomic.Int64 // events dropped because the hub queue was full
	kicked  atomic.Int64 // slow clients disconnected
}

// NewHub creates a hub. Call Run in its own goroutine.
func NewHub(ctx context.Context, ins []domain.Instrument, opt Options, log *slog.Logger) *Hub {
	if opt.SendBuffer <= 0 {
		opt.SendBuffer = 256
	}
	if opt.EventBuffer <= 0 {
		opt.EventBuffer = 4096
	}
	if opt.Authenticate == nil {
		opt.Authenticate = legacyUser
	}
	syms := make(map[string]struct{}, len(ins))
	for _, i := range ins {
		syms[i.Code] = struct{}{}
	}
	return &Hub{
		ctx: ctx, log: log, symbols: syms, sendBuf: opt.SendBuffer, authn: opt.Authenticate,
		events:  make(chan domain.Event, opt.EventBuffer),
		cmds:    make(chan hubCmd, 256),
		clients: make(map[*Client]struct{}),
		subs:    make(map[string]map[*Client]struct{}),
	}
}

// Publish implements usecase.EventPublisher. It never blocks.
func (h *Hub) Publish(e domain.Event) {
	select {
	case h.events <- e:
	default:
		h.dropped.Add(1)
	}
}

// Stats returns counters useful for monitoring.
func (h *Hub) Stats() (dropped, kicked int64) { return h.dropped.Load(), h.kicked.Load() }

// Wait blocks until every connection handler has returned.
func (h *Hub) Wait() { h.wg.Wait() }

// Run is the hub event loop. It returns when the hub context is cancelled.
func (h *Hub) Run() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case e := <-h.events:
			h.dispatch(e)
		case c := <-h.cmds:
			switch c.kind {
			case cmdRegister:
				h.clients[c.c] = struct{}{}
			case cmdUnregister:
				h.removeClient(c.c)
			case cmdOp:
				h.handleOp(c.c, c.msg)
			}
		}
	}
}

func (h *Hub) submit(c hubCmd) {
	select {
	case h.cmds <- c:
	case <-h.ctx.Done():
	}
}

func subKey(channel, symbol string) string { return channel + "|" + symbol }

func (h *Hub) dispatch(e domain.Event) {
	key := subKey(e.Channel, e.Symbol)
	if e.Channel == domain.ChannelOrderUpdate {
		key = subKey(e.Channel, e.UserID)
	}
	set := h.subs[key]
	if len(set) == 0 {
		return
	}
	// Serialize once, fan out the same bytes to every subscriber.
	payload, err := json.Marshal(outMsg{
		Type: "event", Channel: e.Channel, Symbol: e.Symbol, TS: e.Time.UnixMilli(), Data: e.Data,
	})
	if err != nil {
		h.log.Error("marshal event", "err", err)
		return
	}
	droppable := e.Channel == domain.ChannelTicker || e.Channel == domain.ChannelOrderBook
	for c := range set {
		if !c.trySend(payload) {
			if droppable {
				continue
			}
			h.kick(c)
		}
	}
}

func (h *Hub) kick(c *Client) {
	h.kicked.Add(1)
	h.log.Warn("disconnecting slow client", "client", c.id)
	c.cancel() // readPump/writePump exit, ServeHTTP then unregisters
	h.removeClient(c)
}

func (h *Hub) removeClient(c *Client) {
	for key := range c.keys {
		if set := h.subs[key]; set != nil {
			delete(set, c)
			if len(set) == 0 {
				delete(h.subs, key)
			}
		}
	}
	c.keys = nil
	delete(h.clients, c)
}

func (h *Hub) handleOp(c *Client, m clientMsg) {
	if _, ok := h.clients[c]; !ok {
		return // already removed
	}
	reject := func(code, msg string) {
		c.reply(outMsg{Type: "error", Op: m.Op, Channel: m.Channel, Code: code, Message: msg})
	}
	if m.Op != "subscribe" && m.Op != "unsubscribe" {
		reject("INVALID_OP", "op must be subscribe or unsubscribe")
		return
	}

	var keys []string
	var applied []string
	switch m.Channel {
	case domain.ChannelOrderUpdate:
		if c.userID == "" {
			reject("UNAUTHENTICATED", "order.update requires a user id (?user= or X-User-ID header)")
			return
		}
		keys = []string{subKey(m.Channel, c.userID)}
	case domain.ChannelTicker, domain.ChannelTrade, domain.ChannelOrderBook:
		if len(m.Symbols) == 0 {
			reject("INVALID_SYMBOLS", "symbols is required")
			return
		}
		for _, s := range m.Symbols {
			s = upper(s)
			if _, ok := h.symbols[s]; !ok {
				reject("INVALID_SYMBOL", "unknown symbol "+s)
				return
			}
			keys = append(keys, subKey(m.Channel, s))
			applied = append(applied, s)
		}
	default:
		reject("INVALID_CHANNEL", "unknown channel "+m.Channel)
		return
	}

	if m.Op == "subscribe" {
		if c.distinctSymbolsWith(keys) > maxSymbolsPerClient {
			reject("TOO_MANY_SYMBOLS", "a client may subscribe to at most 5 symbols")
			return
		}
		if c.keys == nil {
			c.keys = make(map[string]struct{})
		}
		for _, k := range keys {
			c.keys[k] = struct{}{}
			if h.subs[k] == nil {
				h.subs[k] = make(map[*Client]struct{})
			}
			h.subs[k][c] = struct{}{}
		}
	} else {
		for _, k := range keys {
			delete(c.keys, k)
			if set := h.subs[k]; set != nil {
				delete(set, c)
				if len(set) == 0 {
					delete(h.subs, k)
				}
			}
		}
	}
	c.reply(outMsg{Type: "ack", Op: m.Op, Channel: m.Channel, Symbols: applied})
}

// legacyUser trusts the caller-supplied identity (no authentication).
func legacyUser(r *http.Request) (string, error) {
	if u := r.Header.Get("X-User-ID"); u != "" {
		return u, nil
	}
	return r.URL.Query().Get("user"), nil
}

// ServeHTTP upgrades the request and serves one WebSocket connection.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, err := h.authn(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="minix"`)
		http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	h.wg.Add(1)
	defer h.wg.Done()

	ctx, cancel := context.WithCancel(h.ctx)
	c := &Client{
		id: newClientID(), userID: userID, conn: conn,
		send: make(chan []byte, h.sendBuf), ctx: ctx, cancel: cancel, hub: h,
	}
	h.submit(hubCmd{kind: cmdRegister, c: c})
	h.log.Info("ws connected", "client", c.id, "user", userID)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writePump()
	}()
	c.readPump()

	cancel()
	<-writerDone
	h.submit(hubCmd{kind: cmdUnregister, c: c})
	_ = conn.CloseNow()
	h.log.Info("ws disconnected", "client", c.id)
}
