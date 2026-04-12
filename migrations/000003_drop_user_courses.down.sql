USE uft_db;

CREATE TABLE IF NOT EXISTS user_courses (
    user_ssh_fingerprint VARCHAR(128) NOT NULL COMMENT 'ssh.FingerprintSHA256 value, e.g. SHA256:...',
    moodle_course_id       INT UNSIGNED NOT NULL,
    name                   VARCHAR(512)   NOT NULL,
    updated_at             TIMESTAMP      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (user_ssh_fingerprint, moodle_course_id),
    KEY idx_user_courses_user (user_ssh_fingerprint)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
