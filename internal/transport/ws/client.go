package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/coder/websocket"
)

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func newClientID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Client is one WebSocket connection.
type Client struct {
	id     string
	userID string
	conn   *websocket.Conn
	send   chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	hub    *Hub

	keys map[string]struct{} // subscription keys; owned by the hub goroutine
}

// trySend enqueues without blocking and reports whether it succeeded.
func (c *Client) trySend(b []byte) bool {
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

// reply sends a control message; a client that cannot even receive acks is
// too slow and gets disconnected (via its context).
func (c *Client) reply(m outMsg) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	if !c.trySend(b) {
		c.cancel()
	}
}

// distinctSymbolsWith counts distinct market symbols after adding keys.
// Called from the hub goroutine only.
func (c *Client) distinctSymbolsWith(keys []string) int {
	set := make(map[string]struct{})
	add := func(k string) {
		if i := strings.IndexByte(k, '|'); i >= 0 && !strings.HasPrefix(k, "order.update|") {
			set[k[i+1:]] = struct{}{}
		}
	}
	for k := range c.keys {
		add(k)
	}
	for _, k := range keys {
		add(k)
	}
	return len(set)
}

func (c *Client) readPump() {
	c.conn.SetReadLimit(readLimitBytes)
	windowStart := time.Now()
	count := 0
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		if now := time.Now(); now.Sub(windowStart) >= time.Second {
			windowStart, count = now, 0
		}
		if count++; count > maxMsgsPerSecond {
			c.reply(outMsg{Type: "error", Code: "RATE_LIMITED", Message: "too many messages"})
			_ = c.conn.Close(websocket.StatusPolicyViolation, "rate limited")
			return
		}
		var m clientMsg
		if err := json.Unmarshal(data, &m); err != nil {
			c.reply(outMsg{Type: "error", Code: "INVALID_JSON", Message: "message must be a JSON object"})
			continue
		}
		c.hub.submit(hubCmd{kind: cmdOp, c: c, msg: m})
	}
}

func (c *Client) writePump() {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.send:
			wctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}
