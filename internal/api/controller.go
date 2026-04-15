package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"uts_bot/internal/apiauth"
	"uts_bot/internal/config"
	"uts_bot/internal/moodlehttp"
	"uts_bot/internal/saia"
	"uts_bot/internal/store"
)

// Controller holds API dependencies and registers all HTTP handlers for the project.
type Controller struct {
	db     *sql.DB
	apiKey string
	mu     sync.Mutex
}

// NewController builds the API controller used to mount every route.
func NewController(db *sql.DB, apiKey string) *Controller {
	return &Controller{db: db, apiKey: apiKey}
}

// RegisterRoutes attaches all API handlers to mux.
func (c *Controller) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/courses", c.Courses)
	mux.HandleFunc("/api/v1/activities", c.Activities)
}

func (c *Controller) requireAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if !apiauth.ValidAPIKey(r, c.apiKey) {
		slog.Warn("api key rejected")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// searchModeAuto preserves legacy behavior (courses only): fill DB when empty, no-op when already seeded.
const searchModeAuto = "auto"

// parseSearchQuery reads ?search=db|page. emptyDefault is used when the query param is omitted (e.g. "page" or searchModeAuto).
func parseSearchQuery(r *http.Request, emptyDefault string) (string, error) {
	v := strings.TrimSpace(r.URL.Query().Get("search"))
	if v == "" {
		return emptyDefault, nil
	}
	switch strings.ToLower(v) {
	case "db", "page":
		return strings.ToLower(v), nil
	default:
		return "", fmt.Errorf("invalid search=%q: use db or page", v)
	}
}

// parseOptionalCourseViewID reads ?course_id= (Moodle course id from course/view.php?id=). Omitted means all courses / no filter.
func parseOptionalCourseViewID(r *http.Request) (*int, error) {
	v := strings.TrimSpace(r.URL.Query().Get("course_id"))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseUint(v, 10, 31)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("invalid course_id=%q: must be a positive integer", v)
	}
	x := int(n)
	return &x, nil
}

// runScraper logs into SAIA and walks courses/activities (Excel export side effects today).
func (c *Controller) runScraper(ctx context.Context) error {
	cl := moodlehttp.New()
	s := saia.New(cl)
	return s.Run(ctx, config.SAIAPage, nil)
}

// runActivitiesScraper runs Run then getSAIAActivities on the same HTTP session.
// onlyCourseViewID limits the crawl to one course when non-nil (search=page only).
func (c *Controller) runActivitiesScraper(ctx context.Context, onlyCourseViewID *int) error {
	cl := moodlehttp.New()
	s := saia.New(cl)
	s.DB = c.db
	return s.RunThenGetSAIAActivities(ctx, config.SAIAPage, onlyCourseViewID)
}

// Courses returns stored courses. Query: ?search=db (DB only), ?search=page (run scraper, sync static course rows, return list), or omit for legacy auto (fill when empty).
func (c *Controller) Courses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.requireAPIKey(w, r) {
		return
	}

	mode, err := parseSearchQuery(r, searchModeAuto)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	courses, err := store.ListCourses(ctx, c.db)
	if err != nil {
		slog.Error("list courses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch mode {
	case "db":
		writeCoursesJSON(w, courses)
		return
	case "page":
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.runScraper(ctx); err != nil {
			slog.Error("saia sync failed", "err", err)
			http.Error(w, "course sync failed", http.StatusBadGateway)
			return
		}
		tx, err := c.db.BeginTx(ctx, nil)
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
		courses, err = store.ListCourses(ctx, c.db)
		if err != nil {
			slog.Error("list courses after page sync", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeCoursesJSON(w, courses)
		return
	}

	// searchModeAuto — same as before: fast path when rows exist; otherwise scrape + seed once.
	if len(courses) > 0 {
		writeCoursesJSON(w, courses)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	courses, err = store.ListCourses(ctx, c.db)
	if err != nil {
		slog.Error("list courses after lock", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(courses) > 0 {
		writeCoursesJSON(w, courses)
		return
	}

	if err := c.runScraper(ctx); err != nil {
		slog.Error("saia sync failed", "err", err)
		http.Error(w, "course sync failed", http.StatusBadGateway)
		return
	}

	tx, err := c.db.BeginTx(ctx, nil)
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

	courses, err = store.ListCourses(ctx, c.db)
	if err != nil {
		slog.Error("list courses after seed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeCoursesJSON(w, courses)
}

// Activities returns activities from the DB. Query: ?search=db|page, optional ?course_id= (Moodle course/view id) to filter or limit sync to one course.
func (c *Controller) Activities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.requireAPIKey(w, r) {
		return
	}

	mode, err := parseSearchQuery(r, "page")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	courseViewID, err := parseOptionalCourseViewID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	c.mu.Lock()
	defer c.mu.Unlock()

	if mode == "page" {
		if err := c.runActivitiesScraper(ctx, courseViewID); err != nil {
			slog.Error("activities scraper failed", "err", err)
			http.Error(w, "activity sync failed", http.StatusBadGateway)
			return
		}
	}
	activities, err := store.ListActivities(ctx, c.db, courseViewID)
	if err != nil {
		slog.Error("list activities", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeActivitiesJSON(w, activities)
}

func writeCoursesJSON(w http.ResponseWriter, courses []store.Course) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{"courses": courses})
}

func writeActivitiesJSON(w http.ResponseWriter, activities []store.Activity) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{"activities": activities})
}
