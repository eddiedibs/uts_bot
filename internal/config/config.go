package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

var (
	SAIAPage            string
	UndesiredActivities = []string{"RECURSO", "PÁGINA", "URL"}
	Username            string
	Password            string
	// CourseViewBaseURL is the Moodle course page without query string, e.g. …/course/view.php
	CourseViewBaseURL string
	// DatabaseDSN is a MySQL DSN, e.g. user:pass@tcp(127.0.0.1:3306)/uft_db?parseTime=true
	DatabaseDSN string
	// APIListenAddr is the HTTP listen address (e.g. :8080).
	APIListenAddr string
	// APIKey is the shared secret for clients (send via X-API-Key or Authorization: Bearer). Set API_KEY in .env.
	APIKey string
	// PagosCI / PagosPassword authenticate against the UFT pagos API (cedula + password).
	PagosCI       string
	PagosPassword string
	// PagosAPIBaseURL is the pagos backend base URL (trailing slash normalized).
	PagosAPIBaseURL string
	// ScrapeEnabled turns the background scrape scheduler on or off (SCRAPE_ENABLED).
	ScrapeEnabled bool
	// ScrapeInterval is how long the scheduler rests after a cycle *finishes* before starting the
	// next one (SCRAPE_INTERVAL, e.g. 30m). Measuring from the end rather than on a fixed phase
	// means cycles can never overlap no matter how long one runs. Notification staleness is
	// therefore bounded by this plus the cycle duration, not by this alone.
	ScrapeInterval time.Duration
	// ScrapeStartupDelay is how long the scheduler waits after process start before its first
	// cycle (SCRAPE_STARTUP_DELAY). It is short on purpose: long enough for migrations and the
	// database pool to settle, short enough that a fresh boot is not blind for a full interval.
	// Set it to 0 to scrape immediately on start.
	ScrapeStartupDelay time.Duration
	// ScrapeCourseRest is the pause between consecutive per-course activity crawls
	// (SCRAPE_COURSE_REST). Activities are fetched one course at a time and spaced out so the
	// crawl does not arrive at SAIA as a burst.
	ScrapeCourseRest time.Duration
	// ScrapePhaseRest is the pause between the activities phase and the grades phase
	// (SCRAPE_PHASE_REST).
	ScrapePhaseRest time.Duration
	// ScrapeActiveStartHour / ScrapeActiveEndHour bound the local-time window in which the
	// scheduler scrapes (SCRAPE_ACTIVE_HOURS, e.g. 7-23), so it does not hammer Moodle overnight.
	ScrapeActiveStartHour int
	ScrapeActiveEndHour   int
	// OutboxRetentionDays is how long delivered notification_outbox rows are kept
	// (OUTBOX_RETENTION_DAYS). Unconsumed rows are never pruned.
	OutboxRetentionDays int
)

func init() {
	// Must run before reading env: package vars are initialized before main(), so
	// godotenv in main() would run too late for these fields.
	_ = godotenv.Load()

	SAIAPage = getEnvOr("SAIA_PAGE", "https://saia.uft.edu.ve/")
	Username = os.Getenv("UTS_USERNAME")
	Password = os.Getenv("UTS_PASSWORD")
	CourseViewBaseURL = strings.TrimSuffix(getEnvOr("COURSE_VIEW_BASE_URL", "https://saia.uft.edu.ve/course/view.php"), "?")
	DatabaseDSN = os.Getenv("DATABASE_DSN")
	APIListenAddr = getEnvOr("API_LISTEN", ":8080")
	APIKey = strings.TrimSpace(os.Getenv("API_KEY"))
	PagosCI = strings.TrimSpace(os.Getenv("PAGOS_CI"))
	PagosPassword = os.Getenv("PAGOS_PASSWORD")
	PagosAPIBaseURL = strings.TrimRight(getEnvOr("PAGOS_API_BASE_URL", "https://uftapp.uft.edu.ve/"), "/") + "/"
	ScrapeEnabled = getEnvBool("SCRAPE_ENABLED", true)
	ScrapeInterval = getEnvDuration("SCRAPE_INTERVAL", 30*time.Minute)
	ScrapeStartupDelay = getEnvDurationAllowZero("SCRAPE_STARTUP_DELAY", 2*time.Minute)
	ScrapeCourseRest = getEnvDuration("SCRAPE_COURSE_REST", 2*time.Minute)
	ScrapePhaseRest = getEnvDuration("SCRAPE_PHASE_REST", 5*time.Minute)
	ScrapeActiveStartHour, ScrapeActiveEndHour = parseActiveHours(getEnvOr("SCRAPE_ACTIVE_HOURS", "7-23"))
	OutboxRetentionDays = getEnvInt("OUTBOX_RETENTION_DAYS", 14)
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		slog.Warn("invalid boolean env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return b
}

func getEnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("invalid duration env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return d
}

// getEnvDurationAllowZero is getEnvDuration for settings where zero is a meaningful value rather
// than a mistake. It stays separate because zero would be a hot loop for the rests.
func getEnvDurationAllowZero(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		slog.Warn("invalid duration env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return d
}

// parseActiveHours reads "START-END" as local-time hours. "0-24" (or any full-day range) means
// always active. Malformed input falls back to all day rather than silently disabling scraping.
func parseActiveHours(raw string) (int, int) {
	start, end, ok := strings.Cut(strings.TrimSpace(raw), "-")
	if !ok {
		slog.Warn("invalid SCRAPE_ACTIVE_HOURS, scraping all day", "value", raw)
		return 0, 24
	}
	s, errS := strconv.Atoi(strings.TrimSpace(start))
	e, errE := strconv.Atoi(strings.TrimSpace(end))
	if errS != nil || errE != nil || s < 0 || s > 24 || e < 0 || e > 24 || s >= e {
		slog.Warn("invalid SCRAPE_ACTIVE_HOURS, scraping all day", "value", raw)
		return 0, 24
	}
	return s, e
}
