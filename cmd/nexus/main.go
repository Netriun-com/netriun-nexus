// SPDX-License-Identifier: AGPL-3.0-only

// Command nexus starts the Netriun Nexus service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/app"
	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// Production handlers arrive with T12; unknown kinds stay queued meanwhile.
	return runService(ctx, cfg, worker.Registry{})
}

func runService(parent context.Context, cfg config, handlers worker.Registry) error {
	ctx, stop := context.WithCancel(parent)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var db *pgxpool.Pool
	var handler http.Handler
	if cfg.role == "worker" {
		if os.Getenv("DATABASE_URL") == "" {
			return errors.New("DATABASE_URL is required")
		}
		var err error
		db, err = pgxpool.New(startup, os.Getenv("DATABASE_URL"))
		if err != nil {
			return err
		}
		defer db.Close()
		if err = db.Ping(startup); err != nil {
			return err
		}
		if err = app.Migrate(startup, db); err != nil {
			return err
		}
	} else {
		a, err := app.New(startup)
		if err != nil {
			return err
		}
		defer a.Close()
		db, handler = a.DB, a.Handler()
	}
	cancel()
	var runner *worker.Worker
	var workerDone chan error
	if cfg.role != "serve" {
		queue, err := jobs.New(db, map[string]int{"account:*": cfg.accountLimit, "provider:*": cfg.providerLimit})
		if err != nil {
			return err
		}
		runner, err = worker.New(queue, handlers, cfg.worker)
		if err != nil {
			return err
		}
		if cfg.role == "worker" {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !runner.Alive() {
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte(`{"status":"stopping"}`))
					return
				}
				w.Write([]byte(`{"status":"ok"}`))
			})
			handler = mux
		}
	}
	listener, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return err
	}
	defer listener.Close()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	if runner != nil {
		workerDone = make(chan error, 1)
		go func() { workerDone <- runner.Run(ctx) }()
	}
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("Netriun Nexus listening", "address", listener.Addr(), "role", cfg.role)
		serverErr <- srv.Serve(listener)
	}()
	workerFinished := false
	select {
	case <-ctx.Done():
	case err = <-serverErr:
	case err = <-workerDone:
		workerFinished = true
	}
	stop()
	shutdown, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	if shutdownErr := srv.Shutdown(shutdown); shutdownErr != nil {
		srv.Close()
	}
	if workerDone != nil && !workerFinished {
		<-workerDone
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
