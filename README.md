# uts_bot

HTTP API for UFT SAIA course/activity sync (Go + MySQL + Chromium).

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

   ```bash
   docker compose up --build -d
   ```

   On each start, **`migrate`** runs SQL migrations against MySQL, then the **`api`** container starts when migrations finish successfully.

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

4. **Stop**

   ```bash
   docker compose down
   ```

### Migrations only (optional)

If you need to run migrations without bringing up the API:

```bash
docker compose up -d db
docker compose run --rm migrate
```

### Notes

- **`parseTime=true`** in `DATABASE_DSN` is recommended (included in `.env.example`).
- Excel output from the legacy scraper path is written under the API container working directory (`/app`); compose does not mount a volume for it by default.
- Requires **Docker Compose V2** (`docker compose`) for `service_completed_successfully`. If your setup is older, run `docker compose run --rm migrate` once after `db` is healthy, then `docker compose up -d api` without the migrate dependency (temporarily remove `migrate` from `api.depends_on`).
