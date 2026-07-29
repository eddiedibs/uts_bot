package saia

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"uts_bot/internal/config"
)

// GradeRow is one row from the Moodle user grade report (table.user-grade).
type GradeRow struct {
	RowType      string   `json:"row_type"` // category, item, subtotal, course_total
	Level        int      `json:"level,omitempty"`
	CategoryPath []string `json:"category_path,omitempty"`
	ActivityType string   `json:"activity_type,omitempty"`
	Name         string   `json:"name"`
	Link         string   `json:"link,omitempty"`
	Weight       string   `json:"weight,omitempty"`
	Grade        string   `json:"grade,omitempty"`
	Passed       *bool    `json:"passed,omitempty"`
	Range        string   `json:"range,omitempty"`
	Percentage   string   `json:"percentage,omitempty"`
	Feedback     string   `json:"feedback,omitempty"`
	Contribution string   `json:"contribution_to_course_total,omitempty"`
}

// CourseGrades is the parsed Calificaciones report for one course.
type CourseGrades struct {
	CourseViewID int        `json:"course_view_id"`
	ReportURL    string     `json:"report_url"`
	Rows         []GradeRow `json:"rows"`
}

var levelClassRe = regexp.MustCompile(`\blevel(\d+)\b`)

// GetCourseCalifications logs in if needed, opens the course page, follows the Calificaciones
// link (grade report), and parses table.user-grade.
func (s *SAIA) GetCourseCalifications(ctx context.Context, targetPage string, moodleCourseID int) (*CourseGrades, error) {
	if err := s.c.LoginMoodle(ctx, targetPage, config.Username, config.Password); err != nil {
		return nil, fmt.Errorf("moodle login: %w", err)
	}

	courseURL, err := courseViewURL(moodleCourseID)
	if err != nil {
		return nil, err
	}
	courseHTML, err := s.c.Get(ctx, courseURL)
	if err != nil {
		return nil, fmt.Errorf("get course page: %w", err)
	}

	gradeURL, err := gradeReportURLFromCoursePage(courseURL, courseHTML, moodleCourseID)
	if err != nil {
		return nil, err
	}
	gradeHTML, err := s.c.Get(ctx, gradeURL)
	if err != nil {
		return nil, fmt.Errorf("get grade report: %w", err)
	}

	rows, err := parseUserGradeTable(gradeHTML)
	if err != nil {
		return nil, err
	}
	return &CourseGrades{
		CourseViewID: moodleCourseID,
		ReportURL:    gradeURL,
		Rows:         rows,
	}, nil
}

