package saia

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"uts_bot/internal/config"
	"uts_bot/internal/docparse"
	"uts_bot/internal/excel"
	"uts_bot/internal/moodlehttp"
	"uts_bot/internal/store"
)

// SAIA drives Moodle HTTP scraping (no browser).
type SAIA struct {
	c *moodlehttp.Client
	// DB when set, activity links from div.activityname are upserted into activities after each course page load.
	DB *sql.DB
	// Events accumulates the notification-worthy changes detected during this crawl. Callers own
	// flushing them (see TakeEvents), so a whole cycle lands in the outbox atomically and the
	// notifier never sees a half-finished scrape.
	Events []store.OutboxEvent

	lastCourseURL  string
	lastCourseHTML []byte
}

func New(c *moodlehttp.Client) *SAIA {
	return &SAIA{c: c}
}

// TakeEvents returns the events collected so far and clears the buffer, so a long-lived SAIA
// cannot replay the same change on a later cycle.
func (s *SAIA) TakeEvents() []store.OutboxEvent {
	events := s.Events
	s.Events = nil
	return events
}

// Run logs in, walks courses (every course in courses, or only onlyCourseViewID when set),
// persists activity links when DB is set, and syncs Excel deadlines. courses is ignored when
// onlyCourseViewID is set.
func (s *SAIA) Run(ctx context.Context, targetPage string, courses []store.Course, onlyCourseViewID *int) error {
	if err := s.c.LoginMoodle(ctx, targetPage, config.Username, config.Password); err != nil {
		return fmt.Errorf("moodle login: %w", err)
	}

	if onlyCourseViewID != nil {
		name := CourseLabelForMoodleID(ctx, s.DB, *onlyCourseViewID)
		s.processCoursePage(ctx, name, *onlyCourseViewID)
		return nil
	}

	for _, course := range courses {
		s.processCoursePage(ctx, course.Name, course.MoodleID)
	}
	return nil
}

// CourseLabelForMoodleID resolves a human course name from the DB, falling back to a generic
// label so notifications never show a bare id.
func CourseLabelForMoodleID(ctx context.Context, db *sql.DB, moodleCourseID int) string {
	if db != nil {
		if name, err := store.CourseNameByMoodleID(ctx, db, moodleCourseID); err == nil && name != "" {
			return name
		}
	}
	return fmt.Sprintf("course %d", moodleCourseID)
}

// courseTimelineInProgress is Moodle's myoverview classification for courses that have
// started, are not complete, and have not passed their end date. Using "all" also returns
// past enrollments; crawling those activities yields DEADLINE NOT FOUND spam because ended
// courses no longer expose assignment deadline markup.
const courseTimelineInProgress = "inprogress"

// DiscoverCourses logs in and returns enrolled courses Moodle classifies as in progress
// (active). Past and future courses are excluded so activity/deadline crawls skip dead terms.
//
// Moodle's "Mis cursos" / myoverview block often ships an empty HTML shell and fills cards
// via AJAX (core_course_get_enrolled_courses_by_timeline_classification). That AJAX call is
// preferred because it honors timeline classification; dashboard SSR links (when present)
// are only a fallback if AJAX fails.
func (s *SAIA) DiscoverCourses(ctx context.Context, targetPage string) ([]store.Course, error) {
	if err := s.c.LoginMoodle(ctx, targetPage, config.Username, config.Password); err != nil {
		return nil, fmt.Errorf("moodle login: %w", err)
	}
	body, err := s.c.Get(ctx, config.DashboardURL)
	if err != nil {
		return nil, fmt.Errorf("get dashboard %s: %w", config.DashboardURL, err)
	}

	courses, ajaxErr := s.discoverCoursesViaAjax(ctx, body)
	if ajaxErr == nil {
		if len(courses) == 0 {
			return nil, fmt.Errorf("no in-progress courses on dashboard %s", config.DashboardURL)
		}
		return courses, nil
	}
	slog.Warn("ajax in-progress course discovery failed; falling back to dashboard HTML",
		"err", ajaxErr, "dashboard", config.DashboardURL)

	courses, err = parseCourseLinks(body)
	if err != nil {
		return nil, fmt.Errorf("parse dashboard: %w", err)
	}
	if len(courses) == 0 {
		return nil, fmt.Errorf("no courses found on dashboard %s (ajax: %v)", config.DashboardURL, ajaxErr)
	}
	return courses, nil
}

