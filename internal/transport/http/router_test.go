package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hiuncy/minix/internal/auth"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newAuth(t *testing.T) *auth.Service {
	t.Helper()
	s, err := auth.New("0123456789abcdef0123456789abcdef", time.Minute, map[string]string{"alice": "pw"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct{ Error struct{ Code string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body is not a JSON error envelope: %q", rec.Body.String())
	}
	return b.Error.Code
}

func TestNotFoundAndMethodNotAllowedAreJSON(t *testing.T) {
	h := NewRouter(Deps{Log: quietLog()})

	rec := do(h, "GET", "/nope", nil)
	if rec.Code != 404 || errCode(t, rec) != "NOT_FOUND" {
		t.Errorf("404: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", "/healthz", nil)
	if rec.Code != 405 || errCode(t, rec) != "METHOD_NOT_ALLOWED" {
		t.Errorf("405: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Allow") == "" {
		t.Error("405 response should carry an Allow header")
	}
}

func TestTrailingSlashIsNotRedirected(t *testing.T) {
	h := NewRouter(Deps{Log: quietLog()})
	rec := do(h, "GET", "/healthz/", nil)
	if rec.Code != 404 {
		t.Errorf("status %d, want 404 (no redirect)", rec.Code)
	}
}

func TestRequestID(t *testing.T) {
	h := NewRouter(Deps{Log: quietLog()})
	if id := do(h, "GET", "/healthz", nil).Header().Get("X-Request-ID"); len(id) != 16 {
		t.Errorf("generated id = %q", id)
	}
	if id := do(h, "GET", "/healthz", map[string]string{"X-Request-ID": "abc"}).Header().Get("X-Request-ID"); id != "abc" {
		t.Errorf("propagated id = %q", id)
	}
}

func TestRecoveryReturnsJSON500(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(requestID(), recovery(quietLog()))
	r.GET("/boom", func(*gin.Context) { panic("boom") })

	rec := do(r, "GET", "/boom", nil)
	if rec.Code != 500 || errCode(t, rec) != "INTERNAL" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic message must not leak to the client")
	}
}

func TestAuthMiddlewareRejections(t *testing.T) {
	svc := newAuth(t)
	h := NewRouter(Deps{Log: quietLog(), Auth: svc})

	for name, hdr := range map[string]map[string]string{
		"no auth":        nil,
		"garbage bearer": {"Authorization": "Bearer nope"},
		"basic scheme":   {"Authorization": "Basic YWxpY2U6cHc="},
		"legacy header":  {"X-User-ID": "alice"},
	} {
		rec := do(h, "GET", "/api/v1/orders", hdr)
		if rec.Code != 401 || errCode(t, rec) != "UNAUTHORIZED" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", name)
		}
	}
}

func TestLoginRouteOnlyExistsWithAuth(t *testing.T) {
	if rec := do(NewRouter(Deps{Log: quietLog()}), "POST", "/api/v1/auth/login", nil); rec.Code != 404 {
		t.Errorf("login without auth enabled: %d", rec.Code)
	}
}
