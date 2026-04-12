package excel

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

var spanishMonths = [13]string{
	"", "enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
}

var spanishMonthIndex = map[string]int{
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4,
	"mayo": 5, "junio": 6, "julio": 7, "agosto": 8,
	"septiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

var dayHeaders = [7]string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}

func filename(year int) string {
	return fmt.Sprintf("%d_Calendar.xlsx", year)
}

// CreateCalendar adds a month sheet to f and saves it.
func CreateCalendar(month, year int, f *excelize.File) (*excelize.File, error) {
	sheetName := spanishMonths[month]
	if _, err := f.NewSheet(sheetName); err != nil {
		return nil, fmt.Errorf("new sheet: %w", err)
	}

	// Remove default blank sheet on brand-new workbooks
	sheets := f.GetSheetList()
	if len(sheets) == 2 && (sheets[0] == "Sheet1" || sheets[1] == "Sheet1") {
		f.DeleteSheet("Sheet1")
	}

	centerStyle, err := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", WrapText: true},
	})
	if err != nil {
		return nil, fmt.Errorf("create style: %w", err)
	}

	// Column widths + day headers
	for col := 1; col <= 7; col++ {
		letter, _ := excelize.ColumnNumberToName(col)
		f.SetColWidth(sheetName, letter, letter, 15)
		cell, _ := excelize.CoordinatesToCellName(col, 1)
		f.SetCellValue(sheetName, cell, dayHeaders[col-1])
		f.SetCellStyle(sheetName, cell, cell, centerStyle)
	}

	// Date layout — Monday-first grid (Go's Sunday=0 → shift to Monday=0)
	firstDay := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	dayOne := int(firstDay.Weekday()+6) % 7 // 0=Mon … 6=Sun
	numDays := daysInMonth(month, year)

	row, col := 2, dayOne+1
	for day := 1; day <= numDays; day++ {
		cell, _ := excelize.CoordinatesToCellName(col, row)
		f.SetCellValue(sheetName, cell, day)
		f.SetCellStyle(sheetName, cell, cell, centerStyle)
		col++
		if col > 7 {
			col = 1
			row++
		}
	}

	if err := f.SaveAs(filename(year)); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}
	slog.Info("calendar created", "month", sheetName, "year", year)
	return f, nil
}

// CheckCalendar loads (or creates) the workbook and adds the month sheet.
func CheckCalendar(month, year int) (*excelize.File, error) {
	f, err := excelize.OpenFile(filename(year))
	if err != nil {
		slog.Warn("no workbook found, creating new", "year", year)
		f = excelize.NewFile()
		return CreateCalendar(month, year, f)
	}
	sheetName := spanishMonths[month]
	for _, s := range f.GetSheetList() {
		if s == sheetName {
			return nil, fmt.Errorf("duplicate sheet: %s", sheetName)
		}
	}
	return CreateCalendar(month, year, f)
}

// WriteActivity appends subjectData to the cell containing day in the month sheet.
func WriteActivity(f *excelize.File, monthName, subjectData string, year, day int) error {
	slog.Info("writing activity to sheet", "sheet", monthName, "day", day)
	wrapStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true},
	})
	rows, err := f.GetRows(monthName)
	if err != nil {
		return fmt.Errorf("get rows: %w", err)
	}
	for rIdx, row := range rows {
		for cIdx, cell := range row {
			if strings.Contains(strings.ToLower(cell), fmt.Sprintf("%d", day)) {
				cellName, _ := excelize.CoordinatesToCellName(cIdx+1, rIdx+1)
				newVal := cell + subjectData
				f.SetCellValue(monthName, cellName, newVal)
				lines := float64(strings.Count(newVal, "\n") + 1)
				f.SetRowHeight(monthName, rIdx+1, 12*1.2*lines)
				letter, _ := excelize.ColumnNumberToName(cIdx + 1)
				f.SetColWidth(monthName, letter, letter, 70)
				f.SetCellStyle(monthName, cellName, cellName, wrapStyle)
				return f.SaveAs(filename(year))
			}
		}
	}
	return nil
}

// WriteDataToExcel parses a Spanish-locale date string and writes the activity.
// Date format: "lunes, 15 de enero de 2024, 10:30"
func WriteDataToExcel(dateString, subjectData string) error {
	t, err := parseSpanishDate(dateString)
	if err != nil {
		return fmt.Errorf("parse date: %w", err)
	}
	month, day, year := int(t.Month()), t.Day(), t.Year()
	monthName := spanishMonths[month]

	f, ferr := excelize.OpenFile(filename(year))
	if ferr != nil {
		slog.Warn("no workbook found, creating new", "year", year)
		f = excelize.NewFile()
		if _, err := CreateCalendar(month, year, f); err != nil {
			return err
		}
		f, _ = excelize.OpenFile(filename(year))
	}

	hasSheet := false
	for _, s := range f.GetSheetList() {
		if s == monthName {
			hasSheet = true
			break
		}
	}
	if !hasSheet {
		newF, err := CheckCalendar(month, year)
		if err != nil {
			return err
		}
		return WriteActivity(newF, monthName, subjectData, year, day)
	}
	return WriteActivity(f, monthName, subjectData, year, day)
}

func daysInMonth(month, year int) int {
	return time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC).Day()
}

// parseSpanishDate parses "weekday, DD de month de YYYY, HH:MM"
func parseSpanishDate(s string) (time.Time, error) {
	// Drop weekday prefix ("lunes, ")
	rest := s
	if i := strings.Index(s, ", "); i >= 0 {
		rest = s[i+2:]
	}
	// rest = "15 de enero de 2024, 10:30"
	datePart, timePart, found := strings.Cut(rest, ", ")
	if !found {
		return time.Time{}, fmt.Errorf("bad format: %q", s)
	}
	// datePart = "15 de enero de 2024"
	var day, year int
	var monthName string
	if _, err := fmt.Sscanf(datePart, "%d de %s de %d", &day, &monthName, &year); err != nil {
		return time.Time{}, fmt.Errorf("parse date part %q: %w", datePart, err)
	}
	monthNum, ok := spanishMonthIndex[strings.ToLower(monthName)]
	if !ok {
		return time.Time{}, fmt.Errorf("unknown month: %s", monthName)
	}
	var hour, minute int
	fmt.Sscanf(timePart, "%d:%d", &hour, &minute)
	return time.Date(year, time.Month(monthNum), day, hour, minute, 0, 0, time.UTC), nil
}

// BreakWord wraps text so each line is at most 41 chars.
func BreakWord(text string) string {
	words := strings.Fields(text)
	var sb strings.Builder
	lineLen := 0
	for _, word := range words {
		if lineLen+len(word)+1 > 41 {
			sb.WriteByte('\n')
			lineLen = 0
		}
		sb.WriteString(word + " ")
		lineLen += len(word) + 1
	}
	return sb.String()
}
