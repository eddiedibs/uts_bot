USE uft_db;

-- Tracks consecutive scrape cycles a previously-known course was absent from the discovered
-- dashboard list. A single missed cycle (partial/flaky render) no longer deletes the course and
-- cascades into its activities/attachments/grades; only maxCourseMisses in a row does.
ALTER TABLE courses
  ADD COLUMN miss_count INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Consecutive discovery cycles this course was not seen';
