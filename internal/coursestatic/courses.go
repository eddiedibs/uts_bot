package coursestatic

// UFTMoodleCourses are the SAIA course IDs (view.php?id=…) seeded in
// migrations/000001_uft_courses.up.sql — keep in sync with the DB migration.
var UFTMoodleCourses = []struct {
	MoodleID int
	Name     string
}{
	{23277, "Analisis numerico"},
	{23265, "Computacion para ingenieros"},
	{23347, "Dibujo"},
	{23269, "Estructuras discretas II"},
	{23196, "Fisica I"},
	{23266, "Matematica III"},
	{23348, "Quimica"},
}
