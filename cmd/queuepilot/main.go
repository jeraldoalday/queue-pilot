// Command queuepilot runs the QueuePilot HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jeraldoalday/queuepilot/internal/config"
	"github.com/jeraldoalday/queuepilot/internal/db"
	"github.com/jeraldoalday/queuepilot/internal/platform/clock"
	"github.com/jeraldoalday/queuepilot/internal/platform/logger"
	"github.com/jeraldoalday/queuepilot/internal/session"
	"github.com/jeraldoalday/queuepilot/internal/web"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "queuepilot:", err)
		os.Exit(1)
	}
}

// dispatch routes the command line to a subcommand. With no arguments the
// binary starts the server, which is the overwhelmingly common case and
// what every deployment target invokes.
//
// An unrecognised argument is an error rather than being ignored: the
// Makefile's `seed` target used to fall through to starting the server,
// so `make seed` silently served HTTP and appeared to hang instead of
// seeding anything.
func dispatch(args []string) error {
	if len(args) == 0 {
		return run()
	}

	switch args[0] {
	case "serve":
		return run()
	case "seed":
		return seed(args[1:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `queuepilot - appointment booking and walk-in queue management

usage:
  queuepilot [serve]   start the HTTP server (default)
  queuepilot seed      populate the database with a demo organization
  queuepilot help      show this message
`)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Expired sessions are ignored at lookup, so nothing about
	// correctness depends on removing their rows — but nothing removed
	// them either, and a long-lived instance accumulates one dead row per
	// login forever. The sweeper runs for the life of the process and
	// stops with it.
	sessions := session.NewStore(pool, clock.Real{})
	go session.NewSweeper(sessions, log).Run(ctx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           web.NewServer(cfg, log, pool).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server starting", "addr", srv.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}
	log.Info("server stopped")
	return nil
}
