package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"

	"uts_bot/internal/api"
	"uts_bot/internal/config"

	_ "github.com/go-sql-driver/mysql"
)

// denyWarnHandler wraps inner and suppresses slog.LevelWarn (keeps Info and Error).
type denyWarnHandler struct{ inner slog.Handler }

func (d denyWarnHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level == slog.LevelWarn {
		return false
	}
	return d.inner.Enabled(ctx, level)
}

func (d denyWarnHandler) Handle(ctx context.Context, r slog.Record) error {
	return d.inner.Handle(ctx, r)
}

func (d denyWarnHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return denyWarnHandler{inner: d.inner.WithAttrs(attrs)}
}

func (d denyWarnHandler) WithGroup(name string) slog.Handler {
	return denyWarnHandler{inner: d.inner.WithGroup(name)}
}

// multiHandler fans log records out to multiple handlers.
type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r)
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make(multiHandler, len(m))
	for i, h := range m {
		hs[i] = h.WithAttrs(attrs)
	}
	return hs
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	hs := make(multiHandler, len(m))
	for i, h := range m {
		hs[i] = h.WithGroup(name)
	}
	return hs
}

func main() {
	stderrBase := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	stderrHandler := denyWarnHandler{inner: stderrBase}

	logFile, fileErr := os.OpenFile("uts_bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if fileErr != nil {
		slog.SetDefault(slog.New(stderrHandler))
		slog.Error("open log file", "err", fileErr)
	} else {
		defer logFile.Close()
		fileHandler := slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(multiHandler{stderrHandler, fileHandler}))
	}

	// .env is loaded in internal/config init (before main runs).

	if config.DatabaseDSN == "" {
		slog.Error("set DATABASE_DSN (MySQL DSN)")
		os.Exit(1)
	}
	if config.APIKey == "" {
		slog.Error("set API_KEY (shared secret for clients)")
		os.Exit(1)
	}
	db, err := sql.Open("mysql", config.DatabaseDSN)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		slog.Error("database ping", "err", err)
		os.Exit(1)
	}
	// Long Moodle crawls can go minutes without touching the DB while connections sit idle in the
	// pool. MySQL (or proxies) then close those sessions (wait_timeout / EOF); the next query can get
	// "invalid connection". Evict idle conns from the pool before the server does.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(90 * time.Second)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := api.Run(db, config.APIKey); err != nil {
		slog.Error("api server", "err", err)
		os.Exit(1)
	}
}