func (s *SAIA) discoverCoursesViaAjax(ctx context.Context, dashboardHTML []byte) ([]store.Course, error) {
	sesskey, err := extractSesskey(dashboardHTML)
	if err != nil {
		return nil, fmt.Errorf("dashboard sesskey: %w", err)
	}
	base, err := moodleOrigin(config.DashboardURL)
	if err != nil {
		return nil, err
	}
	ajaxURL := base + "/lib/ajax/service.php?sesskey=" + url.QueryEscape(sesskey) +
		"&info=core_course_get_enrolled_courses_by_timeline_classification"

	var out []store.Course
	seen := make(map[int]bool)
	offset := 0
	for {
		payload := []map[string]any{{
			"index":      0,
			"methodname": "core_course_get_enrolled_courses_by_timeline_classification",
			"args": map[string]any{
				"offset":           offset,
				"limit":            48,
				"classification":   courseTimelineInProgress,
				"sort":             "fullname",
				"customfieldname":  "",
				"customfieldvalue": "",
			},
		}}
		body, err := s.c.PostJSON(ctx, ajaxURL, payload)
		if err != nil {
			return nil, fmt.Errorf("ajax enrolled courses (offset=%d): %w", offset, err)
		}
		batch, next, err := parseEnrolledCoursesAjax(body)
		if err != nil {
			return nil, fmt.Errorf("ajax enrolled courses decode (offset=%d): %w", offset, err)
		}
		for _, c := range batch {
			if seen[c.MoodleID] {
				continue
			}
			seen[c.MoodleID] = true
			out = append(out, c)
		}
		if len(batch) == 0 || next <= offset || next == 0 {
			break
		}
		offset = next
		if offset > 10000 {
			break
		}
	}
	return out, nil
}

func moodleOrigin(dashboardURL string) (string, error) {
	u, err := url.Parse(dashboardURL)
	if err != nil {
		return "", fmt.Errorf("parse dashboard url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("dashboard url missing scheme/host: %s", dashboardURL)
	}
	return u.Scheme + "://" + u.Host, nil
}

func extractSesskey(html []byte) (string, error) {
	// Prefer M.cfg.sesskey from the Moodle page bootstrap.
	if m := sesskeyCfgRe.FindSubmatch(html); len(m) == 2 {
		return string(m[1]), nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return "", err
	}
	if v, ok := doc.Find(`input[name="sesskey"]`).First().Attr("value"); ok && v != "" {
		return v, nil
	}
	href, _ := doc.Find(`a[href*="logout.php"][href*="sesskey="]`).First().Attr("href")
	if href != "" {
		u, err := url.Parse(href)
		if err == nil {
			if sk := u.Query().Get("sesskey"); sk != "" {
				return sk, nil
			}
		}
	}
	return "", fmt.Errorf("sesskey not found in dashboard HTML")
}

var sesskeyCfgRe = regexp.MustCompile(`"sesskey"\s*:\s*"([^"]+)"`)

type enrolledCoursesAjaxItem struct {
	Error  bool `json:"error"`
	Data   *struct {
		Courses []struct {
			ID        int    `json:"id"`
			Fullname  string `json:"fullname"`
			Shortname string `json:"shortname"`
		} `json:"courses"`
		NextOffset int `json:"nextoffset"`
	} `json:"data"`
	Exception string `json:"exception"`
	Message   string `json:"message"`
}

func parseEnrolledCoursesAjax(body []byte) ([]store.Course, int, error) {
	var items []enrolledCoursesAjaxItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return nil, 0, fmt.Errorf("empty ajax response")
	}
	item := items[0]
	if item.Error {
		msg := item.Message
		if msg == "" {
			msg = item.Exception
		}
		if msg == "" {
			msg = "unknown ajax error"
		}
		return nil, 0, fmt.Errorf("%s", msg)
	}
	if item.Data == nil {
		return nil, 0, fmt.Errorf("ajax response missing data")
	}
	out := make([]store.Course, 0, len(item.Data.Courses))
	for _, c := range item.Data.Courses {
		if c.ID <= 0 {
			continue
		}
		name := collapseWhitespace(c.Fullname)
		if name == "" {
			name = collapseWhitespace(c.Shortname)
		}
		if name == "" {
			continue
		}
		out = append(out, store.Course{MoodleID: c.ID, Name: name})
	}
	return out, item.Data.NextOffset, nil
}

