package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxActivityNameLen = 512
	maxActivityLinkLen = 2048
)

// Activity is a row from the activities table (API / DB round-trip).
type Activity struct {
	// MoodleCourseID is the activity / course-module id from mod URLs (?id=cmid), not the course page id.
	MoodleCourseID int `json:"moodle_course_id"`
	// CourseViewID is the Moodle course id from course/view.php?id= (nil if row predates column).
	CourseViewID    *int            `json:"course_view_id,omitempty"`
	CourseName      string          `json:"course_name"`
	Name            string          `json:"name"`
	Link            string          `json:"link"`
	ActivityContent json.RawMessage `json:"activity_content"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// ActivityUpsert is one row for the activities table (moodle_course_id = course module id from ?id=).
type ActivityUpsert struct {
	MoodleCourseID  uint32
	CourseViewID    *uint32 // Moodle course id (course/view.php?id=); nil stores SQL NULL
	Name            string
	Link            string
	ActivityContent json.RawMessage
}

// UpsertActivities inserts or updates rows by primary key moodle_course_id and returns one
// OutboxEvent per genuinely new or changed activity, tagged with courseName.
//
// Change detection compares name and link against the stored row inside the same transaction.
// It deliberately ignores activity_content, which is re-parsed from HTML on every scrape and so
// churns constantly. Comparing beats reading RowsAffected(), whose 0/1/2 encoding silently
// breaks if clientFoundRows=true is ever added to the DSN.
func UpsertActivities(ctx context.Context, db *sql.DB, rows []ActivityUpsert, courseName string) ([]OutboxEvent, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	ids := make([]uint32, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.MoodleCourseID)
	}
	existing, err := selectActivityFingerprints(ctx, tx, ids)
	if err != nil {
		return nil, err
	}

	const q = `
INSERT INTO activities (moodle_course_id, course_view_id, name, link, activity_content)
VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	course_view_id = VALUES(course_view_id),
	name = VALUES(name),
	link = VALUES(link),
	activity_content = VALUES(activity_content)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	var events []OutboxEvent
	for _, row := range rows {
		name := truncateRunes(row.Name, maxActivityNameLen)
		link := truncateRunes(row.Link, maxActivityLinkLen)
		var cv any
		if row.CourseViewID != nil {
			cv = *row.CourseViewID
		}
		if _, err := stmt.ExecContext(ctx, row.MoodleCourseID, cv, name, link, row.ActivityContent); err != nil {
			return nil, fmt.Errorf("upsert activity %d: %w", row.MoodleCourseID, err)
		}
		if ev, ok := activityEvent(existing, row.MoodleCourseID, name, link, row.CourseViewID, courseName); ok {
			events = append(events, ev)
		}
		// Record what we just wrote so a cmid linked twice on the same course page does not
		// produce two events.
		existing[row.MoodleCourseID] = activityFingerprint{name: name, link: link}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return events, nil
}

// activityFingerprint holds the notification-relevant columns of a stored activity.
type activityFingerprint struct {
	name string
	link string
}

func selectActivityFingerprints(ctx context.Context, tx *sql.Tx, ids []uint32) (map[uint32]activityFingerprint, error) {
	out := make(map[uint32]activityFingerprint, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT moodle_course_id, name, link FROM activities WHERE moodle_course_id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select existing activities: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uint32
		var fp activityFingerprint
		if err := rows.Scan(&id, &fp.name, &fp.link); err != nil {
			return nil, fmt.Errorf("scan existing activity: %w", err)
		}
		out[id] = fp
	}
	return out, rows.Err()
}

func activityEvent(existing map[uint32]activityFingerprint, id uint32, name, link string, courseViewID *uint32, courseName string) (OutboxEvent, bool) {
	ev := OutboxEvent{
		EventType:    EventTypeActivity,
		CourseViewID: courseViewID,
		CourseName:   courseName,
		EntityKey:    strconv.FormatUint(uint64(id), 10),
		Title:        name,
		URL:          link,
	}
	prev, found := existing[id]
	switch {
	case !found:
		ev.ChangeType = ChangeTypeNew
	case prev.name != name:
		ev.ChangeType = ChangeTypeUpdated
		ev.Detail = "Renombrada (antes: " + prev.name + ")"
	case prev.link != link:
		ev.ChangeType = ChangeTypeUpdated
		ev.Detail = "Enlace actualizado"
	default:
		return OutboxEvent{}, false
	}
	return ev, true
}

// ListActivities returns activity rows. If courseViewID is non-nil, only rows for that Moodle course id
// (course/view.php?id=) are returned. Requires migration 000006 for course_view_id filtering.
func ListActivities(ctx context.Context, db *sql.DB, courseViewID *int) ([]Activity, error) {
	q := `SELECT moodle_course_id, course_view_id, name, link, activity_content, created_at, updated_at
FROM activities`
	var args []any
	if courseViewID != nil {
		q += ` WHERE course_view_id = ?`
		args = append(args, *courseViewID)
	}
	q += ` ORDER BY updated_at DESC, moodle_course_id`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query activities: %w", err)
	}
	defer rows.Close()

	out := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		var createdAt, updatedAt flexTime
		var courseView sql.NullInt64
		if err := rows.Scan(&a.MoodleCourseID, &courseView, &a.Name, &a.Link, &a.ActivityContent, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		a.CreatedAt = createdAt.t
		a.UpdatedAt = updatedAt.t
		if courseView.Valid {
			v := int(courseView.Int64)
			a.CourseViewID = &v
		}
		a.CourseName = courseNameFromActivityContent(a.ActivityContent)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func courseNameFromActivityContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var meta struct {
		CourseName string `json:"course_name"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return meta.CourseName
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

// flexTime implements sql.Scanner for MySQL TIMESTAMP/DATETIME. Works with or without
// parseTime=true on the DSN ([]uint8 from driver vs time.Time).
type flexTime struct{ t time.Time }

func (f *flexTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		f.t = time.Time{}
		return nil
	case time.Time:
		f.t = v
		return nil
	case []byte:
		return f.parseMySQL(string(v))
	case string:
		return f.parseMySQL(v)
	default:
		return fmt.Errorf("flexTime: unsupported type %T", src)
	}
}

func (f *flexTime) parseMySQL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		f.t = time.Time{}
		return nil
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC3339Nano,
		time.RFC3339,
	}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			f.t = t
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("parse mysql time %q: %w", s, lastErr)
}
