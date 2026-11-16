package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hiuncy/minix/internal/domain"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type httpError struct {
	status int
	apiError
}

func (e httpError) Error() string { return e.Message }

func badRequest(msg string) error {
	return httpError{http.StatusBadRequest, apiError{"BAD_REQUEST", msg}}
}

// respondError writes the standard error envelope.
func respondError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": apiError{code, msg}})
}

func unauthorized(c *gin.Context, msg string) {
	c.Header("WWW-Authenticate", `Bearer realm="minix"`)
	respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", msg)
}

// decodeJSON reads a size-limited JSON body and rejects unknown fields.
func decodeJSON(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

// writeError maps domain errors to HTTP status codes.
func writeError(c *gin.Context, err error) {
	var he httpError
	status, code := http.StatusInternalServerError, "INTERNAL"
	msg := "internal server error"
	switch {
	case errors.As(err, &he):
		status, code, msg = he.status, he.Code, he.Message
	case errors.Is(err, domain.ErrInvalidOrder):
		status, code, msg = http.StatusBadRequest, "INVALID_ORDER", err.Error()
	case errors.Is(err, domain.ErrUnknownSymbol):
		status, code, msg = http.StatusNotFound, "UNKNOWN_SYMBOL", err.Error()
	case errors.Is(err, domain.ErrOrderNotFound):
		status, code, msg = http.StatusNotFound, "ORDER_NOT_FOUND", err.Error()
	case errors.Is(err, domain.ErrForbidden):
		status, code, msg = http.StatusForbidden, "FORBIDDEN", "order belongs to another user"
	case errors.Is(err, domain.ErrNotCancelable):
		status, code, msg = http.StatusConflict, "NOT_CANCELABLE", err.Error()
	case errors.Is(err, domain.ErrOverloaded):
		status, code, msg = http.StatusServiceUnavailable, "OVERLOADED", err.Error()
	case errors.Is(err, domain.ErrShuttingDown):
		status, code, msg = http.StatusServiceUnavailable, "SHUTTING_DOWN", err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, code, msg = http.StatusGatewayTimeout, "TIMEOUT", "request timed out"
	}
	if status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "1")
	}
	respondError(c, status, code, msg)
}