func gradeReportURL(moodleCourseID int) (string, error) {
	base := strings.TrimSuffix(config.CourseViewBaseURL, "/course/view.php")
	if base == config.CourseViewBaseURL {
		u, err := url.Parse(config.CourseViewBaseURL)
		if err != nil {
			return "", fmt.Errorf("course view base URL: %w", err)
		}
		base = strings.TrimSuffix(u.String(), "/course/view.php")
	}
	u, err := url.Parse(base + "/grade/report/index.php")
	if err != nil {
		return "", fmt.Errorf("grade report base URL: %w", err)
	}
	q := u.Query()
	q.Set("id", strconv.Itoa(moodleCourseID))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func gradeReportURLFromCoursePage(coursePageURL string, courseHTML []byte, moodleCourseID int) (string, error) {
	base, err := url.Parse(coursePageURL)
	if err != nil {
		return "", fmt.Errorf("parse course page URL: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(courseHTML))
	if err != nil {
		return "", fmt.Errorf("parse course HTML: %w", err)
	}

	var found string
	doc.Find(`a[href*="grade/report/index.php"]`).EachWithBreak(func(_ int, a *goquery.Selection) bool {
		label := collapseWhitespace(a.Text())
		if !strings.EqualFold(label, "Calificaciones") {
			return true
		}
		href, _ := a.Attr("href")
		if href == "" {
			return true
		}
		found = resolveHREF(base, href)
		return false
	})
	if found != "" {
		return found, nil
	}
	return gradeReportURL(moodleCourseID)
}

func parseUserGradeTable(html []byte) ([]GradeRow, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse grade HTML: %w", err)
	}
	tbl := doc.Find("table.user-grade").First()
	if tbl.Length() == 0 {
		return nil, fmt.Errorf("grade table table.user-grade not found")
	}

	var out []GradeRow
	catPath := make([]string, 0, 4)

	tbl.Find("tbody tr").Each(func(_ int, tr *goquery.Selection) {
		if strings.Contains(tr.AttrOr("class", ""), "spacer") {
			return
		}
		th := tr.Find("th").First()
		if th.Length() == 0 {
			return
		}
		thClass := th.AttrOr("class", "")
		level := classLevel(thClass)

		if strings.Contains(thClass, "category") {
			name := categoryName(th)
			if name == "" {
				return
			}
			catPath = setCategoryPath(catPath, level, name)
			out = append(out, GradeRow{
				RowType:      "category",
				Level:        level,
				CategoryPath: append([]string(nil), catPath...),
				Name:         name,
			})
			return
		}

		rowType := "item"
		switch {
		case strings.Contains(thClass, "courseitem"):
			rowType = "course_total"
		case strings.Contains(thClass, "baggt"):
			rowType = "subtotal"
		}

		name, link := gradeItemName(th)
		if name == "" {
			return
		}

		row := GradeRow{
			RowType:      rowType,
			Level:        level,
			CategoryPath: append([]string(nil), catPath...),
			ActivityType: strings.TrimSpace(th.Find(".text-uppercase.small").First().Text()),
			Name:         name,
			Link:         link,
			Weight:       cellText(tr.Find("td.column-weight").First()),
			Grade:        gradeCellText(tr.Find("td.column-grade").First()),
			Range:        cellText(tr.Find("td.column-range").First()),
			Percentage:   cellText(tr.Find("td.column-percentage").First()),
			Feedback:     feedbackText(tr.Find("td.column-feedback").First()),
			Contribution: cellText(tr.Find("td.column-contributiontocoursetotal").First()),
		}
		if rowType == "item" {
			if passed, ok := gradePassed(tr.Find("td.column-grade").First()); ok {
				row.Passed = &passed
			}
		}
		out = append(out, row)
	})

	return out, nil
}

func classLevel(class string) int {
	m := levelClassRe.FindStringSubmatch(class)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func setCategoryPath(path []string, level int, name string) []string {
	if level < 1 {
		level = 1
	}
	idx := level - 1
	if idx >= len(path) {
		out := append(path, make([]string, idx-len(path)+1)...)
		out[idx] = name
		return out
	}
	out := append(path[:idx], name)
	return out
}

func categoryName(th *goquery.Selection) string {
	sel := th.Find(".category-content > span").Last()
	if sel.Length() == 0 {
		sel = th.Find(".category-content span").Last()
	}
	return strings.TrimSpace(sel.Text())
}

func gradeItemName(th *goquery.Selection) (name, link string) {
	header := th.Find(".gradeitemheader").First()
	if header.Length() == 0 {
		return collapseWhitespace(th.Text()), ""
	}
	if a := header.Find("a[href]").First(); a.Length() > 0 {
		href, _ := a.Attr("href")
		return strings.TrimSpace(a.Text()), strings.TrimSpace(href)
	}
	if title, ok := header.Attr("title"); ok && strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title), ""
	}
	return strings.TrimSpace(header.Text()), ""
}

func cellText(sel *goquery.Selection) string {
	if sel.Length() == 0 {
		return ""
	}
	return collapseWhitespace(sel.Text())
}

func gradeCellText(sel *goquery.Selection) string {
	if sel.Length() == 0 {
		return ""
	}
	clone := sel.Clone()
	clone.Find("i, .action-menu, .moodle-actionmenu").Remove()
	return collapseWhitespace(clone.Text())
}

func gradePassed(sel *goquery.Selection) (bool, bool) {
	if sel.Length() == 0 {
		return false, false
	}
	if sel.Find("i.fa-check.text-success").Length() > 0 {
		return true, true
	}
	if sel.HasClass("gradepass") {
		return true, true
	}
	return false, false
}

func feedbackText(sel *goquery.Selection) string {
	if sel.Length() == 0 {
		return ""
	}
	t := collapseWhitespace(sel.Text())
	if t == "" || t == "\u00a0" {
		return ""
	}
	return t
}
