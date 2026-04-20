package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

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
