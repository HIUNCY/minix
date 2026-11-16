package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hiuncy/minix/internal/auth"
)

const (
	requestIDKey = "request_id"
	userIDKey    = "user_id"
)

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// recovery turns a panic into a 500 JSON error. After a WebSocket upgrade the
// connection is already hijacked, so the panic is only logged.
func recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic in handler", "panic", rec, "path", c.Request.URL.Path)
				if !c.Writer.Written() {
					respondError(c, http.StatusInternalServerError, "INTERNAL", "internal server error")
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}

func accessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "dur", time.Since(start).Round(time.Microsecond),
			"req_id", c.GetString(requestIDKey))
	}
}

// authRequired verifies the Bearer token and stores the user id (the `sub`
// claim) in the context.
func authRequired(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := auth.BearerToken(c.Request)
		if tok == "" {
			unauthorized(c, "missing bearer token")
			c.Abort()
			return
		}
		uid, err := svc.Verify(tok)
		if err != nil {
			unauthorized(c, err.Error())
			c.Abort()
			return
		}
		c.Set(userIDKey, uid)
		c.Next()
	}
}
