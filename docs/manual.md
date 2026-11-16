# Manual: Menjalankan & Menguji Aplikasi

Panduan langkah demi langkah untuk menjalankan Mini Exchange dan menguji setiap fitur secara manual. Referensi lengkap ada di [api.md](api.md) dan [websocket.md](websocket.md).

> [!NOTE]
> Perintah di bawah mengikuti perilaku yang sudah dicakup test otomatis (`make race`). Saat menulis manual ini server belum dijalankan secara manual, jadi bila ada output yang berbeda, cek bagian [Troubleshooting](#9-troubleshooting).

---

## 1. Prasyarat

| Kebutuhan | Keterangan |
|---|---|
| Go | versi sesuai `go` directive di `go.mod` |
| `curl` | untuk REST |
| `jq` | merapikan JSON dan mengambil token (opsional tapi disarankan) |
| `websocat` **atau** Node.js (`npx wscat`) | untuk WebSocket |

Instal WebSocket client (pilih salah satu):

```bash
# websocat (https://github.com/vi/websocat)
cargo install websocat        # atau lewat package manager distro
# wscat
npx wscat --version
```

---

## 2. Menjalankan Aplikasi

```bash
cd /home/zay/stress/go/test
make run            # sama dengan: go run ./cmd/server
```

Log yang diharapkan:

```
level=INFO msg="JWT auth enabled" users=2 ttl=1h0m0s
level=INFO msg=listening addr=:8080 simulator=true
```

> [!WARNING]
> Jika `JWT_SECRET` tidak diisi akan muncul peringatan dan token **tidak valid lagi setelah restart**. Untuk sesi pengujian yang stabil, set secret (minimal 32 karakter).

### Mode yang disarankan

| Tujuan | Perintah |
|---|---|
| **Uji fungsional deterministik** (tanpa order bot) | `SIM_ENABLED=false JWT_SECRET=THIS_IS_SECRET_KEY_MUST_32_CHAR_LENGTH make run` |
| **Demo realtime** (ada trade otomatis) | `JWT_SECRET=THIS_IS_SECRET_KEY_MUST_32_CHAR_LENGTH make run` |
| **Mode lama tanpa JWT** (`X-User-ID`) | `AUTH_ENABLED=false make run` |
| **Port lain** | `PORT=9090 make run` |

Variabel lingkungan lengkap ada di [README.md](../README.md#run). Contoh akun demo bawaan:

| Username | Password |
|---|---|
| `alice` | `alice-pass` |
| `bob` | `bob-pass` |

Symbol yang tersedia: `BBCA, BBRI, TLKM, ASII, GOTO`.

### Alternatif: Docker

```bash
docker build -t minix .
docker run --rm -p 8080:8080 -e JWT_SECRET=THIS_IS_SECRET_KEY_MUST_32_CHAR_LENGTH minix
```

> Untuk semua bagian berikutnya, jalankan server di **terminal A** dengan `SIM_ENABLED=false` supaya hasil mudah diprediksi, lalu gunakan **terminal B** (dan C) untuk pengujian.

---

## 3. Persiapan Pengujian

Di terminal B:

```bash
export HOST=http://localhost:8080

# Login dan simpan token
export ALICE=$(curl -s -X POST $HOST/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"alice-pass"}' | jq -r .data.access_token)
export BOB=$(curl -s -X POST $HOST/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"bob","password":"bob-pass"}' | jq -r .data.access_token)

echo "$ALICE" | cut -c1-30   # harus terisi, bukan "null"
```

---

## 4. Uji Fitur REST

### 4.1 Health check

```bash
curl -s $HOST/healthz
```
✅ Harapan: `{"status":"ok"}`

### 4.2 Autentikasi (JWT)

| Skenario | Perintah | Harapan |
|---|---|---|
| Login benar | lihat bagian 3 | `200`, ada `access_token`, `token_type: "Bearer"`, `expires_at` |
| Password salah | `curl -s -i -X POST $HOST/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"alice","password":"salah"}'` | `401`, `code: UNAUTHORIZED`, header `WWW-Authenticate` |
| Tanpa token | `curl -s -i $HOST/api/v1/orders` | `401 UNAUTHORIZED` |
| Token rusak | `curl -s -i -H 'Authorization: Bearer abc' $HOST/api/v1/orders` | `401` |
| Header lama diabaikan | `curl -s -i -H 'X-User-ID: alice' $HOST/api/v1/orders` | `401` |
| Endpoint publik tanpa token | `curl -s $HOST/api/v1/market/tickers` | `200` |

Uji token kedaluwarsa: jalankan server dengan `JWT_TTL=5s`, login, tunggu 6 detik, lalu panggil `/api/v1/orders` → `401`.

### 4.3 Create order & validasi

```bash
curl -s -X POST $HOST/api/v1/orders \
  -H "Authorization: Bearer $ALICE" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"SELL","price":9000,"quantity":100}' | jq
```
✅ Harapan: `201`, `status: "OPEN"`, `user_id: "alice"`, `filled_quantity: 0`, `id` berawalan `ORD-`.

Simpan id untuk langkah selanjutnya:

```bash
export SELL_ID=$(curl -s -X POST $HOST/api/v1/orders \
  -H "Authorization: Bearer $ALICE" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"SELL","price":9100,"quantity":50}' | jq -r .data.id)
```

**Validasi** (semua harus `400`):

| Body | `code` |
|---|---|
| `{"symbol":"NOPE","side":"BUY","price":100,"quantity":1}` | `INVALID_ORDER` |
| `{"symbol":"BBCA","side":"HOLD","price":100,"quantity":1}` | `INVALID_ORDER` |
| `{"symbol":"BBCA","side":"BUY","price":0,"quantity":1}` | `INVALID_ORDER` |
| `{"symbol":"BBCA","side":"BUY","price":100,"quantity":-5}` | `INVALID_ORDER` |
| `{"symbol":"BBCA","side":"BUY","price":100,"quantity":1,"x":1}` | `BAD_REQUEST` (field tak dikenal) |
| `{` (JSON rusak) | `BAD_REQUEST` |

Contoh:

```bash
curl -s -i -X POST $HOST/api/v1/orders -H "Authorization: Bearer $ALICE" \
  -H 'Content-Type: application/json' -d '{"symbol":"BBCA","side":"HOLD","price":100,"quantity":1}'
```

### 4.4 Matching engine

**Full fill + eksekusi di harga order yang menunggu (maker)**

```bash
# Alice sudah punya SELL 100 @ 9000 (langkah 4.3). Bob BUY 100 @ 9500 (harga lebih tinggi):
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $BOB" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"BUY","price":9500,"quantity":100}' | jq .data
```
✅ Harapan: `status: "FILLED"`. Cek trade: harga eksekusi **9000** (harga maker), bukan 9500.

```bash
curl -s "$HOST/api/v1/trades?symbol=BBCA" | jq '.data[0]'
```

**Partial fill**

```bash
# Bob BUY 80 @ 9100, tersedia SELL 50 @ 9100 (SELL_ID dari 4.3)
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $BOB" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"BUY","price":9100,"quantity":80}' | jq .data
```
✅ Harapan: `status: "PARTIAL"`, `filled_quantity: 50`. Sisa 30 masuk order book sebagai BUY @ 9100. Order SELL milik Alice menjadi `FILLED`:

```bash
curl -s -H "Authorization: Bearer $ALICE" $HOST/api/v1/orders/$SELL_ID | jq .data.status   # "FILLED"
```

**Tidak match (harga tidak bertemu)**

```bash
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $BOB" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BBRI","side":"BUY","price":4000,"quantity":10}' | jq .data.status   # "OPEN"
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $ALICE" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"BBRI","side":"SELL","price":4500,"quantity":10}' | jq .data.status  # "OPEN"
```

**Prioritas harga lalu waktu (FIFO)**: buat dua SELL `TLKM` di harga sama (A lebih dulu, lalu B), lalu satu BUY yang menghabiskan keduanya. Lihat `GET /api/v1/trades?symbol=TLKM`: trade pertama harus melawan SELL A.

### 4.5 Daftar order & filter

```bash
curl -s -H "Authorization: Bearer $ALICE" "$HOST/api/v1/orders" | jq '.data | length'
curl -s -H "Authorization: Bearer $ALICE" "$HOST/api/v1/orders?symbol=BBRI" | jq '.data[].symbol'
curl -s -H "Authorization: Bearer $BOB"   "$HOST/api/v1/orders?status=PARTIAL" | jq '.data[].status'
curl -s -H "Authorization: Bearer $ALICE" "$HOST/api/v1/orders?limit=1&offset=1" | jq '.data | length'
curl -s -i -H "Authorization: Bearer $ALICE" "$HOST/api/v1/orders?status=BAHAYA"   # 400
```
✅ Harapan: urutan terbaru dulu. Alice hanya melihat order miliknya. `limit` maks 500, default 50.

### 4.6 Detail & cancel order

```bash
# Buat order lalu cancel
ID=$(curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $ALICE" \
  -H 'Content-Type: application/json' \
  -d '{"symbol":"GOTO","side":"BUY","price":50,"quantity":5}' | jq -r .data.id)

curl -s -i -X DELETE $HOST/api/v1/orders/$ID -H "Authorization: Bearer $BOB"     # 403 FORBIDDEN
curl -s    -X DELETE $HOST/api/v1/orders/$ID -H "Authorization: Bearer $ALICE" | jq .data.status  # "CANCELLED"
curl -s -i -X DELETE $HOST/api/v1/orders/$ID -H "Authorization: Bearer $ALICE"   # 409 NOT_CANCELABLE
curl -s -i -H "Authorization: Bearer $ALICE" $HOST/api/v1/orders/ORD-tidakada    # 404 ORDER_NOT_FOUND
```

### 4.7 Trade history

```bash
curl -s "$HOST/api/v1/trades?limit=10" | jq
curl -s "$HOST/api/v1/trades?symbol=BBCA" | jq '.data | length'
curl -s -i "$HOST/api/v1/trades?symbol=NOPE"        # 404 UNKNOWN_SYMBOL
```
✅ Terbaru dulu. Field: `id, symbol, price, quantity, buy_order_id, sell_order_id, taker_side, timestamp`.

### 4.8 Market snapshot

```bash
curl -s $HOST/api/v1/market/tickers | jq                       # semua symbol
curl -s $HOST/api/v1/market/tickers/BBCA | jq                  # last, open, high, low, change, change_pct, volume
curl -s "$HOST/api/v1/market/orderbook/BBCA?depth=5" | jq      # bids (turun), asks (naik)
curl -s "$HOST/api/v1/market/trades/BBCA?limit=5" | jq
curl -s -i $HOST/api/v1/market/orderbook/NOPE                  # 404 UNKNOWN_SYMBOL
```
✅ Setelah trade di 4.4, `last` = 9000/9100 dan `volume` bertambah. Order book memuat sisa BUY 30 @ 9100 pada `bids`.

### 4.9 Error umum

```bash
curl -s -i $HOST/api/v1/tidakada              # 404 NOT_FOUND (JSON)
curl -s -i -X PUT $HOST/healthz               # 405 METHOD_NOT_ALLOWED + header Allow
curl -s -i $HOST/healthz/                     # 404, tidak di-redirect
curl -s -i -H 'X-Request-ID: abc123' $HOST/healthz   # X-Request-ID dipantulkan
```

### 4.10 Uji Persistensi SQLite & Server Restart Rehydration

Data order, trade, ticker, dan akun user kini tersimpan di SQLite (`./data/minix.db`). Uji ketahanan data saat server mati:

1. Buat order resting di terminal B:
```bash
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $ALICE" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"SELL","price":9800,"quantity":50}' | jq .data.status   # "OPEN"
```
2. Cek OrderBook:
```bash
curl -s "$HOST/api/v1/market/orderbook/BBCA?depth=5" | jq .data.asks
```
3. Di terminal A, matikan server (`Ctrl+C`), lalu jalankan kembali:
```bash
make run
```
4. Di terminal B, langsung cek OrderBook **tanpa input order baru**:
```bash
curl -s "$HOST/api/v1/market/orderbook/BBCA?depth=5" | jq .data.asks
```
✅ Harapan: order SELL 50 @ 9800 **otomatis ter-rehydrate** ke dalam memory orderbook!
5. Kirim order BUY crossing:
```bash
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $BOB" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"BUY","price":9800,"quantity":20}' | jq .data.status   # "FILLED"
```
✅ Harapan: Order langsung match dengan order yang direhidrasi, trade tercatat, dan sisa order SELL menjadi 30.


---

## 5. Uji Fitur WebSocket

Gunakan **terminal C** untuk WebSocket, terminal B untuk memicu order via REST. (Variabel `ALICE`/`BOB` perlu di-export ulang di terminal C jika dipakai.)

### 5.1 Koneksi & subscribe market

```bash
websocat "ws://localhost:8080/ws"
# atau: npx wscat -c "ws://localhost:8080/ws"
```

Ketik (satu pesan per baris):

```json
{"op":"subscribe","channel":"market.trade","symbols":["BBCA"]}
{"op":"subscribe","channel":"market.ticker","symbols":["BBCA"]}
{"op":"subscribe","channel":"market.orderbook","symbols":["BBCA"]}
```
✅ Tiap perintah dibalas `{"type":"ack",...}`.

Di terminal B, picu trade:

```bash
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $ALICE" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"SELL","price":9000,"quantity":10}' >/dev/null
curl -s -X POST $HOST/api/v1/orders -H "Authorization: Bearer $BOB" -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"BUY","price":9000,"quantity":10}' >/dev/null
```
✅ Terminal C menerima `event` pada `market.orderbook` (setiap perubahan book), lalu `market.trade` dan `market.ticker` saat trade terjadi.

### 5.2 Unsubscribe

```json
{"op":"unsubscribe","channel":"market.trade","symbols":["BBCA"]}
```
✅ Dibalas `ack`; trade berikutnya tidak lagi dikirim ke channel itu.

### 5.3 Channel `order.update` (butuh token)

```bash
websocat "ws://localhost:8080/ws?token=$ALICE"
```
```json
{"op":"subscribe","channel":"order.update"}
```
Dari terminal B buat/cancel/match order Alice. ✅ Terminal C hanya menerima update order milik Alice (bukan milik Bob). Variasi header: `websocat -H "Authorization: Bearer $ALICE" ws://localhost:8080/ws`.

### 5.4 Kasus error WebSocket

| Skenario | Pesan / aksi | Harapan |
|---|---|---|
| Token rusak | `websocat "ws://localhost:8080/ws?token=abc"` | koneksi ditolak HTTP `401` |
| Anonim minta `order.update` | `{"op":"subscribe","channel":"order.update"}` tanpa token | `{"type":"error","code":"UNAUTHENTICATED"}` |
| Symbol tidak ada | `{"op":"subscribe","channel":"market.trade","symbols":["NOPE"]}` | `INVALID_SYMBOL` |
| Channel tidak ada | `{"op":"subscribe","channel":"x","symbols":["BBCA"]}` | `INVALID_CHANNEL` |
| Tanpa symbols | `{"op":"subscribe","channel":"market.trade"}` | `INVALID_SYMBOLS` |
| Op salah | `{"op":"dance"}` | `INVALID_OP` |
| JSON rusak | `halo` | `INVALID_JSON` |
| >5 symbol | subscribe 6 symbol berbeda | `TOO_MANY_SYMBOLS` |
| Terlalu cepat | kirim >20 pesan/detik | `RATE_LIMITED`, koneksi ditutup |

Daftar 5 symbol untuk uji batas: `BBCA, BBRI, TLKM, ASII, GOTO` (hanya 5 symbol tersedia, jadi batas 5 tercapai bila satu client subscribe semuanya; `TOO_MANY_SYMBOLS` baru bisa dipicu lewat test otomatis atau jika daftar symbol diperbanyak).

### 5.5 Multi-client

Buka 3 terminal dengan `websocat` ke `/ws`, semuanya subscribe `market.trade` untuk `BBCA`. Picu satu trade. ✅ Ketiganya menerima event yang sama.

---

## 6. Uji Simulator (data realtime otomatis)

Jalankan server **tanpa** `SIM_ENABLED=false`:

```bash
JWT_SECRET=THIS_IS_SECRET_KEY_MUST_32_CHAR_LENGTH make run
```

```bash
websocat "ws://localhost:8080/ws"
{"op":"subscribe","channel":"market.ticker","symbols":["BBCA","BBRI"]}
```
✅ Event `market.ticker` mengalir terus tanpa Anda membuat order. Cek juga:

```bash
curl -s $HOST/api/v1/trades?limit=5 | jq '.data[] | {symbol,price,quantity}'
curl -s "$HOST/api/v1/orders?limit=3" -H "Authorization: Bearer $ALICE"   # tidak berisi order bot
```

Order bot memakai user `bot`. Laju default ±1.200 order/menit (`SIM_INTERVAL=500ms`). Ubah dengan `SIM_INTERVAL=100ms`.

---

## 7. Uji Mode Tanpa JWT (opsional)

```bash
AUTH_ENABLED=false SIM_ENABLED=false make run
```

```bash
curl -s -X POST $HOST/api/v1/orders -H 'X-User-ID: alice' -H 'Content-Type: application/json' \
  -d '{"symbol":"BBCA","side":"SELL","price":9000,"quantity":10}' | jq .data.user_id   # "alice"
curl -s -i -X POST $HOST/api/v1/auth/login   # 404 (route login tidak ada)
websocat "ws://localhost:8080/ws?user=alice" # order.update memakai ?user=
```

---

## 8. Test Otomatis & Kualitas Kode

```bash
make test      # semua test
make race      # dengan race detector (disarankan)
make vet
make fmt       # gofmt
```

| Test | Yang dibuktikan |
|---|---|
| `internal/domain` | matching: full/partial fill, harga maker, prioritas harga-waktu, cancel, validasi |
| `internal/auth` | login, token palsu/kedaluwarsa/algoritma lain (`none`, HS512) ditolak |
| `internal/transport/http` | JSON 404/405, request id, recovery panic, auth middleware |
| `internal/e2e` | REST + WebSocket end-to-end, JWT, **16 goroutine × 200 order tanpa race**, client lambat tidak memblokir |

Satu test spesifik:

```bash
go test -race -count=1 -run TestConcurrentSubmissionsAreConsistent -v ./internal/e2e
```

---

## 9. Troubleshooting

| Gejala | Penyebab / solusi |
|---|---|
| `jq: ... null` atau `ALICE` kosong | Login gagal. Cek username/password dan bahwa server berjalan di `$HOST` |
| Semua order `401` setelah restart | `JWT_SECRET` kosong → secret acak baru tiap start. Login ulang atau set `JWT_SECRET` |
| `address already in use` | Port 8080 terpakai. Pakai `PORT=9090` dan ubah `HOST` |
| Order selalu langsung `FILLED`/`PARTIAL` secara aneh | Simulator aktif dan bot ikut match. Pakai `SIM_ENABLED=false` |
| `503 OVERLOADED` | Antrian engine penuh. Naikkan `ENGINE_QUEUE` atau kurangi laju order |
| Client WebSocket tiba-tiba terputus | Terlalu lambat membaca (diputus otomatis) atau melebihi 20 pesan/detik. Reconnect dan ambil snapshot REST |
| Token WebSocket kedaluwarsa | Token hanya dicek saat connect. Login ulang lalu reconnect |
| Event ticker/orderbook ada yang terlewat | Disengaja: pesan ticker/orderbook boleh di-drop untuk client lambat; data terbaru tetap dikirim |

## 10. Hentikan Server

Tekan `Ctrl+C` di terminal A. Server melakukan graceful shutdown dan mencetak ringkasan:

```
level=INFO msg=bye events_dropped=0 slow_clients_kicked=0
```

