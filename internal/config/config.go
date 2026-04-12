package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

var (
	ChromeDriverDir = chromeDriverDir()
	// ChromeBin: path to Chrome/Chromium (e.g. /usr/bin/chromium). Empty uses chromedp default (google-chrome on Linux).
	ChromeBin string
	// BrowserDebug: true = visible Chrome window; false/unset = headless (chromedp default).
	BrowserDebug bool
	// ChromeNoSandbox adds --no-sandbox and --disable-dev-shm-usage (needed for Chromium in Docker).
	ChromeNoSandbox bool
	SAIAPage     string
	LoginBtn     = "loginbtn"
	UndesiredActivities = []string{"RECURSO", "PÁGINA", "URL"}
	Username     string
	Password     string
	// CourseViewBaseURL is the Moodle course page without query string, e.g. …/course/view.php
	CourseViewBaseURL string
	// DatabaseDSN is a MySQL DSN, e.g. user:pass@tcp(127.0.0.1:3306)/uft_db?parseTime=true
	DatabaseDSN string
	// APIListenAddr is the HTTP listen address (e.g. :8080).
	APIListenAddr string
	// APIKey is the shared secret for clients (send via X-API-Key or Authorization: Bearer). Set API_KEY in .env.
	APIKey string
)

func init() {
	// Must run before reading env: package vars are initialized before main(), so
	// godotenv in main() would run too late for these fields.
	_ = godotenv.Load()

	ChromeBin = getEnvOr("CHROME_BIN", "")
	BrowserDebug = envBool("BROWSER_DEBUG", false)
	ChromeNoSandbox = envBool("CHROME_NO_SANDBOX", false)
	SAIAPage = getEnvOr("SAIA_PAGE", "https://saia.uft.edu.ve/")
	Username = os.Getenv("UTS_USERNAME")
	Password = os.Getenv("UTS_PASSWORD")
	CourseViewBaseURL = strings.TrimSuffix(getEnvOr("COURSE_VIEW_BASE_URL", "https://saia.uft.edu.ve/course/view.php"), "?")
	DatabaseDSN = os.Getenv("DATABASE_DSN")
	APIListenAddr = getEnvOr("API_LISTEN", ":8080")
	APIKey = strings.TrimSpace(os.Getenv("API_KEY"))
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func chromeDriverDir() string {
	_, file, _, _ := runtime.Caller(0)
	// internal/config/config.go → project root
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(root, "chromedriver")
}
