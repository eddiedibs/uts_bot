USE uft_db;

CREATE TABLE IF NOT EXISTS notification_outbox (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    event_type     ENUM('activity','attachment','grade') NOT NULL,
    change_type    ENUM('new','updated') NOT NULL,
    course_view_id INT UNSIGNED  NULL,
    course_name    VARCHAR(512)  NOT NULL DEFAULT '',
    entity_key     VARCHAR(768)  NOT NULL COMMENT 'Identifies the source row, for debugging and dedup',
    title          VARCHAR(512)  NOT NULL DEFAULT '',
    detail         VARCHAR(1024) NOT NULL DEFAULT '',
    url            VARCHAR(2048) NOT NULL DEFAULT '',
    created_at     TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    consumed_at    TIMESTAMP     NULL DEFAULT NULL,
    PRIMARY KEY (id),
    KEY idx_outbox_unconsumed (consumed_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
