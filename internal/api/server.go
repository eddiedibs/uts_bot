package api

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"uts_bot/internal/config"
	"uts_bot/internal/scheduler"
)

// Run starts the HTTP server until SIGINT/SIGTERM, along with the background scrape scheduler.
func Run(db *sql.DB, apiKey string) error {
	// One lock shared by the API handlers and the scheduler: only one Moodle session at a time.
	scrapeMu := &sync.Mutex{}

	ctr := NewController(db, apiKey, scrapeMu)
	mux := http.NewServeMux()
	ctr.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:              config.APIListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sched := scheduler.Start(ctx, db, scrapeMu)
	defer sched.Stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", config.APIListenAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
