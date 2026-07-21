# Project: MyCFC Web Application
# Client: Clube Fluvial de Coimbra (CFC)

## Tech Stack
* Backend: Go 1.22+ (`net/http`)
* Database Driver: `jackc/pgx/v5`
* Configuration: `caarlos0/env`
* Logging: `log/slog`
* Security: `gorilla/csrf`
* Database: PostgreSQL 16 (AWS RDS)
* Migrations: `pressly/goose`
* Data Access: `sqlc`
* Storage: AWS S3 (AWS SDK for Go v2)
* UI Components: `a-h/templ`
* Interactivity: HTMX (`hx-get`, `hx-post`, `hx-disabled-elt`, `response-targets`)
* Styling: PicoCSS
* Session: `alexedwards/scs` (HttpOnly, backed by PostgreSQL)
* Local Dev: `air` (for hot-reloading Go)

## Architecture & Style Rules
* **Language Strictness:** The UI and all user-facing data must be strictly European Portuguese (pt-PT). Do not use Brazilian Portuguese (pt-BR) terminology.
* **Timezone Strictness:** The application and database must strictly handle times in `Europe/Lisbon`.
* Monolithic, server-side rendered application.
* Strict GitOps deployment via GitHub Actions and Terraform.

## Directory Structure
/cmd/server/         # main.go entrypoint
/internal/config/    # Environment variable structs
/internal/db/        # sqlc generated code and goose migrations
/internal/storage/   # S3 integration
/internal/handlers/  # HTTP handlers mapped to routes
/internal/auth/      # Session, CSRF, and Role middleware
/ui/components/      # templ components (*.templ)
