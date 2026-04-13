USE uft_db;

CREATE TABLE IF NOT EXISTS activities (
    moodle_course_id INT UNSIGNED NOT NULL COMMENT 'Moodle activity / course module id',
    name               VARCHAR(512) NOT NULL,
    link               VARCHAR(2048) NOT NULL,
    activity_content   JSON NOT NULL,
    PRIMARY KEY (moodle_course_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
