package main

import (
	"database/sql"
	"log/slog"
	"os"

	"uts_bot/internal/api"
	"uts_bot/internal/config"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

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
