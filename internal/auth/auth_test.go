package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "0123456789abcdef0123456789abcdef"

func newSvc(t *testing.T) *Service {
	t.Helper()
	s, err := New(secret, time.Minute, map[string]string{"alice": "pw"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginAndVerify(t *testing.T) {
	s := newSvc(t)
	tok, _, err := s.Login("alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.Verify(tok); err != nil || u != "alice" {
		t.Fatalf("verify = %q, %v", u, err)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	s := newSvc(t)
	for _, c := range [][2]string{{"alice", "nope"}, {"mallory", "pw"}, {"mallory", ""}, {"", ""}} {
		if _, _, err := s.Login(c[0], c[1]); err != ErrInvalidCredentials {
			t.Errorf("%v: err = %v", c, err)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	s := newSvc(t)
	good, _, _ := s.Issue("alice")

	sign := func(m jwt.SigningMethod, key any, c jwt.RegisteredClaims) string {
		str, err := jwt.NewWithClaims(m, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return str
	}
	future := jwt.NewNumericDate(time.Now().Add(time.Hour))
	past := jwt.NewNumericDate(time.Now().Add(-time.Hour))

	cases := map[string]string{
		"garbage":      "not-a-token",
		"empty":        "",
		"tampered":     good[:len(good)-2] + "xx",
		"wrong secret": sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-123"), jwt.RegisteredClaims{Issuer: issuer, Subject: "alice", ExpiresAt: future}),
		"expired":      sign(jwt.SigningMethodHS256, []byte(secret), jwt.RegisteredClaims{Issuer: issuer, Subject: "alice", ExpiresAt: past}),
		"no expiry":    sign(jwt.SigningMethodHS256, []byte(secret), jwt.RegisteredClaims{Issuer: issuer, Subject: "alice"}),
		"wrong issuer": sign(jwt.SigningMethodHS256, []byte(secret), jwt.RegisteredClaims{Issuer: "evil", Subject: "alice", ExpiresAt: future}),
		"no subject":   sign(jwt.SigningMethodHS256, []byte(secret), jwt.RegisteredClaims{Issuer: issuer, ExpiresAt: future}),
		"alg HS512":    sign(jwt.SigningMethodHS512, []byte(secret), jwt.RegisteredClaims{Issuer: issuer, Subject: "alice", ExpiresAt: future}),
		"alg none":     sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, jwt.RegisteredClaims{Issuer: issuer, Subject: "alice", ExpiresAt: future}),
	}
	for name, tok := range cases {
		if _, err := s.Verify(tok); err != ErrInvalidToken {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestNewRequiresStrongSecret(t *testing.T) {
	if _, err := New("short", time.Minute, nil); err == nil {
		t.Fatal("expected error for short secret")
	}
}

func TestParseUsers(t *testing.T) {
	u, err := ParseUsers("alice:a1, bob:b:2")
	if err != nil || u["alice"] != "a1" || u["bob"] != "b:2" {
		t.Fatalf("%v %v", u, err)
	}
	if _, err := ParseUsers("broken"); err == nil {
		t.Fatal("expected error")
	}
}
