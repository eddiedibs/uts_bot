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

### Chromium / Snap errors (`cmd_run.go`, `XDG_RUNTIME_DIR`, `mkdir ... snap`)

Those messages come from **Ubuntu’s Snap-packaged Chromium**, not from the `.deb` browser. Snap creates its own home/runtime paths and often **breaks** chromedp under **Docker**, **systemd** services, or **`www-data`**.

**Fix (pick one):**

1. **Use a non-snap browser** and point the app at it:

   ```bash
   # Debian/Ubuntu package (common paths: /usr/bin/chromium, google-chrome-stable)
   sudo apt-get update && sudo apt-get install -y chromium || sudo apt-get install -y chromium-browser
   # Optional: remove snap chromium so PATH does not pick it first
   sudo snap remove chromium 2>/dev/null || true
   ```

   In `.env` set an absolute path that **does not** resolve under `/snap/`:

   ```bash
   CHROME_BIN=/usr/bin/chromium
   # or: CHROME_BIN=/usr/bin/google-chrome-stable
   ```

2. **Docker** — the image already sets `CHROME_BIN=/usr/bin/chromium` (Debian package, not Snap). Run the API **inside** that image; do not mount the host’s `/usr/bin/chromium` if it is a Snap shim.

3. **Restricted service user** — ensure `CHROME_BIN` is a packaged binary and the process has a **writable** `HOME` / temp dir (e.g. set `WorkingDirectory` and `HOME` in systemd to something like `/var/lib/uts-bot` with correct ownership).

With **`CHROME_NO_SANDBOX=true`** and **`CHROME_BIN` unset**, the app tries common **non-snap** paths automatically; setting **`CHROME_BIN`** explicitly is still the most reliable.

### Notes

- **`parseTime=true`** in `DATABASE_DSN` is recommended (included in `.env.example`).
- Excel output from the legacy scraper path is written under the API container working directory (`/app`); compose does not mount a volume for it by default.
- Requires **Docker Compose V2** (`docker compose`) for `service_completed_successfully`. If your setup is older, run `docker compose run --rm migrate` once after `db` is healthy, then `docker compose up -d api` without the migrate dependency (temporarily remove `migrate` from `api.depends_on`).
