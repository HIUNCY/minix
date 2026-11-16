// Package auth issues and verifies JWT access tokens (HS256).
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const issuer = "minix"

// ErrInvalidCredentials is returned by Login for an unknown user or wrong password.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrInvalidToken is returned by Verify for any token problem (malformed,
// bad signature, expired, wrong algorithm, wrong issuer, missing subject).
var ErrInvalidToken = errors.New("invalid or expired token")

// UserStore defines an external user authentication provider (e.g. SQLite database).
type UserStore interface {
	Authenticate(username, password string) (string, error)
}

// Service signs and verifies tokens and checks demo credentials.
type Service struct {
	secret    []byte
	ttl       time.Duration
	users     map[string]string // username -> password fallback
	userStore UserStore
	now       func() time.Time
}

// New creates a Service. secret must be at least 32 bytes.
func New(secret string, ttl time.Duration, users map[string]string) (*Service, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 characters")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Service{secret: []byte(secret), ttl: ttl, users: users, now: time.Now}, nil
}

// SetUserStore configures an external user authentication store.
func (s *Service) SetUserStore(store UserStore) {
	s.userStore = store
}

// Login checks the credentials and returns a signed token and its expiry.
func (s *Service) Login(username, password string) (string, time.Time, error) {
	if s.userStore != nil {
		userID, err := s.userStore.Authenticate(username, password)
		if err != nil {
			return "", time.Time{}, err
		}
		return s.Issue(userID)
	}

	want, ok := s.users[username]
	// Compare even for unknown users to keep timing uniform.
	if subtle.ConstantTimeCompare([]byte(want), []byte(password)) != 1 || !ok {
		return "", time.Time{}, ErrInvalidCredentials
	}
	return s.Issue(username)
}

// Issue signs a token for userID.
func (s *Service) Issue(userID string) (string, time.Time, error) {
	now := s.now()
	exp := now.Add(s.ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, exp, nil
}

// Verify validates a token and returns the user id (the `sub` claim).
func (s *Service) Verify(token string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || claims.Subject == "" {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}

// BearerToken extracts the token from "Authorization: Bearer <token>".
func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// WSAuthenticator returns a WebSocket connection authenticator. The token may
// be sent as a Bearer header or as the `token` query parameter (browsers
// cannot set headers on WebSocket requests). No token means an anonymous
// client that may only use public market channels; an invalid token is
// rejected.
func (s *Service) WSAuthenticator() func(*http.Request) (string, error) {
	return func(r *http.Request) (string, error) {
		tok := BearerToken(r)
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if tok == "" {
			return "", nil
		}
		return s.Verify(tok)
	}
}

// ParseUsers parses "alice:pw1,bob:pw2".
func ParseUsers(spec string) (map[string]string, error) {
	users := make(map[string]string)
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, pw, ok := strings.Cut(part, ":")
		if !ok || name == "" || pw == "" {
			return nil, fmt.Errorf("invalid user entry %q, want name:password", part)
		}
		users[name] = pw
	}
	return users, nil
}
