package saia

import (
	"testing"
)

const sampleUserGradeTable = `<table class="table generaltable user-grade">
<tbody>
<tr class="" data-hidden="false">
<th class="level1 d1 b1b b1t category column-itemname cell c0 lastcol" colspan="9" scope="row"><div class="d-flex category-content">
    <span>Computación para Ingenieros</span>
</div></th>
</tr>
<tr class="cat_24916" data-hidden="false">
<th class="level2 d2 b1b b1t category column-itemname cell c0 lastcol" colspan="8" scope="row"><div class="d-flex category-content">
    <span>Corte I</span>
</div></th>
</tr>
<tr class="cat_24916 cat_25602" data-hidden="false">
<th class="level3 item b1b column-itemname cell c0" scope="row"><div class="item"><div><span class="d-block text-uppercase small" title="Tarea">Tarea</span><div class="rowtitle"><span class="gradeitemheader" title="Asignación practica">Asignación practica</span></div></div></div></th>
<td class="column-weight cell c1">66,67 %</td>
<td class="gradepass column-grade cell c2"><i class="icon fa fa-check text-success fa-fw inline"></i>8,00</td>
<td class="column-range cell c3">0–10</td>
<td class="column-percentage cell c4">80,00 %</td>
<td class="column-feedback cell c5"><p>Análisis :2/3</p></td>
<td class="column-contributiontocoursetotal cell c6">13,33 %</td>
</tr>
<tr class="cat_24916 cat_25602" data-hidden="false">
<th class="level2 d2 baggt b2b column-itemname cell c0" scope="row"><div class="categoryitem"><span class="gradeitemheader" title="Total Corte I">Total Corte I</span></div></th>
<td class="column-weight cell c1">25,00 %</td>
<td class="column-grade cell c2">12,00</td>
<td class="column-range cell c3">0–15</td>
<td class="column-percentage cell c4">80,00 %</td>
<td class="column-feedback cell c5">&nbsp;</td>
<td class="column-contributiontocoursetotal cell c6">-</td>
</tr>
<tr class="cat_24916 lastrow" data-hidden="false">
<th class="level1 d1 baggt b2b courseitem column-itemname cell c0" scope="row"><span class="gradeitemheader" title="Total del curso">Total del curso</span></th>
<td class="column-weight cell c1">-</td>
<td class="column-grade cell c2">40,25</td>
<td class="column-range cell c3">0–60</td>
<td class="column-percentage cell c4">67,08 %</td>
<td class="column-feedback cell c5">&nbsp;</td>
<td class="column-contributiontocoursetotal cell c6">-</td>
</tr>
</tbody>
</table>`

func TestParseUserGradeTable(t *testing.T) {
	t.Parallel()

	rows, err := parseUserGradeTable([]byte(sampleUserGradeTable))
	if err != nil {
		t.Fatalf("parseUserGradeTable: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}

	if rows[0].RowType != "category" || rows[0].Name != "Computación para Ingenieros" {
		t.Fatalf("rows[0]: %+v", rows[0])
	}
	if rows[1].RowType != "category" || len(rows[1].CategoryPath) != 2 || rows[1].CategoryPath[1] != "Corte I" {
		t.Fatalf("rows[1]: %+v", rows[1])
	}
	if rows[2].RowType != "item" || rows[2].ActivityType != "Tarea" || rows[2].Grade != "8,00" {
		t.Fatalf("rows[2]: %+v", rows[2])
	}
	if rows[2].Passed == nil || !*rows[2].Passed {
		t.Fatalf("rows[2].Passed want true")
	}
	if rows[3].RowType != "subtotal" || rows[3].Grade != "12,00" {
		t.Fatalf("rows[3]: %+v", rows[3])
	}
	if rows[4].RowType != "course_total" || rows[4].Grade != "40,25" {
		t.Fatalf("rows[4]: %+v", rows[4])
	}
}

func TestGradeReportURLFromCoursePage(t *testing.T) {
	t.Parallel()

	courseHTML := []byte(`<html><body>
<a href="/grade/report/index.php?id=23265">Calificaciones</a>
</body></html>`)
	got, err := gradeReportURLFromCoursePage("https://saia.uft.edu.ve/course/view.php?id=23265", courseHTML, 23265)
	if err != nil {
		t.Fatalf("gradeReportURLFromCoursePage: %v", err)
	}
	want := "https://saia.uft.edu.ve/grade/report/index.php?id=23265"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
