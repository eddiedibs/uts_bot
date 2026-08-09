-- Least-privilege MySQL user for uts_notifier.
--
-- The notifier only ever reads pending rows and stamps consumed_at, so it gets SELECT and UPDATE
-- on notification_outbox and nothing else. It cannot read scraped course content, grades, or
-- attachments, and it cannot delete outbox history (retention is uts_bot's job).
--
-- Run once against a running database, as root:
--   docker compose exec -T db mysql -uroot -p"$MYSQL_ROOT_PASSWORD" < scripts/notifier_user.sql
--
-- Replace the password below, or template it, before running in anything real.

CREATE USER IF NOT EXISTS 'notifier'@'%' IDENTIFIED BY 'change_me_notifier_db_password';

GRANT SELECT, UPDATE ON uft_db.notification_outbox TO 'notifier'@'%';

FLUSH PRIVILEGES;
