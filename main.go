package main

import (
	"log/slog"
	"os"

	"uts_bot/internal/browser"
	"uts_bot/internal/config"
	"uts_bot/internal/jobs"
	"uts_bot/internal/saia"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// .env is loaded in internal/config init (before main runs).

	b := browser.New()
	defer b.Close()

	switch os.Getenv("BOT_MODE") {
	case "jobs":
		j := jobs.New(b)
		if err := j.GetJobPostings(); err != nil {
			slog.Error("jobs bot failed", "err", err)
			os.Exit(1)
		}
	case "saia":
		s := saia.New(b)
		if err := s.Run(config.SAIAPage); err != nil {
			slog.Error("saia bot failed", "err", err)
			os.Exit(1)
		}
	default:
		slog.Error("set BOT_MODE=jobs|saia")
		os.Exit(1)
	}
}
