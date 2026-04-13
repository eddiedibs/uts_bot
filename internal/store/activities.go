package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	MoodleCourseID  int             `json:"moodle_course_id"`
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
	Name            string
	Link            string
	ActivityContent json.RawMessage
}

// UpsertActivities inserts or updates rows by primary key moodle_course_id.
func UpsertActivities(ctx context.Context, db *sql.DB, rows []ActivityUpsert) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	const q = `
INSERT INTO activities (moodle_course_id, name, link, activity_content)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	name = VALUES(name),
	link = VALUES(link),
	activity_content = VALUES(activity_content)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		name := truncateRunes(row.Name, maxActivityNameLen)
		link := truncateRunes(row.Link, maxActivityLinkLen)
		if _, err := stmt.ExecContext(ctx, row.MoodleCourseID, name, link, row.ActivityContent); err != nil {
			return fmt.Errorf("upsert activity %d: %w", row.MoodleCourseID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListActivities returns all activity rows (requires migrations through 000005 for timestamp columns).
func ListActivities(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT moodle_course_id, name, link, activity_content, created_at, updated_at
FROM activities ORDER BY updated_at DESC, moodle_course_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query activities: %w", err)
	}
	defer rows.Close()

	out := make([]Activity, 0)
	for rows.Next() {
		var a Activity
		var createdAt, updatedAt flexTime
		if err := rows.Scan(&a.MoodleCourseID, &a.Name, &a.Link, &a.ActivityContent, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		a.CreatedAt = createdAt.t
		a.UpdatedAt = updatedAt.t
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
