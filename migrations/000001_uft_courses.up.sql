-- Target database for UFT SAIA tooling (align DSN with /uft_db).
CREATE DATABASE IF NOT EXISTS uft_db CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE uft_db;

CREATE TABLE IF NOT EXISTS courses (
    moodle_course_id INT UNSIGNED NOT NULL COMMENT 'Moodle id= query param on course/view.php',
    name             VARCHAR(512) NOT NULL,
    created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (moodle_course_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO courses (moodle_course_id, name) VALUES
    (23277, 'Analisis numerico'),
    (23265, 'Computacion para ingenieros'),
    (23347, 'Dibujo'),
    (23269, 'Estructuras discretas II'),
    (23196, 'Fisica I'),
    (23266, 'Matematica III'),
    (23348, 'Quimica')
ON DUPLICATE KEY UPDATE name = VALUES(name);
