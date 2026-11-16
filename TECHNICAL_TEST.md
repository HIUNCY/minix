# Golang Technical Test – Realtime Trading System (Mini Exchange)

Dokumen ini merupakan salinan terstruktur dan rapi dari spesifikasi teknis pada `YBTech Skil Test Backend Developer.pdf`.

---

## 🎯 Objective
Membangun **mini realtime trading backend** menggunakan Golang yang mampu:
1. Menyediakan **REST API**
2. Menyediakan **WebSocket** untuk data realtime
3. Menangani **event streaming** (price / trade)
4. Menangani **concurrency** dengan baik
5. Dirancang untuk asumsi **beban produksi skala menengah**

---

## ⚡ Asumsi Beban Sistem
Gunakan asumsi berikut dalam desain:
* **1.000 order per menit**
* **500 WebSocket client aktif**
* Setiap client subscribe **1 sampai 5 stock**
* Broadcast **tidak boleh blocking** meskipun ada client lambat

> [!NOTE]
> Tidak perlu melakukan load test. Namun desain harus mempertimbangkan asumsi ini dan dijelaskan di `README.md`.

---

## 📋 Requirements

### 1. REST API (HTTP)
Sediakan beberapa endpoint berikut:

#### a. Create Order
* Endpoint untuk membuat order (`BUY` / `SELL`).
* Sistem harus aman terhadap *concurrent order submission*.
* **Input minimal:**
  * `Stock Code`
  * `Side` (`BUY` / `SELL`)
  * `Price`
  * `Quantity`

#### b. Get Order List
* Mengambil daftar order yang sudah dibuat.
* Mendukung filter:
  * `Stock`
  * `Status` *(opsional)*

#### c. Get Trade History
* Menampilkan riwayat transaksi (trade) yang sudah terjadi.

#### d. Get Market Info (Snapshot)
Endpoint snapshot agar client bisa render data awal tanpa harus menunggu event WS pertama:
* **Get Ticker / Price:** Contoh: *last price*, *change*, *volume*.
* **Get Order Book:** *bid / offer depth*.
* **Get Recent Trades:** *last N trades* *(opsional)*.

> [!TIP]
> *Market snapshot* boleh bersumber dari simulasi internal atau dari public API.

---

### 2. Matching Engine (Core Logic)
Implementasikan *simple matching engine*:
1. Order `BUY` akan match dengan `SELL` jika harga sesuai.
2. Jika match:
   * Buat record **Trade**.
   * Update **Status Order**.
3. Handle:
   * Partial fill *(optional bonus)*
   * FIFO matching *(optional bonus)*

**Tambahan Wajib:**
* Matching engine harus aman terhadap **race condition**.
* Jelaskan bagaimana memastikan konsistensi data saat order masuk secara bersamaan.

---

### 3. WebSocket (Real Time)
Buat WebSocket server dengan kemampuan:

#### a. Subscription
Client bisa subscribe ke:
* `price/ticker`
* `trade`
* `order update` *(optional bonus)*

#### b. Minimal Realtime Channels (Disarankan)
Agar jelas dan mudah dites, minimal dukung channel berikut:
* `market.ticker` (harga terakhir per Stock)
* `market.trade` (stream trade per Stock)
* `market.orderbook` *(opsional bonus)* (depth bid/offer)
* `order.update` *(opsional bonus)* (status order per user / per client)

> [!IMPORTANT]
> Saat client connect, client mengirim message `subscribe`/`unsubscribe` (format bebas tapi konsisten).

#### c. Perilaku Server
* Broadcast update secara realtime.
* Mengirim data ketika:
  * Ada trade terjadi.
  * Ada perubahan harga.

#### d. Multi-Client Connection
* Tidak boleh blocking.
* Harus scalable secara concurrency.

---

### 4. Realtime Price Simulation
Buat simulasi data:
1. Harga berubah secara periodik / berdasarkan trade.
2. Generate event secara realtime.
3. Broadcast ke semua subscriber.
4. Jelaskan di `README.md` bagaimana simulasi bekerja.

---

