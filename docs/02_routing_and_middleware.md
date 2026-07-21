# Task: Setup Config, Router, and Resilient DB Connection

**1. Configuration (`caarlos0/env`):**
Define a struct to parse: `DATABASE_URL`, `PORT`, `SESSION_SECRET`, `CSRF_SECRET`, `S3_BUCKET_NAME`, `GOOGLE_CALENDAR_API_KEY`, and `APP_VERSION`.

**2. Database & Middleware Initialization:**
* Load `Europe/Lisbon` via `time.LoadLocation`.
* **Resilience:** Initialize the `pgx` connection pool via `database/sql`. You MUST configure `db.SetMaxOpenConns(20)` and `db.SetMaxIdleConns(5)` to prevent App Runner auto-scaling from exhausting RDS connections.
* Initialize `slog` JSON logging. Wrap the *entire* router with `gorilla/csrf`. Initialize `scs` using the `pgx` store.

**3. Consolidated Route Table (`net/http` Go 1.22):**
Define the exact following routes. Do not invent others.

* **Public:** `GET /login`, `POST /login`, `GET /registo`, `POST /registo`, `GET /health` (Returns 200 OK)
* **Authenticated (Base):** `POST /logout`, `GET /dashboard` (Redirects to specific dashboard)
* **Authenticated (Role-Restricted):**
  * `GET /dashboard/competitor`
  * `GET /dashboard/leisure`
  * `GET /dashboard/guardian`
  * `GET /admin/fleet`
  * `POST /repairs` (Receives multipart form data)
  * `POST /guardian/add-dependent`
