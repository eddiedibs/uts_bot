package store

import (
	"context"
	"database/sql"
	"fmt"

	"uts_bot/internal/saia"
)

// Course is a Moodle course row returned by the API.
type Course struct {
	MoodleID int    `json:"moodle_course_id"`
	Name     string `json:"name"`
}

// ListCourses returns all rows from the global courses table.
func ListCourses(ctx context.Context, db *sql.DB) ([]Course, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT moodle_course_id, name FROM courses ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query courses: %w", err)
	}
	defer rows.Close()

	var out []Course
	for rows.Next() {
		var c Course
		if err := rows.Scan(&c.MoodleID, &c.Name); err != nil {
			return nil, fmt.Errorf("scan course: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SeedCoursesFromStatic upserts the static SAIA course list into courses.
func SeedCoursesFromStatic(ctx context.Context, tx *sql.Tx) error {
	for _, c := range saia.UFTMoodleCourses {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO courses (moodle_course_id, name) VALUES (?, ?)
ON DUPLICATE KEY UPDATE name = VALUES(name)`,
			c.MoodleID, c.Name,
		); err != nil {
			return fmt.Errorf("seed course %d: %w", c.MoodleID, err)
		}
	}
	return nil
}
