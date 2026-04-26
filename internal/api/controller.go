package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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
	mux.HandleFunc("GET /api/v1/courses/{courseViewID}/attachments", c.CourseAttachmentList)
	mux.HandleFunc("GET /api/v1/attachments/content", c.AttachmentContent)
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

// scraperSyncMaxWall caps one Moodle sync. Keep below http.Server WriteTimeout (see internal/api/server.go).
const scraperSyncMaxWall = 14 * time.Minute

// detachedScrapeContext returns a context that is not cancelled when the HTTP client disconnects,
// so long syncs are not interrupted by proxy/client idle timeouts. It still respects a max wall
// clock and carries request values from parent (for any future tracing).
func detachedScrapeContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	return context.WithTimeout(base, scraperSyncMaxWall)
}

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
	slog.Info("endpoint consulted", "endpoint", "/api/v1/courses", "remote_addr", r.RemoteAddr, "search", r.URL.Query().Get("search"))

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
		scrapeCtx, cancel := detachedScrapeContext(ctx)
		defer cancel()
		if err := c.runScraper(scrapeCtx); err != nil {
			slog.Error("saia sync failed", "err", err)
			http.Error(w, "course sync failed", http.StatusBadGateway)
			return
		}
		tx, err := c.db.BeginTx(scrapeCtx, nil)
		if err != nil {
			slog.Error("begin tx", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()
		if err := store.SeedCoursesFromStatic(scrapeCtx, tx); err != nil {
			slog.Error("seed courses", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			slog.Error("commit", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		courses, err = store.ListCourses(scrapeCtx, c.db)
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

	scrapeCtx, cancel := detachedScrapeContext(ctx)
	defer cancel()
	if err := c.runScraper(scrapeCtx); err != nil {
		slog.Error("saia sync failed", "err", err)
		http.Error(w, "course sync failed", http.StatusBadGateway)
		return
	}

	tx, err := c.db.BeginTx(scrapeCtx, nil)
	if err != nil {
		slog.Error("begin tx", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if err := store.SeedCoursesFromStatic(scrapeCtx, tx); err != nil {
		slog.Error("seed courses", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("commit", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	courses, err = store.ListCourses(scrapeCtx, c.db)
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
	slog.Info("endpoint consulted", "endpoint", "/api/v1/activities", "remote_addr", r.RemoteAddr, "search", r.URL.Query().Get("search"), "course_id", r.URL.Query().Get("course_id"))

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
	listCtx := ctx
	c.mu.Lock()
	defer c.mu.Unlock()

	if mode == "page" {
		scrapeCtx, cancel := detachedScrapeContext(ctx)
		defer cancel()
		listCtx = scrapeCtx
		if err := c.runActivitiesScraper(scrapeCtx, courseViewID); err != nil {
			slog.Error("activities scraper failed", "err", err)
			http.Error(w, "activity sync failed", http.StatusBadGateway)
			return
		}
	}
	activities, err := store.ListActivities(listCtx, c.db, courseViewID)
	if err != nil {
		slog.Error("list activities", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ids := make([]int, len(activities))
	for i, a := range activities {
		ids[i] = a.MoodleCourseID
	}
	attachments, err := store.ListAttachmentsByActivityIDs(listCtx, c.db, ids)
	if err != nil {
		slog.Error("list attachments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeActivitiesJSON(w, activities, attachments)
}

// CourseAttachmentList returns attachment file names and timestamps for a Moodle course (course/view.php?id=).
func (c *Controller) CourseAttachmentList(w http.ResponseWriter, r *http.Request) {
	if !c.requireAPIKey(w, r) {
		return
	}
	idStr := r.PathValue("courseViewID")
	courseViewID, err := strconv.Atoi(idStr)
	if err != nil || courseViewID <= 0 {
		http.Error(w, "invalid course id in path", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	list, err := store.ListAttachmentSummariesByCourseViewID(ctx, c.db, courseViewID)
	if err != nil {
		slog.Error("list attachment summaries", "course_view_id", courseViewID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{
		"course_view_id": courseViewID,
		"attachments":    list,
	})
}

// AttachmentContent returns one attachment body. Query: file_name (required) and either activity_id
// (activity cmid, same as activities.moodle_course_id) or course_id (Moodle course/view id). If only
// course_id is given, file_name must be unique within that course or 409 is returned.
func (c *Controller) AttachmentContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.requireAPIKey(w, r) {
		return
	}
	fileName := strings.TrimSpace(r.URL.Query().Get("file_name"))
	if fileName == "" {
		http.Error(w, "missing file_name query parameter", http.StatusBadRequest)
		return
	}
	activityID, activityErr := parseOptionalPositiveIntQuery(r, "activity_id")
	courseID, courseErr := parseOptionalPositiveIntQuery(r, "course_id")
	if activityErr != nil || courseErr != nil {
		http.Error(w, "invalid activity_id or course_id", http.StatusBadRequest)
		return
	}
	if activityID == nil && courseID == nil {
		http.Error(w, "pass activity_id or course_id together with file_name", http.StatusBadRequest)
		return
	}
	if activityID != nil && courseID != nil {
		http.Error(w, "pass only one of activity_id or course_id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var row store.AttachmentContentRow
	var getErr error
	if activityID != nil {
		row, getErr = store.GetAttachmentByActivityAndFileName(ctx, c.db, *activityID, fileName)
	} else {
		row, getErr = store.GetAttachmentByCourseViewIDAndFileName(ctx, c.db, *courseID, fileName)
	}
	if errors.Is(getErr, store.ErrAttachmentNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(getErr, store.ErrAttachmentAmbiguous) {
		http.Error(w, getErr.Error(), http.StatusConflict)
		return
	}
	if getErr != nil {
		slog.Error("get attachment content", "err", getErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(row)
}

func parseOptionalPositiveIntQuery(r *http.Request, name string) (*int, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("bad %s", name)
	}
	return &n, nil
}

func writeCoursesJSON(w http.ResponseWriter, courses []store.Course) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{"courses": courses})
}

type attachmentItem struct {
	FileName    string `json:"file_name"`
	FileContent string `json:"file_content"`
}

type activityResponse struct {
	MoodleCourseID  int             `json:"moodle_course_id"`
	CourseViewID    *int            `json:"course_view_id,omitempty"`
	CourseName      string          `json:"course_name"`
	Name            string          `json:"name"`
	Link            string          `json:"link"`
	ActivityContent json.RawMessage `json:"activity_content"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Attachments     []attachmentItem `json:"attachments"`
}

func writeActivitiesJSON(w http.ResponseWriter, activities []store.Activity, attachments map[int][]store.ActivityAttachment) {
	responses := make([]activityResponse, len(activities))
	for i, a := range activities {
		atts := attachments[a.MoodleCourseID]
		items := make([]attachmentItem, 0, len(atts))
		for _, att := range atts {
			items = append(items, attachmentItem{
				FileName:    att.FileName,
				FileContent: att.FileContent,
			})
		}
		responses[i] = activityResponse{
			MoodleCourseID:  a.MoodleCourseID,
			CourseViewID:    a.CourseViewID,
			CourseName:      a.CourseName,
			Name:            a.Name,
			Link:            a.Link,
			ActivityContent: a.ActivityContent,
			CreatedAt:       a.CreatedAt,
			UpdatedAt:       a.UpdatedAt,
			Attachments:     items,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(map[string]any{"activities": responses})
}