// parseCourseLinks scans HTML for links to course/view.php?id=N and returns one Course per
// distinct id, in document order. Also accepts Moodle myoverview cards via data-course-id.
func parseCourseLinks(html []byte) ([]store.Course, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	seen := make(map[int]bool)
	var out []store.Course

	doc.Find(`a[href*="course/view.php"]`).Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		id, ok := courseViewIDFromHref(href)
		if !ok || seen[id] {
			return
		}
		name := courseLinkName(a)
		if name == "" {
			return
		}
		seen[id] = true
		out = append(out, store.Course{MoodleID: id, Name: name})
	})

	// Card shells sometimes expose the id on the card even when the nested link text is empty.
	doc.Find(`[data-course-id]`).Each(func(_ int, card *goquery.Selection) {
		raw, _ := card.Attr("data-course-id")
		v, err := strconv.ParseUint(raw, 10, 31)
		if err != nil || v == 0 {
			return
		}
		id := int(v)
		if seen[id] {
			return
		}
		name := courseCardName(card)
		if name == "" {
			return
		}
		seen[id] = true
		out = append(out, store.Course{MoodleID: id, Name: name})
	})
	return out, nil
}

func courseCardName(card *goquery.Selection) string {
	if t, ok := card.Find(`.coursename [title], .multiline[title], [title]`).First().Attr("title"); ok {
		if name := collapseWhitespace(t); name != "" {
			return name
		}
	}
	if name := collapseWhitespace(card.Find(".visually-hidden, .sr-only").First().Text()); name != "" {
		return name
	}
	return collapseWhitespace(card.Find(".coursename, .multiline").First().Text())
}

func courseViewIDFromHref(raw string) (int, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, false
	}
	idStr := u.Query().Get("id")
	if idStr == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(idStr, 10, 31)
	if err != nil || v == 0 {
		return 0, false
	}
	return int(v), true
}

// courseLinkName prefers an explicit label (title/aria-label) over the link's visible text,
// since Moodle course cards often nest extra screen-reader-only text inside the link that would
// otherwise get concatenated into the name.
func courseLinkName(a *goquery.Selection) string {
	for _, attr := range []string{"title", "aria-label"} {
		if v, ok := a.Attr(attr); ok {
			if name := collapseWhitespace(v); name != "" {
				return name
			}
		}
	}
	return collapseWhitespace(a.Text())
}

func (s *SAIA) processCoursePage(ctx context.Context, courseName string, moodleCourseID int) {
	courseURL, err := courseViewURL(moodleCourseID)
	if err != nil {
		slog.Error("course view URL", "course", courseName, "err", err)
		return
	}
	slog.Info("fetching course", "name", courseName, "moodle_id", moodleCourseID, "url", courseURL)
	body, err := s.c.Get(ctx, courseURL)
	if err != nil {
		slog.Error("get course page failed", "course", courseName, "err", err)
		return
	}
	s.lastCourseURL = courseURL
	s.lastCourseHTML = body
	time.Sleep(300 * time.Millisecond)

	if s.DB != nil {
		if err := s.persistActivityNameLinks(ctx, courseName, courseURL, body, uint32(moodleCourseID)); err != nil {
			slog.Error("persist activity name links", "course", courseName, "err", err)
		}
	}

	if err := s.getSAIAActivitiesFromCourse(ctx, courseURL, body); err != nil {
		slog.Error("get activities failed", "course", courseName, "err", err)
	}
}

