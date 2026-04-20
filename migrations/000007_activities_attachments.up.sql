USE uft_db;

CREATE TABLE IF NOT EXISTS activities_attachments (
    moodle_course_id INT UNSIGNED NOT NULL COMMENT 'References activities.moodle_course_id',
    file_name        VARCHAR(512) NOT NULL,
    file_content     LONGTEXT NOT NULL,
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (moodle_course_id, file_name),
    CONSTRAINT fk_activities_attachments_activity
        FOREIGN KEY (moodle_course_id) REFERENCES activities (moodle_course_id)
        ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
