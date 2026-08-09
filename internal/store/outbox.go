package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Outbox event_type values (must match the ENUM in migration 000009).
const (
	EventTypeActivity   = "activity"
	EventTypeAttachment = "attachment"
	EventTypeGrade      = "grade"
)

// Outbox change_type values (must match the ENUM in migration 000009).
const (
	ChangeTypeNew     = "new"
	ChangeTypeUpdated = "updated"
)

const (
	maxOutboxEntityKeyLen = 768
	maxOutboxTitleLen     = 512
	maxOutboxDetailLen    = 1024
	maxOutboxURLLen       = 2048
	maxOutboxCourseName   = 512
)

// outboxInsertBatchSize keeps the multi-row INSERT well under MySQL's max_allowed_packet
// and placeholder limits even when a first-ever scrape produces hundreds of events.
const outboxInsertBatchSize = 100

// OutboxEvent is one pending notification for uts_notifier to pick up. The columns map 1:1
// onto the event dict the notifier already builds, so its phrasing code needs no changes.
type OutboxEvent struct {
	EventType    string
	ChangeType   string
	CourseViewID *uint32
	CourseName   string
	// EntityKey identifies the source row (for debugging and dedup), e.g. "12345" for an
	// activity cmid or "12345/syllabus.pdf" for an attachment.
	EntityKey string
	Title     string
	Detail    string
	URL       string
}

// InsertOutboxEvents writes all events in a single transaction so a scrape cycle becomes
// atomically visible to the notifier, which lets it send one grouped message per cycle
// instead of a partial batch per poll.
func InsertOutboxEvents(ctx context.Context, db *sql.DB, events []OutboxEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for start := 0; start < len(events); start += outboxInsertBatchSize {
		end := min(start+outboxInsertBatchSize, len(events))
		if err := insertOutboxBatch(ctx, tx, events[start:end]); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func insertOutboxBatch(ctx context.Context, tx *sql.Tx, events []OutboxEvent) error {
	placeholders := make([]string, 0, len(events))
	args := make([]any, 0, len(events)*8)
	for _, e := range events {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?)")
		var cv any
		if e.CourseViewID != nil {
			cv = *e.CourseViewID
		}
		args = append(args,
			e.EventType,
			e.ChangeType,
			cv,
			truncateRunes(e.CourseName, maxOutboxCourseName),
			truncateRunes(e.EntityKey, maxOutboxEntityKeyLen),
			truncateRunes(e.Title, maxOutboxTitleLen),
			truncateRunes(e.Detail, maxOutboxDetailLen),
			truncateRunes(e.URL, maxOutboxURLLen),
		)
	}
	q := `
INSERT INTO notification_outbox
	(event_type, change_type, course_view_id, course_name, entity_key, title, detail, url)
VALUES ` + strings.Join(placeholders, ", ")

	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("insert %d outbox events: %w", len(events), err)
	}
	return nil
}

// PruneConsumedOutboxEvents deletes delivered events older than retentionDays. Unconsumed rows
// are never touched, so a notifier that has been down for a long time still gets its backlog.
func PruneConsumedOutboxEvents(ctx context.Context, db *sql.DB, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	const q = `
DELETE FROM notification_outbox
WHERE consumed_at IS NOT NULL
  AND consumed_at < NOW() - INTERVAL ? DAY`

	res, err := db.ExecContext(ctx, q, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("prune outbox: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
