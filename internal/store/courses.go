package store

import (
	"context"
	"database/sql"
	"fmt"
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
	return scanCourses(rows)
}

// CourseNameByMoodleID looks up one course's name, for resolving a human label from a bare
// Moodle course id (course/view.php?id=).
func CourseNameByMoodleID(ctx context.Context, db *sql.DB, moodleCourseID int) (string, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM courses WHERE moodle_course_id = ?`, moodleCourseID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("query course %d: %w", moodleCourseID, err)
	}
	return name, nil
}

// maxCourseMisses is how many consecutive discovery cycles a stored course may go unseen before
// SyncCourses treats it as genuinely gone. A single miss is far more often a flaky/partial
// Moodle render (see the README's "Scraper limitations" note) than a real unenrollment, and
// deleting on the first miss cascades into that course's activities and activities_attachments,
// which then get re-scraped as "new" on the next successful cycle — duplicate notifications for
// unchanged content.
const maxCourseMisses = 3

// courseRow is one stored courses row plus its miss streak, used only inside SyncCourses.
type courseRow struct {
	Course
	missCount uint32
}

// SyncCourses makes the courses table match discovered: every discovered course is upserted and
// its miss streak reset, and any stored course absent from discovered for maxCourseMisses cycles
// in a row is deleted along with the activities and grades scraped for it. This is what lets a
// course that disappears from the Moodle dashboard (the term ended, the student was unenrolled)
// drop out of the DB on its own instead of lingering forever.
//
// As a safety valve against a partial Moodle render being mistaken for mass unenrollment,
// deletions are withheld entirely — guardSkipped is true — when discovered is less than half the
// size of what is already stored. Additions and miss-streak bumps still apply either way, so a
// genuinely new course is never held back by this guard.
func SyncCourses(ctx context.Context, tx *sql.Tx, discovered []Course) (added, removed, stillMissing []Course, guardSkipped bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT moodle_course_id, name, miss_count FROM courses ORDER BY name`)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("query courses: %w", err)
	}
	existing, err := scanCourseRows(rows)
	if err != nil {
		return nil, nil, nil, false, err
	}
	existingByID := make(map[int]courseRow, len(existing))
	for _, c := range existing {
		existingByID[c.MoodleID] = c
	}

	keep := make(map[int]bool, len(discovered))
	for _, c := range discovered {
		keep[c.MoodleID] = true
		if _, ok := existingByID[c.MoodleID]; !ok {
			added = append(added, c)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO courses (moodle_course_id, name, miss_count) VALUES (?, ?, 0)
ON DUPLICATE KEY UPDATE name = VALUES(name), miss_count = 0`,
			c.MoodleID, c.Name,
		); err != nil {
			return nil, nil, nil, false, fmt.Errorf("upsert course %d: %w", c.MoodleID, err)
		}
	}

	if len(existing) >= 2 && len(discovered)*2 < len(existing) {
		return added, nil, nil, true, nil
	}

	for _, c := range existing {
		if keep[c.MoodleID] {
			continue
		}
		miss := c.missCount + 1
		if miss < maxCourseMisses {
			if _, err := tx.ExecContext(ctx, `UPDATE courses SET miss_count = ? WHERE moodle_course_id = ?`, miss, c.MoodleID); err != nil {
				return nil, nil, nil, false, fmt.Errorf("bump miss_count for course %d: %w", c.MoodleID, err)
			}
			stillMissing = append(stillMissing, c.Course)
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM courses WHERE moodle_course_id = ?`, c.MoodleID); err != nil {
			return nil, nil, nil, false, fmt.Errorf("delete course %d: %w", c.MoodleID, err)
		}
		removed = append(removed, c.Course)
	}

	// Sweep every activities/grades row whose course_view_id has no matching courses row, not
	// just the ones removed above: this is what makes the DB self-heal from an orphan left by
	// any other cause (e.g. a concurrent sync elsewhere racing this transaction) instead of
	// requiring that exact course to reappear and disappear again to get cleaned up.
	if err := deleteOrphanedActivitiesAndGrades(ctx, tx); err != nil {
		return nil, nil, nil, false, err
	}

	return added, removed, stillMissing, false, nil
}

// deleteOrphanedActivitiesAndGrades removes rows whose course_view_id no longer has a matching
// courses row. activities_attachments cascades off activities via its foreign key.
func deleteOrphanedActivitiesAndGrades(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
DELETE FROM activities
WHERE course_view_id IS NOT NULL
  AND course_view_id NOT IN (SELECT moodle_course_id FROM courses)`); err != nil {
		return fmt.Errorf("delete orphaned activities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM grades
WHERE course_view_id NOT IN (SELECT moodle_course_id FROM courses)`); err != nil {
		return fmt.Errorf("delete orphaned grades: %w", err)
	}
	return nil
}

func scanCourses(rows *sql.Rows) ([]Course, error) {
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

func scanCourseRows(rows *sql.Rows) ([]courseRow, error) {
	defer rows.Close()
	var out []courseRow
	for rows.Next() {
		var c courseRow
		if err := rows.Scan(&c.MoodleID, &c.Name, &c.missCount); err != nil {
			return nil, fmt.Errorf("scan course: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
