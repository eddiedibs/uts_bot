USE uft_db;

CREATE TABLE IF NOT EXISTS grades (
    course_view_id INT UNSIGNED  NOT NULL COMMENT 'Moodle course id (course/view.php?id=)',
    item_key       VARCHAR(512)  NOT NULL COMMENT 'Stable normalized key: row_type|category_path|name',
    item_name      VARCHAR(512)  NOT NULL,
    row_type       VARCHAR(32)   NOT NULL COMMENT 'category, item, subtotal, course_total',
    activity_type  VARCHAR(64)   NOT NULL DEFAULT '',
    category_path  VARCHAR(1024) NOT NULL DEFAULT '',
    grade          VARCHAR(64)   NOT NULL DEFAULT '',
    percentage     VARCHAR(64)   NOT NULL DEFAULT '',
    weight         VARCHAR(64)   NOT NULL DEFAULT '',
    link           VARCHAR(2048) NOT NULL DEFAULT '',
    created_at     TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    -- Leftmost PK column already indexes course_view_id, so per-course lookups need no extra key.
    PRIMARY KEY (course_view_id, item_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
