// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr          string
	SimEnabled    bool
	SimInterval   time.Duration
	SimSeed       uint64
	WSSendBuffer  int
	EngineQueue   int
	DBPath        string        // path to SQLite database file (default: "./data/minix.db", or ":memory:")
	ShutdownGrace time.Duration

	AuthEnabled bool
	JWTSecret   string        // empty: a random secret is generated at startup
	JWTTTL      time.Duration // access token lifetime
	AuthUsers   string        // "name:password,name:password" (demo credential store)
}

// Load reads the environment, falling back to sane defaults.
func Load() Config {
	return Config{
		AuthEnabled:   getbool("AUTH_ENABLED", true),
		JWTSecret:     getenv("JWT_SECRET", "YAKIN_BERTUMBUH_SEKURITAS_TECHNICAL_TEST"),
		JWTTTL:        getduration("JWT_TTL", time.Hour),
		AuthUsers:     getenv("AUTH_USERS", "alice:alice-pass,bob:bob-pass"),
		Addr:          ":" + getenv("PORT", "8080"),
		SimEnabled:    getbool("SIM_ENABLED", true),
		SimInterval:   getduration("SIM_INTERVAL", 500*time.Millisecond),
		SimSeed:       uint64(getint("SIM_SEED", 42)),
		WSSendBuffer:  getint("WS_SEND_BUFFER", 256),
		EngineQueue:   getint("ENGINE_QUEUE", 1024),
		DBPath:        getenv("DB_PATH", "./data/minix.db"),
		ShutdownGrace: getduration("SHUTDOWN_GRACE", 10*time.Second),
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getint(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func getbool(k string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func getduration(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
