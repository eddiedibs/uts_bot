USE uft_db;

-- Moodle course id from course/view.php?id= (distinct from moodle_course_id = activity cmid)
ALTER TABLE activities
  ADD COLUMN course_view_id INT UNSIGNED NULL COMMENT 'Moodle course id (course/view.php?id=)' AFTER moodle_course_id,
  ADD INDEX idx_activities_course_view_id (course_view_id);
