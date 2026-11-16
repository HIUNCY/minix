package sqlite

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/hiuncy/minix/internal/auth"
)

// UserRepo manages user accounts and authentication using SQLite and bcrypt.
type UserRepo struct {
	db *sql.DB
}

// NewUserRepo creates a new UserRepo.
func NewUserRepo(db *sql.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Authenticate verifies username and password, returning userID or an error.
func (r *UserRepo) Authenticate(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", auth.ErrInvalidCredentials
	}

	var id, hash string
	err := r.db.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, username).Scan(&id, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", auth.ErrInvalidCredentials
		}
		return "", fmt.Errorf("query user %s: %w", username, err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", auth.ErrInvalidCredentials
	}

	return id, nil
}

// CreateUser registers a new user with a hashed password.
func (r *UserRepo) CreateUser(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", errors.New("username and password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	var b [6]byte
	_, _ = rand.Read(b[:])
	id := username // Keep username as ID for simple identity matching or prefix with USR-

	query := `INSERT INTO users (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)`
	_, err = r.db.Exec(query, id, username, string(hash), time.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("insert user: %w", err)
	}
	return id, nil
}

// SeedUsers ensures users from the spec (e.g. "alice:alice-pass,bob:bob-pass") exist.
func (r *UserRepo) SeedUsers(spec string) error {
	users, err := auth.ParseUsers(spec)
	if err != nil {
		return err
	}

	for username, password := range users {
		var exists int
		_ = r.db.QueryRow(`SELECT 1 FROM users WHERE username = ?`, username).Scan(&exists)
		if exists == 0 {
			if _, err := r.CreateUser(username, password); err != nil {
				return fmt.Errorf("seed user %s: %w", username, err)
			}
		}
	}
	return nil
}
