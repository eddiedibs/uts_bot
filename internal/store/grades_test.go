package store

import "testing"

func TestGradeItemKeyNormalizesCosmeticDifferences(t *testing.T) {
	t.Parallel()

	a := GradeItemKey("item", []string{"Computación para Ingenieros", "Corte I"}, "  Tarea   1 ")
	b := GradeItemKey("item", []string{"Computación para Ingenieros", "Corte I"}, "TAREA 1")
	if a != b {
		t.Fatalf("keys differ on casing/whitespace only:\n a = %q\n b = %q", a, b)
	}
}

func TestGradeItemKeySeparatesRepeatedSubtotalNames(t *testing.T) {
	t.Parallel()

	// Moodle names every category subtotal identically, so the category path must be part of
	// the key or these rows overwrite each other.
	first := GradeItemKey("subtotal", []string{"Materia", "Corte I"}, "Total de la categoría")
	second := GradeItemKey("subtotal", []string{"Materia", "Corte II"}, "Total de la categoría")
	if first == second {
		t.Fatalf("subtotals in different categories collided on key %q", first)
	}
}

func TestGradeItemKeyDistinguishesRowTypes(t *testing.T) {
	t.Parallel()

	item := GradeItemKey("item", []string{"Materia"}, "Corte I")
	category := GradeItemKey("category", []string{"Materia"}, "Corte I")
	if item == category {
		t.Fatalf("item and category collided on key %q", item)
	}
}

func TestHasGradeValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		grade string
		want  bool
	}{
		{"8,00", true},
		{"0,00", true},
		{"-", false},
		{" - ", false},
		{"", false},
		{"   ", false},
	}
	for _, tt := range tests {
		if got := hasGradeValue(tt.grade); got != tt.want {
			t.Errorf("hasGradeValue(%q) = %v, want %v", tt.grade, got, tt.want)
		}
	}
}

func TestGradeEventOnlyFiresForItemsAndCourseTotal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rowType   string
		existing  map[string]string
		grade     string
		wantEvent bool
		wantMode  string
	}{
		{
			name:      "new item with a mark is news",
			rowType:   GradeRowTypeItem,
			existing:  map[string]string{},
			grade:     "8,00",
			wantEvent: true,
			wantMode:  ChangeTypeNew,
		},
		{
			name:      "new ungraded item is only scaffolding",
			rowType:   GradeRowTypeItem,
			existing:  map[string]string{},
			grade:     "-",
			wantEvent: false,
		},
		{
			name:      "ungraded item becoming graded is an update",
			rowType:   GradeRowTypeItem,
			existing:  map[string]string{"k": "-"},
			grade:     "8,00",
			wantEvent: true,
			wantMode:  ChangeTypeUpdated,
		},
		{
			name:      "unchanged grade stays silent",
			rowType:   GradeRowTypeItem,
			existing:  map[string]string{"k": "8,00"},
			grade:     "8,00",
			wantEvent: false,
		},
		{
			name:      "course total is worth notifying",
			rowType:   GradeRowTypeCourseTotal,
			existing:  map[string]string{"k": "30,00"},
			grade:     "40,25",
			wantEvent: true,
			wantMode:  ChangeTypeUpdated,
		},
		{
			name:      "category rows stay silent",
			rowType:   GradeRowTypeCategory,
			existing:  map[string]string{},
			grade:     "12,00",
			wantEvent: false,
		},
		{
			name:      "subtotal rows stay silent",
			rowType:   GradeRowTypeSubtotal,
			existing:  map[string]string{"k": "10,00"},
			grade:     "12,00",
			wantEvent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row := GradeUpsert{RowType: tt.rowType, ItemName: "Tarea 1", Grade: tt.grade}
			ev, ok := gradeEvent(tt.existing, row, 42, "k", "Tarea 1", tt.grade, "Materia")
			if ok != tt.wantEvent {
				t.Fatalf("gradeEvent emitted = %v, want %v", ok, tt.wantEvent)
			}
			if !ok {
				return
			}
			if ev.ChangeType != tt.wantMode {
				t.Errorf("ChangeType = %q, want %q", ev.ChangeType, tt.wantMode)
			}
			if ev.EventType != EventTypeGrade {
				t.Errorf("EventType = %q, want %q", ev.EventType, EventTypeGrade)
			}
			if ev.CourseViewID == nil || *ev.CourseViewID != 42 {
				t.Errorf("CourseViewID = %v, want 42", ev.CourseViewID)
			}
		})
	}
}

func TestActivityEventDetectsNewRenamedAndRelinked(t *testing.T) {
	t.Parallel()

	existing := map[uint32]activityFingerprint{
		10: {name: "Tarea 1", link: "https://saia/mod/assign/view.php?id=10"},
		11: {name: "Tarea 2", link: "https://saia/mod/assign/view.php?id=11"},
		12: {name: "Tarea 3", link: "https://saia/mod/assign/view.php?id=12"},
	}

	if _, ok := activityEvent(existing, 99, "Nueva", "https://saia/new", nil, "Materia"); !ok {
		t.Error("unknown activity should emit an event")
	}
	if ev, ok := activityEvent(existing, 10, "Tarea 1 (corregida)", existing[10].link, nil, "Materia"); !ok {
		t.Error("renamed activity should emit an event")
	} else if ev.ChangeType != ChangeTypeUpdated {
		t.Errorf("ChangeType = %q, want %q", ev.ChangeType, ChangeTypeUpdated)
	}
	if _, ok := activityEvent(existing, 11, "Tarea 2", "https://saia/moved", nil, "Materia"); !ok {
		t.Error("relinked activity should emit an event")
	}
	if _, ok := activityEvent(existing, 12, "Tarea 3", existing[12].link, nil, "Materia"); ok {
		t.Error("unchanged activity should stay silent")
	}
}
