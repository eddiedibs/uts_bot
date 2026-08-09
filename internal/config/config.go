package config

import (
	"os"
	"strings"

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
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
