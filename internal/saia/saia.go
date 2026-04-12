package saia

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"uts_bot/internal/browser"
	"uts_bot/internal/config"
	"uts_bot/internal/excel"
)

type SAIA struct {
	b *browser.Browser
}

func New(b *browser.Browser) *SAIA {
	return &SAIA{b: b}
}

func (s *SAIA) Run(targetPage string) error {
	if err := s.b.OpenPage(targetPage); err != nil {
		return fmt.Errorf("open page: %w", err)
	}

	// Native Moodle login (#login): fill credentials then submit.
	if config.Username == "" || config.Password == "" {
		return fmt.Errorf("set UTS_USERNAME and UTS_PASSWORD (e.g. in .env)")
	}
	if err := s.b.ClearAndType("#username", config.Username, false); err != nil {
		return fmt.Errorf("username: %w", err)
	}
	if err := s.b.ClearAndType("#password", config.Password, false); err != nil {
		return fmt.Errorf("password: %w", err)
	}
	if err := s.b.Click("#" + config.LoginBtn); err != nil {
		return fmt.Errorf("submit login: %w", err)
	}

	time.Sleep(2 * time.Second)

	for _, course := range UFTMoodleCourses {
		courseURL, err := courseViewURL(course.MoodleID)
		if err != nil {
			return err
		}
		slog.Info("opening course", "name", course.Name, "moodle_id", course.MoodleID, "url", courseURL)
		if err := s.b.OpenPage(courseURL); err != nil {
			slog.Error("open course page failed", "course", course.Name, "err", err)
			continue
		}
		time.Sleep(2 * time.Second)

		if err := s.getSAIAActivities(); err != nil {
			slog.Error("get activities failed", "course", course.Name, "err", err)
		}
	}
	return nil
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

func (s *SAIA) getSAIAActivities() error {
	sectionCount, err := s.b.CountElements(".course-content-item-content")
	if err != nil {
		return fmt.Errorf("count sections: %w", err)
	}

	for i := 1; i < sectionCount; i++ {
		classAttr, err := s.b.GetElementAttribute(".course-content-item-content", i, "class")
		if err != nil {
			slog.Warn("get class failed", "section", i, "err", err)
			continue
		}

		if !strings.Contains(classAttr, "collapse show") {
			// Expand collapsed section
			if err := s.b.ClickChild(".course-section-header", i, "a", 0); err != nil {
				slog.Warn("expand section failed", "section", i, "err", err)
				continue
			}
		}

		if err := s.findActivity(i); err != nil {
			slog.Warn("find activity failed", "section", i, "err", err)
		}
	}
	return nil
}

func (s *SAIA) findActivity(sectionIdx int) error {
	activityCount, err := s.b.CountElementsInParent(".course-content-item-content", sectionIdx, ".aalink.stretched-link")
	if err != nil {
		return fmt.Errorf("count activities: %w", err)
	}

	for i := 0; i < activityCount; i++ {
		// Re-query each iteration to avoid stale references
		titleText, err := s.b.GetChildText(".course-content-item-content", sectionIdx, ".text-uppercase.small", i)
		if err != nil {
			slog.Warn("get activity title failed", "err", err)
			continue
		}
		if isUndesired(strings.TrimSpace(titleText)) {
			continue
		}

		if err := s.b.ClickChild(".course-content-item-content", sectionIdx, ".aalink.stretched-link", i); err != nil {
			slog.Error("click activity failed", "err", err)
			return err
		}

		if !s.b.ElementExists(".description-inner") {
			slog.Warn("DEADLINE NOT FOUND ON CURRENT ACTIVITY, REDIRECTING...")
			s.b.GoBack() //nolint:errcheck
			continue
		}

		deadlineText, err := s.b.GetText(".description-inner")
		if err != nil {
			s.b.GoBack() //nolint:errcheck
			continue
		}

		// Get the strong text in the last div (used as prefix to strip)
		itemToRemove, err := s.b.EvalJSString(`
			(function(){
				var divs=document.querySelector('.description-inner').querySelectorAll('div');
				var last=divs[divs.length-1];
				var strong=last.querySelector('strong');
				return strong?strong.innerText.trim():'';
			})()
		`)
		if err != nil || !strings.Contains(deadlineText, itemToRemove) {
			s.b.GoBack() //nolint:errcheck
			continue
		}

		activityDateText, err := s.b.EvalJSString(`
			(function(){
				var divs=document.querySelector('.description-inner').querySelectorAll('div');
				return divs[divs.length-1].innerText.trim();
			})()
		`)
		if err != nil {
			s.b.GoBack() //nolint:errcheck
			continue
		}

		newActivityDate := activityDateText
		if prefix := itemToRemove + " "; strings.HasPrefix(activityDateText, prefix) {
			newActivityDate = activityDateText[len(prefix):]
		}

		activityDesc, err := s.b.GetText(".page-header-headings")
		if err != nil {
			s.b.GoBack() //nolint:errcheck
			continue
		}
		activityDesc = strings.ReplaceAll(strings.TrimSpace(activityDesc), "\n", " ")

		subjectTitle, err := s.b.EvalJSString(`document.querySelector('.breadcrumb-item a').innerText.trim()`)
		if err != nil {
			s.b.GoBack() //nolint:errcheck
			continue
		}

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

		time.Sleep(time.Second)
		s.b.GoBack() //nolint:errcheck
	}
	return nil
}

func isUndesired(title string) bool {
	for _, u := range config.UndesiredActivities {
		if u == title {
			return true
		}
	}
	return false
}