// RunThenGetSAIAActivities runs the full SAIA crawl (Run), then runs getSAIAActivities
// again on the last fetched course page. Used by the /activities API.
func (s *SAIA) RunThenGetSAIAActivities(ctx context.Context, targetPage string, courses []store.Course, onlyCourseViewID *int) error {
	if err := s.Run(ctx, targetPage, courses, onlyCourseViewID); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	if len(s.lastCourseHTML) == 0 {
		return nil
	}
	if err := s.getSAIAActivitiesFromCourse(ctx, s.lastCourseURL, s.lastCourseHTML); err != nil {
		return fmt.Errorf("get SAIA activities: %w", err)
	}
	return nil
}

func (s *SAIA) persistActivityNameLinks(ctx context.Context, courseName, coursePageURL string, courseHTML []byte, courseViewID uint32) error {
	base, err := url.Parse(coursePageURL)
	if err != nil {
		return fmt.Errorf("parse course page URL: %w", err)
	}
	links, err := collectActivityNameLinks(courseHTML)
	if err != nil {
		return fmt.Errorf("collect activityname links: %w", err)
	}

	var rows []store.ActivityUpsert
	var attachmentRows []store.ActivityAttachmentUpsert
	for _, link := range links {
		id, ok := moodleActivityModuleID(link.Href)
		if !ok {
			continue
		}
		name := strings.TrimSpace(link.Text)
		if name == "" {
			name = "(no title)"
		}
		absURL := strings.TrimSpace(resolveHREF(base, link.Href))
		activityText := ""
		var actBody []byte
		if absURL != "" {
			var fetchErr error
			actBody, fetchErr = s.c.Get(ctx, absURL)
			if fetchErr != nil {
				slog.Warn("fetch activity page for activity_content", "url", absURL, "err", fetchErr)
			} else {
				var textErr error
				activityText, textErr = pageDivText(actBody)
				if textErr != nil {
					slog.Warn("parse activity page #page", "url", absURL, "err", textErr)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}

		// Collect document attachments (.pdf, .docx, .xlsx) linked from the activity page.
		if len(actBody) > 0 && absURL != "" {
			actBase, parseErr := url.Parse(absURL)
			if parseErr == nil {
				docs, docsErr := collectDocumentLinks(actBody, actBase)
				if docsErr != nil {
					slog.Warn("collect document links", "url", absURL, "err", docsErr)
				}
				for _, doc := range docs {
					content, dlErr := s.downloadAndParseDocument(ctx, doc.URL, doc.FileName)
					if dlErr != nil {
						slog.Warn("download/parse document", "url", doc.URL, "err", dlErr)
						continue
					}
					attachmentRows = append(attachmentRows, store.ActivityAttachmentUpsert{
						MoodleCourseID: id,
						FileName:       doc.FileName,
						FileContent:    content,
					})
				}
			}
		}

		cv := courseViewID
		content, err := json.Marshal(map[string]string{
			"course_name":    courseName,
			"course_view_id": strconv.FormatUint(uint64(courseViewID), 10),
			"content":        activityText,
		})
		if err != nil {
			return fmt.Errorf("marshal activity_content: %w", err)
		}
		rows = append(rows, store.ActivityUpsert{
			MoodleCourseID:  id,
			CourseViewID:    &cv,
			Name:            name,
			Link:            absURL,
			ActivityContent: content,
		})
	}
	cv := courseViewID
	activityEvents, err := store.UpsertActivities(ctx, s.DB, rows, courseName)
	if err != nil {
		return err
	}
	s.Events = append(s.Events, activityEvents...)

	attachmentEvents, err := store.UpsertActivityAttachments(ctx, s.DB, attachmentRows, &cv, courseName)
	if err != nil {
		return err
	}
	s.Events = append(s.Events, attachmentEvents...)
	return nil
}

// PersistCourseGrades scrapes the grade report for one course, stores it, and collects an event
// for every item whose mark appeared or changed.
func (s *SAIA) PersistCourseGrades(ctx context.Context, courseName string, moodleCourseID int) error {
	if s.DB == nil {
		return nil
	}
	report, err := s.GetCourseCalifications(ctx, config.SAIAPage, moodleCourseID)
	if err != nil {
		return fmt.Errorf("get califications: %w", err)
	}
	events, err := PersistCourseGradeReport(ctx, s.DB, courseName, report)
	if err != nil {
		return err
	}
	s.Events = append(s.Events, events...)
	return nil
}

// PersistCourseGradeReport stores an already-fetched grade report. It is split out from
// PersistCourseGrades so the /califications handler can persist the same report it is about to
// return without scraping Moodle twice.
func PersistCourseGradeReport(ctx context.Context, db *sql.DB, courseName string, report *CourseGrades) ([]store.OutboxEvent, error) {
	if db == nil || report == nil || len(report.Rows) == 0 {
		return nil, nil
	}
	rows := make([]store.GradeUpsert, 0, len(report.Rows))
	for _, r := range report.Rows {
		rows = append(rows, store.GradeUpsert{
			RowType:      r.RowType,
			CategoryPath: r.CategoryPath,
			ActivityType: r.ActivityType,
			ItemName:     r.Name,
			Grade:        r.Grade,
			Percentage:   r.Percentage,
			Weight:       r.Weight,
			Link:         r.Link,
		})
	}
	events, err := store.UpsertGrades(ctx, db, uint32(report.CourseViewID), courseName, rows)
	if err != nil {
		return nil, fmt.Errorf("upsert grades for course %d: %w", report.CourseViewID, err)
	}
	return events, nil
}

// moodleActivityModuleID returns the course-module id from Moodle URLs (?id= on mod pages).
func moodleActivityModuleID(raw string) (uint32, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return 0, false
	}
	idStr := u.Query().Get("id")
	if idStr == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint32(v), true
}

func courseViewURL(moodleCourseID int) (string, error) {
	u, err := url.Parse(config.CourseViewBaseURL)
	if err != nil {
		return "", fmt.Errorf("course view base URL: %w", err)
	}
	q := u.Query()
	q.Set("id", fmt.Sprintf("%d", moodleCourseID))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *SAIA) getSAIAActivitiesFromCourse(ctx context.Context, coursePageURL string, courseHTML []byte) error {
	base, err := url.Parse(coursePageURL)
	if err != nil {
		return fmt.Errorf("parse course URL: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(courseHTML))
	if err != nil {
		return fmt.Errorf("parse course HTML: %w", err)
	}
	var sections []*goquery.Selection
	doc.Find(".course-content-item-content").Each(func(_ int, sel *goquery.Selection) {
		sections = append(sections, sel)
	})
	if len(sections) < 2 {
		return nil
	}
	for i := 1; i < len(sections); i++ {
		sec := sections[i]
		links := sec.Find(".aalink.stretched-link")
		titles := sec.Find(".text-uppercase.small")
		for j := 0; j < links.Length(); j++ {
			linkSel := links.Eq(j)
			href, _ := linkSel.Attr("href")
			if href == "" || href == "#" {
				continue
			}
			titleText := ""
			if j < titles.Length() {
				titleText = strings.TrimSpace(titles.Eq(j).Text())
			}
			if isUndesired(titleText) {
				continue
			}
			abs := resolveHREF(base, href)
			if err := s.processActivityPage(ctx, abs); err != nil {
				slog.Warn("process activity", "url", abs, "err", err)
			}
		}
	}
	return nil
}

func (s *SAIA) processActivityPage(ctx context.Context, activityURL string) error {
	html, err := s.c.Get(ctx, activityURL)
	if err != nil {
		return fmt.Errorf("get activity: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return fmt.Errorf("parse activity HTML: %w", err)
	}
	if doc.Find(".description-inner").Length() == 0 {
		slog.Warn("DEADLINE NOT FOUND ON CURRENT ACTIVITY, skipping", "url", activityURL)
		return nil
	}
	descInner := doc.Find(".description-inner").First()
	deadlineText := strings.TrimSpace(descInner.Text())
	divs := descInner.Find("div")
	if divs.Length() == 0 {
		return nil
	}
	lastDiv := divs.Last()
	itemToRemove := strings.TrimSpace(lastDiv.Find("strong").First().Text())
	if itemToRemove == "" || !strings.Contains(deadlineText, itemToRemove) {
		return nil
	}
	activityDateText := strings.TrimSpace(lastDiv.Text())
	newActivityDate := activityDateText
	if prefix := itemToRemove + " "; strings.HasPrefix(activityDateText, prefix) {
		newActivityDate = activityDateText[len(prefix):]
	}

	activityDesc := strings.TrimSpace(doc.Find(".page-header-headings").Text())
	activityDesc = strings.ReplaceAll(strings.ReplaceAll(activityDesc, "\n", " "), "\t", " ")

	subjectTitle := strings.TrimSpace(doc.Find(".breadcrumb-item a").First().Text())

	cleaned := excel.BreakWord(activityDesc)
	slog.Info("activity found",
		"subject", subjectTitle,
		"activity", activityDesc,
		"scheduled", newActivityDate,
	)
	slog.Info("writing to file...")

	if err := excel.WriteDataToExcel(newActivityDate, fmt.Sprintf("\n\n%s::%s\n", subjectTitle, cleaned)); err != nil {
		slog.Error("write excel failed", "err", err)
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}

func resolveHREF(base *url.URL, href string) string {
	r, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(r).String()
}

type activityNameLink struct {
	Href string
	Text string
}

func collectActivityNameLinks(html []byte) ([]activityNameLink, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	var out []activityNameLink
	doc.Find("div").Each(func(_ int, d *goquery.Selection) {
		cls, ok := d.Attr("class")
		if !ok || cls == "" {
			return
		}
		has := false
		for _, p := range strings.Fields(cls) {
			if p == "activityname" {
				has = true
				break
			}
		}
		if !has {
			return
		}
		d.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			out = append(out, activityNameLink{
				Href: href,
				Text: strings.TrimSpace(a.Text()),
			})
		})
	})
	return out, nil
}

func pageDivText(html []byte) (string, error) {
	if len(html) == 0 {
		return "", nil
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return "", err
	}
	sel := doc.Find("#page").First()
	if sel.Length() == 0 {
		return "", nil
	}
	// Remove nodes whose text is not human content (RequireJS/CDATA, styles, etc.).
	sel.Find("script, style, noscript, template, iframe, object, embed").Remove()
	raw := strings.TrimSpace(sel.Text())
	return collapseWhitespace(raw), nil
}

// collapseWhitespace turns any run of Unicode whitespace into a single ASCII space.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func isUndesired(title string) bool {
	for _, u := range config.UndesiredActivities {
		if u == title {
			return true
		}
	}
	return false
}

type documentLink struct {
	URL      string
	FileName string
}

var documentExtensions = []string{".pdf", ".docx", ".xlsx"}

// collectDocumentLinks scans HTML for <a href> links pointing to supported document types.
func collectDocumentLinks(html []byte, base *url.URL) ([]documentLink, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []documentLink
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if href == "" {
			return
		}
		lower := strings.ToLower(href)
		matched := ""
		for _, ext := range documentExtensions {
			if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") || strings.Contains(lower, ext+"&") {
				matched = ext
				break
			}
		}
		if matched == "" {
			return
		}
		abs := resolveHREF(base, href)
		if seen[abs] {
			return
		}
		seen[abs] = true
		u, parseErr := url.Parse(abs)
		fileName := ""
		if parseErr == nil {
			fileName = path.Base(u.Path)
		}
		if fileName == "" || fileName == "." || fileName == "/" {
			fileName = "document" + matched
		}
		out = append(out, documentLink{URL: abs, FileName: fileName})
	})
	return out, nil
}

// downloadAndParseDocument downloads a document and extracts its plain-text content.
func (s *SAIA) downloadAndParseDocument(ctx context.Context, docURL, fileName string) (string, error) {
	data, err := s.c.Get(ctx, docURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	lower := strings.ToLower(fileName)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return docparse.ParsePDF(data)
	case strings.HasSuffix(lower, ".docx"):
		return docparse.ParseDOCX(data)
	case strings.HasSuffix(lower, ".xlsx"):
		return docparse.ParseXLSX(data)
	default:
		return "", fmt.Errorf("unsupported file type: %s", fileName)
	}
}
