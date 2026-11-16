// Package httpapi exposes the REST API (Gin).
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/hiuncy/minix/internal/auth"
	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/usecase"
)

// Deps are the collaborators of the HTTP layer.
type Deps struct {
	Orders *usecase.OrderService
	Market *usecase.MarketService
	WS     http.Handler
	Log    *slog.Logger
	// Auth enables JWT authentication. When nil, the legacy unauthenticated
	// mode is used and the caller is identified by the X-User-ID header.
	Auth *auth.Service
}

type handler struct{ Deps }

// NewRouter builds the HTTP handler with all routes and middleware.
//
// Handlers must pass c.Request.Context() (never *gin.Context) to services and
// goroutines: gin contexts are pooled and reused after the handler returns.
func NewRouter(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	h := handler{d}

	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.HandleMethodNotAllowed = true
	r.NoRoute(func(c *gin.Context) {
		respondError(c, http.StatusNotFound, "NOT_FOUND", "route not found")
	})
	r.NoMethod(func(c *gin.Context) {
		respondError(c, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	})
	r.Use(requestID(), recovery(d.Log), accessLog(d.Log))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	if d.WS != nil {
		// Authentication of WebSocket clients is done by the hub itself
		// (anonymous clients may use public market channels).
		r.GET("/ws", gin.WrapH(d.WS))
	}

	v1 := r.Group("/api/v1")
	if d.Auth != nil {
		v1.POST("/auth/login", h.login)
	}

	orders := v1.Group("/orders")
	if d.Auth != nil {
		orders.Use(authRequired(d.Auth))
	}
	orders.POST("", h.createOrder)
	orders.GET("", h.listOrders)
	orders.GET("/:id", h.getOrder)
	orders.DELETE("/:id", h.cancelOrder)

	v1.GET("/trades", h.listTrades)
	m := v1.Group("/market")
	m.GET("/tickers", h.listTickers)
	m.GET("/tickers/:symbol", h.getTicker)
	m.GET("/orderbook/:symbol", h.getOrderBook)
	m.GET("/trades/:symbol", h.recentTrades)
	return r
}

const userHeader = "X-User-ID"

// userID returns the acting user. With auth enabled it only trusts the
// verified token; the X-User-ID header is ignored.
func (h handler) userID(c *gin.Context) string {
	if h.Auth != nil {
		return c.GetString(userIDKey)
	}
	if u := strings.TrimSpace(c.GetHeader(userHeader)); u != "" {
		return u
	}
	return "anonymous"
}

// listUser is the user whose orders are listed: always the caller with JWT;
// in legacy mode only when the caller identifies itself.
func (h handler) listUser(c *gin.Context) string {
	if h.Auth != nil {
		return h.userID(c)
	}
	return strings.TrimSpace(c.GetHeader(userHeader))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h handler) login(c *gin.Context) {
	var req loginRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	tok, exp, err := h.Auth.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			unauthorized(c, err.Error())
			return
		}
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"access_token": tok, "token_type": "Bearer", "expires_at": exp,
	}})
}

type createOrderRequest struct {
	Symbol   string `json:"symbol"`
	Side     string `json:"side"`
	Price    int64  `json:"price"`
	Quantity int64  `json:"quantity"`
}

func (h handler) createOrder(c *gin.Context) {
	var req createOrderRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	o, err := h.Orders.Create(c.Request.Context(), usecase.CreateOrderInput{
		UserID: h.userID(c), Symbol: req.Symbol, Side: req.Side, Price: req.Price, Quantity: req.Quantity,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": o})
}

func (h handler) listOrders(c *gin.Context) {
	f := usecase.OrderFilter{
		UserID: h.listUser(c),
		Symbol: c.Query("symbol"),
		Limit:  atoi(c.Query("limit")),
		Offset: atoi(c.Query("offset")),
	}
	if s := strings.ToUpper(c.Query("status")); s != "" {
		f.Status = domain.Status(s)
		if !f.Status.Valid() {
			writeError(c, badRequest("status must be one of OPEN, PARTIAL, FILLED, CANCELLED"))
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": h.Orders.List(f)})
}

func (h handler) getOrder(c *gin.Context) {
	o, err := h.Orders.Get(c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": o})
}

func (h handler) cancelOrder(c *gin.Context) {
	o, err := h.Orders.Cancel(c.Request.Context(), h.userID(c), c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": o})
}

func (h handler) listTrades(c *gin.Context) {
	trades, err := h.Market.Trades(c.Query("symbol"), atoi(c.Query("limit")))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trades})
}

func (h handler) listTickers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": h.Market.Tickers()})
}

func (h handler) getTicker(c *gin.Context) {
	tk, err := h.Market.Ticker(c.Param("symbol"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": tk})
}

func (h handler) getOrderBook(c *gin.Context) {
	snap, err := h.Market.OrderBook(c.Param("symbol"), atoi(c.Query("depth")))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": snap})
}

func (h handler) recentTrades(c *gin.Context) {
	trades, err := h.Market.Trades(c.Param("symbol"), atoi(c.Query("limit")))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": trades})
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
