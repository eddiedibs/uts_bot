package saia

import (
	"strings"
	"testing"
)

func TestCourseTimelineClassificationIsInProgress(t *testing.T) {
	t.Parallel()

	// Crawl/sync must use Moodle's active enrollment set. "all" includes past courses and
	// produces DEADLINE NOT FOUND spam when activities on ended courses are fetched.
	if courseTimelineInProgress != "inprogress" {
		t.Fatalf("courseTimelineInProgress = %q, want %q", courseTimelineInProgress, "inprogress")
	}
}

func TestParseCourseLinksDedupesByID(t *testing.T) {
	t.Parallel()

	// Realistic Moodle dashboard markup: a course card's link nests extra spans (icon labels,
	// screen-reader text) that would otherwise get concatenated into the visible text, and the
	// same course can be linked twice (card title + "view course" button).
	html := []byte(`
<div class="dashboard-card">
  <a class="aalink" href="https://saia.uft.edu.ve/course/view.php?id=23277" title="Analisis numerico">
    <span class="sr-only">Course</span>
    <span class="coursename">Analisis numerico</span>
  </a>
  <a href="https://saia.uft.edu.ve/course/view.php?id=23277">View</a>
</div>
<div class="dashboard-card">
  <a href="/course/view.php?id=23265" aria-label="Computacion para ingenieros">
    Computacion
  </a>
</div>
<div class="dashboard-card">
  <a href="/course/view.php?id=23348">
    Quimica
  </a>
</div>
`)

	got, err := parseCourseLinks(html)
	if err != nil {
		t.Fatalf("parseCourseLinks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d courses, want 3: %+v", len(got), got)
	}

	want := map[int]string{
		23277: "Analisis numerico",
		23265: "Computacion para ingenieros",
		23348: "Quimica",
	}
	seen := make(map[int]bool)
	for _, c := range got {
		if seen[c.MoodleID] {
			t.Fatalf("duplicate course id %d in result: %+v", c.MoodleID, got)
		}
		seen[c.MoodleID] = true
		wantName, ok := want[c.MoodleID]
		if !ok {
			t.Fatalf("unexpected course id %d: %+v", c.MoodleID, c)
		}
		if c.Name != wantName {
			t.Errorf("course %d name = %q, want %q", c.MoodleID, c.Name, wantName)
		}
	}
}

func TestParseCourseLinksFromMyoverviewCard(t *testing.T) {
	t.Parallel()

	html := []byte(`
<div class="card course-card" data-region="course-content" data-course-id="24203">
  <a href="https://saia.uft.edu.ve/course/view.php?id=24203" tabindex="-1">
    <span class="visually-hidden"> Gerencia, Liderazgo y Emprendimiento </span>
  </a>
  <a href="https://saia.uft.edu.ve/course/view.php?id=24203" class="aalink coursename">
    <span class="multiline" title=" Gerencia, Liderazgo y Emprendimiento (Por Competencia)">
      Gerencia, Liderazgo y Emprendimiento (Por ...
    </span>
  </a>
</div>
`)
	got, err := parseCourseLinks(html)
	if err != nil {
		t.Fatalf("parseCourseLinks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d courses, want 1: %+v", len(got), got)
	}
	if got[0].MoodleID != 24203 {
		t.Errorf("id = %d, want 24203", got[0].MoodleID)
	}
	if !strings.Contains(got[0].Name, "Gerencia") {
		t.Errorf("name = %q, want Gerencia…", got[0].Name)
	}
}

func TestParseEnrolledCoursesAjax(t *testing.T) {
	t.Parallel()

	body := []byte(`[{
		"error": false,
		"data": {
			"courses": [
				{"id": 24203, "fullname": "Gerencia", "shortname": "GER"},
				{"id": 24526, "fullname": "Computacion para Ingenieros", "shortname": "COMP"}
			],
			"nextoffset": 2
		}
	}]`)
	got, next, err := parseEnrolledCoursesAjax(body)
	if err != nil {
		t.Fatalf("parseEnrolledCoursesAjax: %v", err)
	}
	if next != 2 {
		t.Errorf("nextoffset = %d, want 2", next)
	}
	if len(got) != 2 {
		t.Fatalf("got %d courses, want 2", len(got))
	}
	if got[0].MoodleID != 24203 || got[0].Name != "Gerencia" {
		t.Errorf("first = %+v", got[0])
	}
}

func TestExtractSesskey(t *testing.T) {
	t.Parallel()

	html := []byte(`<html><script>M.cfg = {"wwwroot":"https://saia.uft.edu.ve","sesskey":"ChRMaoEbkb"};</script>
<a href="https://saia.uft.edu.ve/login/logout.php?sesskey=OTHER">Cerrar sesión</a></html>`)
	got, err := extractSesskey(html)
	if err != nil {
		t.Fatalf("extractSesskey: %v", err)
	}
	if got != "ChRMaoEbkb" {
		t.Errorf("sesskey = %q, want ChRMaoEbkb", got)
	}
}

func TestParseCourseLinksSkipsLinksWithoutUsableName(t *testing.T) {
	t.Parallel()

	html := []byte(`<a href="/course/view.php?id=1"><img src="x.png"></a>`)

	got, err := parseCourseLinks(html)
	if err != nil {
		t.Fatalf("parseCourseLinks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no courses (link has no name)", got)
	}
}

func TestParseCourseLinksIgnoresUnrelatedLinks(t *testing.T) {
	t.Parallel()

	html := []byte(`
<a href="/course/view.php">All courses</a>
<a href="/course/view.php?id=0">Zero id</a>
<a href="/course/view.php?id=abc">Bad id</a>
<a href="/mod/assign/view.php?id=999">Not a course link</a>
`)

	got, err := parseCourseLinks(html)
	if err != nil {
		t.Fatalf("parseCourseLinks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none of these to parse as courses", got)
	}
}

func TestCourseViewIDFromHref(t *testing.T) {
	t.Parallel()

	tests := []struct {
		href   string
		wantID int
		wantOK bool
	}{
		{"https://saia.uft.edu.ve/course/view.php?id=23277", 23277, true},
		{"/course/view.php?id=23277&notifyeditingon=1", 23277, true},
		{"/course/view.php", 0, false},
		{"/course/view.php?id=0", 0, false},
		{"/course/view.php?id=-1", 0, false},
		{"/course/view.php?id=abc", 0, false},
	}
	for _, tt := range tests {
		id, ok := courseViewIDFromHref(tt.href)
		if id != tt.wantID || ok != tt.wantOK {
			t.Errorf("courseViewIDFromHref(%q) = (%d, %v), want (%d, %v)", tt.href, id, ok, tt.wantID, tt.wantOK)
		}
	}
}
