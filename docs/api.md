# REST API

Base URL: `http://localhost:8080/api/v1`. All bodies are JSON. Success:
`{"data": ...}`. Error: `{"error": {"code": "...", "message": "..."}}`.

Identity: send `X-User-ID: <name>` (optional, default `anonymous`).
Prices/quantities are integers.

## Authentication

With `AUTH_ENABLED=true` (default) the order endpoints need
`Authorization: Bearer <token>`; the user is the token's `sub`. Market data,
trades and `/healthz` are public. (With `AUTH_ENABLED=false`, send `X-User-ID`
instead.)

### `POST /auth/login` → `200`

```bash
curl -s -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"username":"alice","password":"alice-pass"}'
```

```json
{"data":{"access_token":"eyJhbGciOi...","token_type":"Bearer","expires_at":"2026-11-05T17:30:00Z"}}
```

`401 UNAUTHORIZED` for wrong credentials, and for a missing/invalid/expired
token on protected endpoints (`WWW-Authenticate: Bearer`).

## Orders

### `POST /orders` – create a LIMIT order → `201`

```bash
curl -s -X POST localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d '{"symbol":"BBCA","side":"SELL","price":9000,"quantity":100}'
```

```json
{"data":{"id":"ORD-3f9c1a2b7d4e5f60","user_id":"alice","symbol":"BBCA","side":"SELL",
 "price":9000,"quantity":100,"filled_quantity":0,"status":"OPEN",
 "created_at":"2026-11-02T11:30:00Z","updated_at":"2026-11-02T11:30:00Z"}}
```

`side` is case-insensitive (`BUY`/`SELL`), `symbol` too. The response is the
order after matching (`OPEN`, `PARTIAL` or `FILLED`).

### `GET /orders` – list (newest first)

Query: `symbol`, `status` (`OPEN|PARTIAL|FILLED|CANCELLED`), `limit` (default 50,
max 500), `offset`. Only the caller's orders are returned (token user; in legacy
mode only when `X-User-ID` is sent).

```bash
curl -s -H "Authorization: Bearer $TOKEN" 'localhost:8080/api/v1/orders?symbol=BBCA&status=PARTIAL'
```

### `GET /orders/{id}` – one order

### `DELETE /orders/{id}` – cancel (owner only)

`200` with status `CANCELLED`; `403` other user; `404` unknown; `409` already
filled/cancelled.

## Trades

### `GET /trades?symbol=BBCA&limit=50` – trade history (newest first)

```json
{"data":[{"id":"BBCA-12","symbol":"BBCA","price":9000,"quantity":40,
 "buy_order_id":"ORD-...","sell_order_id":"ORD-...","taker_side":"BUY",
 "timestamp":"2026-11-02T11:30:01Z"}]}
```

## Market snapshot

| Endpoint | Description |
|---|---|
| `GET /market/tickers` | all tickers |
| `GET /market/tickers/{symbol}` | `last, open, high, low, change, change_pct, volume` |
| `GET /market/orderbook/{symbol}?depth=10` | aggregated bids (desc) / asks (asc) |
| `GET /market/trades/{symbol}?limit=50` | recent trades |

```json
{"data":{"symbol":"BBCA","bids":[{"price":8975,"quantity":120,"orders":2}],
 "asks":[{"price":9000,"quantity":60,"orders":1}],"updated_at":"..."}}
```

## Misc

`GET /healthz` → `{"status":"ok"}`; `GET /ws` → WebSocket (see
[websocket.md](websocket.md)).

## Errors

Unknown routes and wrong methods also use the JSON error envelope
(`404 NOT_FOUND`, `405 METHOD_NOT_ALLOWED` with an `Allow` header). Trailing
slashes are not redirected (`/api/v1/orders/` is a 404). Every response carries
an `X-Request-ID` (echoed if the client sends one).

| HTTP | code | When |
|---|---|---|
| 400 | `BAD_REQUEST` | malformed JSON, unknown field, bad `status` |
| 400 | `INVALID_ORDER` | bad symbol/side/price/quantity |
| 401 | `UNAUTHORIZED` | bad credentials, missing/invalid/expired token |
| 403 | `FORBIDDEN` | cancelling another user's order |
| 404 | `ORDER_NOT_FOUND`, `UNKNOWN_SYMBOL` | |
| 409 | `NOT_CANCELABLE` | order not resting in the book |
| 503 | `OVERLOADED`, `SHUTTING_DOWN` | engine queue full (`Retry-After: 1`) |
| 504 | `TIMEOUT` | engine did not answer in 2s |

