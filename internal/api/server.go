package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"uts_bot/internal/apiauth"
	"uts_bot/internal/browser"
	"uts_bot/internal/config"
	"uts_bot/internal/saia"
	"uts_bot/internal/store"
)

// Handler serves API-key–protected HTTP endpoints.
type Handler struct {
	db     *sql.DB
	apiKey string
	mu     sync.Mutex
}

// Run starts the HTTP server until SIGINT/SIGTERM.
func Run(db *sql.DB, apiKey string) error {
	h := &Handler{db: db, apiKey: apiKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/courses", h.handleCourses)

	srv := &http.Server{
		Addr:              config.APIListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func (h *Handler) handleCourses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !apiauth.ValidAPIKey(r, h.apiKey) {
		slog.Warn("api key rejected")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	courses, err := store.ListCourses(ctx, h.db)
	if err != nil {
		slog.Error("list courses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(courses) > 0 {
		writeCoursesJSON(w, courses)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	courses, err = store.ListCourses(ctx, h.db)
	if err != nil {
		slog.Error("list courses after lock", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(courses) > 0 {
		writeCoursesJSON(w, courses)
		return
	}

	b := browser.New()
	defer b.Close()

	if err := saia.New(b).Run(config.SAIAPage); err != nil {
		slog.Error("saia sync failed", "err", err)
		http.Error(w, "course sync failed", http.StatusBadGateway)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("begin tx", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if err := store.SeedCoursesFromStatic(ctx, tx); err != nil {
		slog.Error("seed courses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("commit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	courses, err = store.ListCourses(ctx, h.db)
	if err != nil {
		slog.Error("list courses after seed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeCoursesJSON(w, courses)
}

func writeCoursesJSON(w http.ResponseWriter, courses []store.Course) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{"courses": courses})
}
