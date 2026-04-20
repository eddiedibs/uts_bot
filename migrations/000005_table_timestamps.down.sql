USE uft_db;

ALTER TABLE activities
  DROP COLUMN updated_at,
  DROP COLUMN created_at;

ALTER TABLE courses
  DROP COLUMN updated_at;
