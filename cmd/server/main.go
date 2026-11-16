package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hiuncy/minix/internal/auth"
	"github.com/hiuncy/minix/internal/config"
	"github.com/hiuncy/minix/internal/domain"
	"github.com/hiuncy/minix/internal/engine"
	"github.com/hiuncy/minix/internal/infra/sqlite"
	"github.com/hiuncy/minix/internal/simulator"
	httpapi "github.com/hiuncy/minix/internal/transport/http"
	"github.com/hiuncy/minix/internal/transport/ws"
	"github.com/hiuncy/minix/internal/usecase"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func newAuth(cfg config.Config, log *slog.Logger) (*auth.Service, error) {
	secret := cfg.JWTSecret
	if secret == "" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		secret = hex.EncodeToString(b[:])
		log.Warn("JWT_SECRET not set: generated a random secret, tokens become invalid on restart")
	}
	users, err := auth.ParseUsers(cfg.AuthUsers)
	if err != nil {
		return nil, fmt.Errorf("AUTH_USERS: %w", err)
	}
	log.Info("JWT auth enabled", "users", len(users), "ttl", cfg.JWTTTL)
	return auth.New(secret, cfg.JWTTTL, users)
}

func run(log *slog.Logger) error {
	cfg := config.Load()
	ins := domain.DefaultInstruments()

	// Root context: cancelled on SIGINT/SIGTERM. Every goroutine hangs off it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Infrastructure (SQLite).
	db, err := sqlite.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open sqlite database (%s): %w", cfg.DBPath, err)
	}
	defer db.Close()
	log.Info("sqlite connected", "path", cfg.DBPath)

	orders := sqlite.NewOrderRepo(db.DB)
	trades := sqlite.NewTradeRepo(db.DB)
	tickers := sqlite.NewTickerRepo(db.DB, ins)
	books := sqlite.NewBookRepo()
	userRepo := sqlite.NewUserRepo(db.DB)

	if err := userRepo.SeedUsers(cfg.AuthUsers); err != nil {
		log.Warn("seed users error", "err", err)
	}

	hubOpts := ws.Options{SendBuffer: cfg.WSSendBuffer}
	var authSvc *auth.Service
	if cfg.AuthEnabled {
		var err error
		if authSvc, err = newAuth(cfg, log); err != nil {
			return err
		}
		authSvc.SetUserStore(userRepo)
		hubOpts.Authenticate = authSvc.WSAuthenticator()
	} else {
		log.Warn("JWT auth disabled: callers are identified by the unauthenticated X-User-ID header")
	}
	hub := ws.NewHub(ctx, ins, hubOpts, log)

	// Use cases + engine.
	projector := usecase.NewProjector(orders, trades, tickers, books, hub)
	eng := engine.New(ctx, ins, projector, cfg.EngineQueue, log)

	// Rehydrate active resting orders into order book.
	if _, err := sqlite.Rehydrate(ctx, orders, eng, log); err != nil {
		log.Warn("rehydrate orders warning", "err", err)
	}

	orderSvc := usecase.NewOrderService(eng, orders, ins)
	marketSvc := usecase.NewMarketService(tickers, books, trades, ins)

	var bg sync.WaitGroup
	bg.Add(1)
	go func() { defer bg.Done(); hub.Run() }()
	if cfg.SimEnabled {
		sim := simulator.New(orderSvc, ins, simulator.Config{Interval: cfg.SimInterval, Seed: cfg.SimSeed}, log)
		bg.Add(1)
		go func() { defer bg.Done(); sim.Run(ctx) }()
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewRouter(httpapi.Deps{Orders: orderSvc, Market: marketSvc, WS: hub, Log: log, Auth: authSvc}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "simulator", cfg.SimEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		stop()
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Graceful shutdown: stop intake, then let background goroutines drain.
	shCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	bg.Wait()  // simulator + hub loop (ctx is already cancelled)
	eng.Wait() // matching workers
	hub.Wait() // websocket connection handlers (hijacked, not tracked by srv)
	dropped, kicked := hub.Stats()
	log.Info("bye", "events_dropped", dropped, "slow_clients_kicked", kicked)
	return nil
}
