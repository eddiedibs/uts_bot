package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrAttachmentNotFound is returned when no attachment matches the lookup keys.
var ErrAttachmentNotFound = errors.New("attachment not found")

// ErrAttachmentAmbiguous is returned when course_id + file_name matches more than one row.
var ErrAttachmentAmbiguous = errors.New("multiple attachments match; pass activity_id to disambiguate")

// ActivityAttachment is a row from the activities_attachments table.
type ActivityAttachment struct {
	MoodleCourseID int    `json:"moodle_course_id"`
	FileName       string `json:"file_name"`
	FileContent    string `json:"file_content"`
}

// ActivityAttachmentUpsert is one row for the activities_attachments table.
type ActivityAttachmentUpsert struct {
	MoodleCourseID uint32
	FileName       string
	FileContent    string
}

// UpsertActivityAttachments inserts or updates attachment rows by primary key (moodle_course_id, file_name).
func UpsertActivityAttachments(ctx context.Context, db *sql.DB, rows []ActivityAttachmentUpsert) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	const q = `
INSERT INTO activities_attachments (moodle_course_id, file_name, file_content)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE
	file_content = VALUES(file_content)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, row.MoodleCourseID, row.FileName, row.FileContent); err != nil {
			return fmt.Errorf("upsert attachment %d/%s: %w", row.MoodleCourseID, row.FileName, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListAttachmentsByActivityIDs returns all attachments for the given moodle_course_ids,
// keyed by moodle_course_id.
func ListAttachmentsByActivityIDs(ctx context.Context, db *sql.DB, ids []int) (map[int][]ActivityAttachment, error) {
	if len(ids) == 0 {
		return map[int][]ActivityAttachment{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(
		`SELECT moodle_course_id, file_name, file_content FROM activities_attachments WHERE moodle_course_id IN (%s)`,
		strings.Join(placeholders, ","),
	)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query attachments: %w", err)
	}
	defer rows.Close()

	out := make(map[int][]ActivityAttachment)
	for rows.Next() {
		var a ActivityAttachment
		if err := rows.Scan(&a.MoodleCourseID, &a.FileName, &a.FileContent); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		out[a.MoodleCourseID] = append(out[a.MoodleCourseID], a)
	}
	return out, rows.Err()
}

// AttachmentSummary is a lightweight attachment row (no file body).
type AttachmentSummary struct {
	FileName  string    `json:"file_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListAttachmentSummariesByCourseViewID returns attachment names and timestamps for all activities
// belonging to the given Moodle course (course/view.php?id=). Requires activities.course_view_id populated.
func ListAttachmentSummariesByCourseViewID(ctx context.Context, db *sql.DB, courseViewID int) ([]AttachmentSummary, error) {
	const q = `
SELECT aa.file_name, aa.created_at, aa.updated_at
FROM activities_attachments aa
INNER JOIN activities a ON a.moodle_course_id = aa.moodle_course_id
WHERE a.course_view_id = ?
ORDER BY aa.updated_at DESC, aa.file_name`
	rows, err := db.QueryContext(ctx, q, courseViewID)
	if err != nil {
		return nil, fmt.Errorf("query attachment summaries: %w", err)
	}
	defer rows.Close()

	var out []AttachmentSummary
	for rows.Next() {
		var s AttachmentSummary
		var cAt, uAt flexTime
		if err := rows.Scan(&s.FileName, &cAt, &uAt); err != nil {
			return nil, fmt.Errorf("scan attachment summary: %w", err)
		}
		s.CreatedAt = cAt.t
		s.UpdatedAt = uAt.t
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []AttachmentSummary{}
	}
	return out, nil
}

// AttachmentContentRow is a full attachment payload for API responses.
type AttachmentContentRow struct {
	ActivityMoodleID int       `json:"activity_id"`
	FileName         string    `json:"file_name"`
	FileContent      string    `json:"file_content"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GetAttachmentByActivityAndFileName loads file_content by primary key (moodle_course_id = activity cmid, file_name).
func GetAttachmentByActivityAndFileName(ctx context.Context, db *sql.DB, activityMoodleID int, fileName string) (AttachmentContentRow, error) {
	const q = `SELECT file_content, created_at, updated_at FROM activities_attachments WHERE moodle_course_id = ? AND file_name = ?`
	var row AttachmentContentRow
	row.ActivityMoodleID = activityMoodleID
	row.FileName = fileName
	var cAt, uAt flexTime
	err := db.QueryRowContext(ctx, q, activityMoodleID, fileName).Scan(&row.FileContent, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentContentRow{}, ErrAttachmentNotFound
	}
	if err != nil {
		return AttachmentContentRow{}, fmt.Errorf("query attachment: %w", err)
	}
	row.CreatedAt = cAt.t
	row.UpdatedAt = uAt.t
	return row, nil
}

// GetAttachmentByCourseViewIDAndFileName loads an attachment when file_name is unique within that Moodle course.
func GetAttachmentByCourseViewIDAndFileName(ctx context.Context, db *sql.DB, courseViewID int, fileName string) (AttachmentContentRow, error) {
	const q = `
SELECT aa.moodle_course_id, aa.file_name, aa.file_content, aa.created_at, aa.updated_at
FROM activities_attachments aa
INNER JOIN activities a ON a.moodle_course_id = aa.moodle_course_id
WHERE a.course_view_id = ? AND aa.file_name = ?`
	rows, err := db.QueryContext(ctx, q, courseViewID, fileName)
	if err != nil {
		return AttachmentContentRow{}, fmt.Errorf("query attachment by course: %w", err)
	}
	defer rows.Close()

	var out []AttachmentContentRow
	for rows.Next() {
		var row AttachmentContentRow
		var cAt, uAt flexTime
		if err := rows.Scan(&row.ActivityMoodleID, &row.FileName, &row.FileContent, &cAt, &uAt); err != nil {
			return AttachmentContentRow{}, fmt.Errorf("scan attachment: %w", err)
		}
		row.CreatedAt = cAt.t
		row.UpdatedAt = uAt.t
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return AttachmentContentRow{}, err
	}
	switch len(out) {
	case 0:
		return AttachmentContentRow{}, ErrAttachmentNotFound
	case 1:
		return out[0], nil
	default:
		return AttachmentContentRow{}, ErrAttachmentAmbiguous
	}
}
