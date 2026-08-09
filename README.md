# uts_bot

HTTP API for UFT SAIA course/activity sync (Go + MySQL). The Moodle scraper uses **plain HTTP** with cookie sessions and HTML parsing (no Chrome/Chromium).

## Run with Docker Compose

1. **Environment**

   ```bash
   cp .env.example .env
   ```

   Edit `.env` and set at least:

   - `API_KEY` — client auth (`X-API-Key` or `Authorization: Bearer`)
   - `UTS_USERNAME` / `UTS_PASSWORD` — Moodle login
   - `DATABASE_DSN` — for Compose, use host **`db`** and credentials that match `MYSQL_USER` / `MYSQL_PASSWORD` (defaults in `.env.example` work together)
   - If you change `MYSQL_ROOT_PASSWORD`, keep it in sync with what the **`migrate`** service uses (same variable in `docker-compose.yml`)

2. **Start**

   Use **Compose V2** (space between `docker` and `compose`). The **`--build`** flag applies to **`up`**, not to **`docker`**:

   ```bash
   docker compose up --build -d
   ```

   If you only have the older standalone binary, use a hyphen:

   ```bash
   docker-compose up --build -d
   ```

   On each start, **`migrate`** runs SQL migrations against MySQL, then the **`api`** container starts when migrations finish successfully.

   If you see **`unknown flag: --build`**, you likely ran **`docker --build ...`** or put **`--build`** immediately after **`docker`**. The correct token order is: **`docker`** → **`compose`** → **`up`** → **`--build`**.

3. **Check**

   ```bash
   docker compose ps
   docker compose logs -f api
   ```

   Call the API (replace `API_KEY` and port if needed):

   ```bash
   curl -sS -H "X-API-Key: YOUR_API_KEY" "http://localhost:8080/api/v1/courses"
   curl -sS -H "X-API-Key: YOUR_API_KEY" "http://localhost:8080/api/v1/activities"
   ```

   Optional query **`search`** (both endpoints accept **`db`** | **`page`**; invalid values return **400**):

   - **`/api/v1/activities?search=db`** — return stored activities only (no Moodle crawl).
   - **`/api/v1/activities?search=page`** — run the scraper, upsert the DB, then return activities (same as omitting `search`, the default for this route).
   - **`/api/v1/activities?course_id=23348`** — limit results (and, with `search=page`, the crawl) to that Moodle **course** id (`course/view.php?id=`). Omit `course_id` for all courses. Combine with `search`, e.g. `?search=db&course_id=23348`.
   - **`/api/v1/courses?search=db`** — return courses from the DB only (empty list if never seeded).
   - **`/api/v1/courses?search=page`** — run the scraper, re-seed course names from the static list, then return courses.
   - **`/api/v1/courses`** with no `search` — legacy behavior: return DB rows if any exist; otherwise scrape once, seed, then return.

   **Attachments** (same `X-API-Key` / Bearer auth):

   - **`GET /api/v1/courses/{courseViewID}/attachments`** — JSON list of `file_name`, `created_at`, `updated_at` for that Moodle course (`course/view.php?id=`). Example: `curl -sS -H "X-API-Key: KEY" "http://localhost:8080/api/v1/courses/23347/attachments"`.
   - **`GET /api/v1/attachments/content?file_name=...&activity_id=...`** — full attachment JSON including `file_content` (activity `activity_id` = activity cmid / `activities.moodle_course_id`).
   - **`GET /api/v1/attachments/content?file_name=...&course_id=...`** — same when `file_name` is unique within that course; **409** if more than one match (then use `activity_id`).

   **Calificaciones** (live Moodle grade report; requires `course_id`):

   - **`GET /api/v1/califications?course_id=23265`** — logs into SAIA, opens the course page, follows the **Calificaciones** link (`grade/report/index.php?id=`), parses `table.user-grade`, and returns JSON with `rows` (categories, assignments/forums/quizzes, subtotals, course total). Example: `curl -sS -H "X-API-Key: KEY" "http://localhost:8080/api/v1/califications?course_id=23265"`.

4. **Stop**

   ```bash
   docker compose down
   ```

### Migrations (local)

SQL files live in [`migrations/`](migrations/) (`NNNN_name.up.sql` / `.down.sql`). Apply them with the [golang-migrate CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate) against your local MySQL (`uft_db`).

1. **Install the CLI** (pick one):

   ```bash
   # Go
   go install -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3

   # Or download a release binary from:
   # https://github.com/golang-migrate/migrate/releases
   ```

   Ensure `migrate` is on your `PATH` (e.g. `$GOPATH/bin` / `$HOME/go/bin`).

2. **Create the database** if it does not exist yet (example with root):

   ```bash
   mysql -h 127.0.0.1 -u root -p -e "CREATE DATABASE IF NOT EXISTS uft_db CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
   ```

3. **Run migrations** from the repo root. Use a MySQL URL (not the Go `DATABASE_DSN` form). Replace `USER` / `PASS` / host / port to match your local MySQL:

   ```bash
   migrate -path=./migrations \
     -database="mysql://USER:PASS@tcp(127.0.0.1:3306)/uft_db?multiStatements=true&charset=utf8mb4" \
     up
   ```

   If the password has special characters (`@`, `#`, `/`, etc.), URL-encode them in the DSN.

4. **Other useful commands** (same `-path` and `-database`):

   ```bash
   migrate -path=./migrations -database="mysql://USER:PASS@tcp(127.0.0.1:3306)/uft_db?multiStatements=true&charset=utf8mb4" version
   migrate -path=./migrations -database="mysql://USER:PASS@tcp(127.0.0.1:3306)/uft_db?multiStatements=true&charset=utf8mb4" down 1
   ```

Match credentials with `DATABASE_DSN` in `.env` so the API can connect to the same schema after `up`.

### Scraper limitations (HTTP-only)

Course and activity pages must be **mostly server-rendered HTML** in the response. If Moodle hides collapsed sections until JavaScript runs, some activity links may be missing until the theme is adjusted or the site exposes the same markup without JS.

### Notes

- **`parseTime=true`** in `DATABASE_DSN` is recommended (included in `.env.example`).
- Excel output from the legacy scraper path is written under the API container working directory (`/app`); compose does not mount a volume for it by default.
- Requires **Docker Compose V2** (`docker compose`) for `service_completed_successfully`. If your setup is older, run `docker compose run --rm migrate` once after `db` is healthy, then `docker compose up -d api` without the migrate dependency (temporarily remove `migrate` from `api.depends_on`).
