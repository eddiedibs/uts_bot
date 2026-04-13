package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"

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

func main() {
	base := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(denyWarnHandler{inner: base}))

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
	if err := api.Run(db, config.APIKey); err != nil {
		slog.Error("api server", "err", err)
		os.Exit(1)
	}
}