### 5. Concurrency & Performance
1. **Teknologi yang digunakan:**
   * Goroutine
   * Channel
2. **Karakteristik Sistem:**
   * Bebas dari *race condition*
   * *Non-blocking*
   * Mampu menangani *multiple request & connection*

**Tambahan Wajib:**
1. Jelaskan potensi **bottleneck utama**.
2. Jelaskan **strategi scaling horizontal**.
3. Jelaskan cara mencegah **goroutine leak**.

---

### 6. Data Storage
Pilihan penyimpanan data:
* **in-memory** *(wajib)*
* **Redis** *(optional bonus)*
* **Database** *(optional bonus)*

---

## 🛠️ Technical Constraints
1. Gunakan **Golang**.
2. **Clean Architecture** (pemisahan layer diutamakan) *(Nilai tambah)*.
3. Kode harus **readable & maintainable**.
4. **Error handling** yang baik.

---

## 📦 Expected Output (Deliverables)
Kandidat harus mengumpulkan:

### 1. Source Code
* Git repository (public/private).
* Struktur project jelas.

### 2. README.md (Wajib Berisi)
* Cara menjalankan project.
* Desain arsitektur (singkat).
* *Flow system*.
* Asumsi yang digunakan.
* Penjelasan potensi race condition.
* Strategi broadcast *non-blocking*.
* Tiga bottleneck utama dan cara mengatasinya.

### 3. Dokumentasi API
* List endpoint.
* Cara penggunaan / contoh request & response.

### 4. Dokumentasi WebSocket
* Cara connect.
* Cara subscribe.
* Format message (bebas tapi konsisten).
* Rekomendasi tools dan cara testing WebSocket menggunakan tools tersebut.

---

## 🌐 Rekomendasi Open API / Public WebSocket (Opsional untuk Testing)
Bagian ini opsional jika ingin menggunakan data market eksternal agar simulasi terasa riil:
1. **Finnhub** — REST + WebSocket (free tier, butuh API key)
   * Realtime lintas stocks / forex / crypto.
   * Ref: https://finnhub.io/
2. **Binance (Crypto)** — Public WebSocket (tanpa API key)
   * Realtime ticker / trades / orderbook.
   * Ref: [Binance WebSocket Streams](https://developers.binance.com/docs/binance-spot-api-docs/web-socket-streams) | [Market Data Only](https://developers.binance.com/docs/binance-spot-api-docs/faqs/market_data_only)
3. **Kraken (Crypto)** — Public WebSocket (tanpa API key)
   * Realtime ticker / order book / trades.
   * Public WS: `wss://ws.kraken.com/`
   * Ref: [Kraken FAQ](https://support.kraken.com/articles/360022326871-kraken-websocket-api-frequently-asked-questions)
4. **Alpaca** — Paper trading (gratis) + WebSocket untuk order/account update (butuh API key)
   * Cocok untuk uji flow submit order -> status update.
   * Ref: [Paper Trading](https://docs.alpaca.markets/docs/paper-trading) | [WebSocket Streaming](https://docs.alpaca.markets/docs/websocket-streaming)

> **Catatan:** Jika menggunakan data crypto dari provider di atas, cukup petakan ticker (misal `BTCUSDT`) ke Stock di sistem.

---

## 🌟 Bonus Point (Nice to Have)
- [ ] Menggunakan message broker (Kafka / NATS)
- [ ] Menggunakan Redis untuk pub/sub
- [ ] Implementasi rate limiting
- [ ] Implementasi authentication (JWT)
- [ ] Logging & monitoring
- [ ] Unit test

---

## ⚖️ Evaluation Criteria
1. Arsitektur & design
2. Pemahaman concurrency (goroutine / channel)
3. WebSocket handling
4. Code quality & readability
5. Problem solving
6. Pendekatan penanganan realtime
7. Mengikuti standar internasional (state management / clean architecture) di Golang

---

## 💡 Notes
* **Tidak perlu UI / frontend**.
* Fokus pada **backend & realtime system**.
* Tidak perlu perfect matching engine (logika sederhana sudah cukup).
* Yang terpenting: **cara berpikir & struktur sistem**.

