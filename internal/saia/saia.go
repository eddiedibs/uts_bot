package saia

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"uts_bot/internal/config"
	"uts_bot/internal/coursestatic"
	"uts_bot/internal/excel"
	"uts_bot/internal/moodlehttp"
	"uts_bot/internal/store"
)

// SAIA drives Moodle HTTP scraping (no browser).
type SAIA struct {
	c *moodlehttp.Client
	// DB when set, activity links from div.activityname are upserted into activities after each course page load.
	DB *sql.DB

	lastCourseURL  string
	lastCourseHTML []byte
}

func New(c *moodlehttp.Client) *SAIA {
	return &SAIA{c: c}
}

// Run logs in, walks courses (all static courses, or only onlyCourseViewID when set), persists activity links when DB is set, and syncs Excel deadlines.
func (s *SAIA) Run(ctx context.Context, targetPage string, onlyCourseViewID *int) error {
	if err := s.c.LoginMoodle(ctx, targetPage, config.Username, config.Password); err != nil {
		return fmt.Errorf("moodle login: %w", err)
	}

	if onlyCourseViewID != nil {
		name := courseLabelForMoodleID(*onlyCourseViewID)
		s.processCoursePage(ctx, name, *onlyCourseViewID)
		return nil
	}

	for _, course := range coursestatic.UFTMoodleCourses {
		s.processCoursePage(ctx, course.Name, course.MoodleID)
	}
	return nil
}

func courseLabelForMoodleID(moodleCourseID int) string {
	for _, c := range coursestatic.UFTMoodleCourses {
		if c.MoodleID == moodleCourseID {
			return c.Name
		}
	}
	return fmt.Sprintf("course %d", moodleCourseID)
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
func (s *SAIA) RunThenGetSAIAActivities(ctx context.Context, targetPage string, onlyCourseViewID *int) error {
	if err := s.Run(ctx, targetPage, onlyCourseViewID); err != nil {
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
		if absURL != "" {
			actBody, err := s.c.Get(ctx, absURL)
			if err != nil {
				slog.Warn("fetch activity page for activity_content", "url", absURL, "err", err)
			} else {
				activityText, err = pageDivText(actBody)
				if err != nil {
					slog.Warn("parse activity page #page", "url", absURL, "err", err)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		cv := courseViewID
		content, err := json.Marshal(map[string]string{
			"course_name":     courseName,
			"course_view_id":  strconv.FormatUint(uint64(courseViewID), 10),
			"content":         activityText,
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
	return store.UpsertActivities(ctx, s.DB, rows)
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
