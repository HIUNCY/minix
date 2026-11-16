# WebSocket

## Connect

```
ws://localhost:8080/ws                      # anonymous: public market channels only
ws://localhost:8080/ws?token=<jwt>          # authenticated (browsers)
Authorization: Bearer <jwt>                 # authenticated (non-browser clients)
```

Get a token from `POST /api/v1/auth/login` (see [api.md](api.md)). The user for
`order.update` is the token's `sub`. An invalid or expired token is rejected
with HTTP `401` before the upgrade; no token = anonymous (`order.update` →
`UNAUTHENTICATED`). With `AUTH_ENABLED=false` the legacy `?user=` /
`X-User-ID` identity is used instead. Beware that a token in the query string
can end up in proxy logs; prefer the header outside browsers. The token is only
checked at connect time, so reconnect after it expires.
Max 5 distinct symbols per connection,
max 20 messages/second, max 4 KB per message.

Tools: [`websocat`](https://github.com/vi/websocat), `wscat`
(`npx wscat -c "ws://localhost:8080/ws?token=$TOKEN"`), or Postman's WebSocket
request.

```bash
websocat "ws://localhost:8080/ws?token=$TOKEN"
{"op":"subscribe","channel":"market.ticker","symbols":["BBCA","BBRI"]}
```

## Client → server

```json
{"op":"subscribe",   "channel":"market.trade", "symbols":["BBCA"]}
{"op":"unsubscribe", "channel":"market.trade", "symbols":["BBCA"]}
{"op":"subscribe",   "channel":"order.update"}
```

| Channel | Needs `symbols` | Payload (`data`) |
|---|---|---|
| `market.ticker` | yes | ticker: `last, open, high, low, change, change_pct, volume` (sent on every trade) |
| `market.trade` | yes | one trade |
| `market.orderbook` | yes | top-20 bid/ask levels snapshot (sent on every book change) |
| `order.update` | no (uses your `user`) | the order in its new state (`OPEN/PARTIAL/FILLED/CANCELLED`) |

## Server → client

Acknowledgement:

```json
{"type":"ack","op":"subscribe","channel":"market.trade","symbols":["BBCA"]}
```

Event:

```json
{"type":"event","channel":"market.trade","symbol":"BBCA","ts":1762057801000,
 "data":{"id":"BBCA-12","symbol":"BBCA","price":9000,"quantity":40,
         "buy_order_id":"ORD-...","sell_order_id":"ORD-...","taker_side":"BUY",
         "timestamp":"2026-11-02T04:30:01Z"}}
```

Error:

```json
{"type":"error","code":"INVALID_SYMBOL","message":"unknown symbol NOPE"}
```

Codes: `INVALID_JSON`, `INVALID_OP`, `INVALID_CHANNEL`, `INVALID_SYMBOLS`,
`INVALID_SYMBOL`, `UNAUTHENTICATED`, `TOO_MANY_SYMBOLS`, `RATE_LIMITED`.

## Delivery semantics

- Per connection, events keep the order in which the engine produced them.
- Slow consumers: `market.ticker` / `market.orderbook` messages may be skipped
  (each carries the latest state); a client too slow for `market.trade` or
  `order.update` is disconnected and should reconnect and re-fetch the REST
  snapshot (`/market/...`) before resubscribing.
- Use the REST snapshots to render initial state, then apply stream updates.

## Try it

1. Terminal A: `websocat 'ws://localhost:8080/ws?user=alice'` and subscribe to
   `market.trade` for `BBCA` and `order.update`.
2. Terminal B: place a crossing SELL and BUY with `curl` (see
   [api.md](api.md)); terminal A receives `order.update` and `market.trade`.
3. The simulator already produces constant `market.ticker` / `market.trade`
   traffic.

