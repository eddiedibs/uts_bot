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
	TargetPage   string
	SAIAPage     string
	LoginBtn     = "loginbtn"
	UndesiredActivities = []string{"RECURSO", "PÁGINA", "URL"}
	Username     string
	Password     string
	APIBaseURL   string
	// CourseViewBaseURL is the Moodle course page without query string, e.g. …/course/view.php
	CourseViewBaseURL string
)

func init() {
	// Must run before reading env: package vars are initialized before main(), so
	// godotenv in main() would run too late for these fields.
	_ = godotenv.Load()

	ChromeBin = getEnvOr("CHROME_BIN", "")
	BrowserDebug = envBool("BROWSER_DEBUG", false)
	TargetPage = getEnvOr("TARGET_PAGE", "https://getonbrd.com/")
	SAIAPage = getEnvOr("SAIA_PAGE", "https://saia.uft.edu.ve/")
	Username = os.Getenv("UTS_USERNAME")
	Password = os.Getenv("UTS_PASSWORD")
	APIBaseURL = getEnvOr("API_BASE_URL", "http://127.0.0.1:8000")
	CourseViewBaseURL = strings.TrimSuffix(getEnvOr("COURSE_VIEW_BASE_URL", "https://saia.uft.edu.ve/course/view.php"), "?")
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
