package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
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

// runScraper logs into SAIA and walks courses/activities (Excel export side effects today).
func (c *Controller) runScraper(ctx context.Context) error {
	cl := moodlehttp.New()
	s := saia.New(cl)
	return s.Run(ctx, config.SAIAPage)
}

// runActivitiesScraper runs Run then getSAIAActivities on the same HTTP session.
func (c *Controller) runActivitiesScraper(ctx context.Context) error {
	cl := moodlehttp.New()
	s := saia.New(cl)
	s.DB = c.db
	return s.RunThenGetSAIAActivities(ctx, config.SAIAPage)
}

// Courses returns stored courses, seeding from SAIA when the table is empty.
func (c *Controller) Courses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.requireAPIKey(w, r) {
		return
	}

	ctx := r.Context()
	courses, err := store.ListCourses(ctx, c.db)
	if err != nil {
		slog.Error("list courses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
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

// Activities runs the SAIA scraper, upserts activity rows, then returns all activities from the DB as JSON.
func (c *Controller) Activities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.requireAPIKey(w, r) {
		return
	}

	ctx := r.Context()
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.runActivitiesScraper(ctx); err != nil {
		slog.Error("activities scraper failed", "err", err)
		http.Error(w, "activity sync failed", http.StatusBadGateway)
		return
	}
	activities, err := store.ListActivities(ctx, c.db)
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
