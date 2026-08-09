package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxGradeItemKeyLen  = 512
	maxGradeItemNameLen = 512
	maxGradeRowTypeLen  = 32
	maxGradeCellLen     = 64
	maxGradeCatPathLen  = 1024
	maxGradeLinkLen     = 2048
)

// Grade row_type values produced by the Moodle report parser.
const (
	GradeRowTypeCategory    = "category"
	GradeRowTypeItem        = "item"
	GradeRowTypeSubtotal    = "subtotal"
	GradeRowTypeCourseTotal = "course_total"
)

// GradeUpsert is one parsed grade-report row destined for the grades table. It mirrors
// saia.GradeRow without importing it, since saia already imports store.
type GradeUpsert struct {
	RowType      string
	CategoryPath []string
	ActivityType string
	ItemName     string
	Grade        string
	Percentage   string
	Weight       string
	Link         string
}

// UpsertGrades persists every parsed row for one course and returns OutboxEvents only for the
// rows a student would want a message about: individual items and the course total. Category and
// subtotal rows are stored (they give the report structure) but stay silent, because they move
// mechanically whenever any child item changes and would double every notification.
//
// An event is emitted when the item is new, or when its grade string changed — which is what
// makes the common "-" to "8,00" transition show up as a real grade posting.
func UpsertGrades(ctx context.Context, db *sql.DB, courseViewID uint32, courseName string, rows []GradeUpsert) ([]OutboxEvent, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	existing, err := selectGradeValues(ctx, tx, courseViewID)
	if err != nil {
		return nil, err
	}

	const q = `
INSERT INTO grades
	(course_view_id, item_key, item_name, row_type, activity_type, category_path, grade, percentage, weight, link)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
	item_name = VALUES(item_name),
	row_type = VALUES(row_type),
	activity_type = VALUES(activity_type),
	category_path = VALUES(category_path),
	grade = VALUES(grade),
	percentage = VALUES(percentage),
	weight = VALUES(weight),
	link = VALUES(link)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	var events []OutboxEvent
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		itemName := strings.TrimSpace(row.ItemName)
		if itemName == "" {
			continue
		}
		key := GradeItemKey(row.RowType, row.CategoryPath, itemName)
		if seen[key] {
			continue
		}
		seen[key] = true

		grade := truncateRunes(row.Grade, maxGradeCellLen)
		catPath := truncateRunes(strings.Join(row.CategoryPath, " / "), maxGradeCatPathLen)
		if _, err := stmt.ExecContext(ctx,
			courseViewID,
			key,
			truncateRunes(itemName, maxGradeItemNameLen),
			truncateRunes(row.RowType, maxGradeRowTypeLen),
			truncateRunes(row.ActivityType, maxGradeCellLen),
			catPath,
			grade,
			truncateRunes(row.Percentage, maxGradeCellLen),
			truncateRunes(row.Weight, maxGradeCellLen),
			truncateRunes(row.Link, maxGradeLinkLen),
		); err != nil {
			return nil, fmt.Errorf("upsert grade %d/%s: %w", courseViewID, key, err)
		}
		if ev, ok := gradeEvent(existing, row, courseViewID, key, itemName, grade, courseName); ok {
			events = append(events, ev)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return events, nil
}

func gradeEvent(existing map[string]string, row GradeUpsert, courseViewID uint32, key, itemName, grade, courseName string) (OutboxEvent, bool) {
	if row.RowType != GradeRowTypeItem && row.RowType != GradeRowTypeCourseTotal {
		return OutboxEvent{}, false
	}
	prev, found := existing[key]
	if found && prev == grade {
		return OutboxEvent{}, false
	}
	// A brand-new item that has no grade yet is just report scaffolding, not news.
	if !found && !hasGradeValue(grade) {
		return OutboxEvent{}, false
	}

	changeType := ChangeTypeUpdated
	if !found {
		changeType = ChangeTypeNew
	}
	detail := grade
	if !hasGradeValue(grade) {
		detail = "sin nota"
	}
	if row.Percentage != "" {
		detail += " (" + row.Percentage + ")"
	}

	cv := courseViewID
	return OutboxEvent{
		EventType:    EventTypeGrade,
		ChangeType:   changeType,
		CourseViewID: &cv,
		CourseName:   courseName,
		EntityKey:    strconv.FormatUint(uint64(courseViewID), 10) + ":" + key,
		Title:        itemName,
		Detail:       detail,
		URL:          row.Link,
	}, true
}

// hasGradeValue reports whether a Moodle grade cell holds an actual mark. Ungraded items render
// as "-" or an empty cell.
func hasGradeValue(grade string) bool {
	trimmed := strings.TrimSpace(grade)
	return trimmed != "" && trimmed != "-"
}

func selectGradeValues(ctx context.Context, tx *sql.Tx, courseViewID uint32) (map[string]string, error) {
	const q = `SELECT item_key, grade FROM grades WHERE course_view_id = ?`
	rows, err := tx.QueryContext(ctx, q, courseViewID)
	if err != nil {
		return nil, fmt.Errorf("select existing grades: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, grade string
		if err := rows.Scan(&key, &grade); err != nil {
			return nil, fmt.Errorf("scan existing grade: %w", err)
		}
		out[key] = grade
	}
	return out, rows.Err()
}

// GradeItemKey builds the stable primary-key component for a grade row. The category path is
// part of the key because Moodle repeats generic names like "Total de la categoría" once per
// category, and keying on the name alone would make those rows overwrite each other.
func GradeItemKey(rowType string, categoryPath []string, itemName string) string {
	parts := make([]string, 0, len(categoryPath)+2)
	parts = append(parts, normalizeGradeKeyPart(rowType))
	parts = append(parts, normalizeGradeKeyPart(strings.Join(categoryPath, "/")))
	parts = append(parts, normalizeGradeKeyPart(itemName))
	return truncateRunes(strings.Join(parts, "|"), maxGradeItemKeyLen)
}

// normalizeGradeKeyPart lowercases and collapses whitespace so cosmetic Moodle formatting
// changes do not look like a new grade item.
func normalizeGradeKeyPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
