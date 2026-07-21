# Project: MyCFC Web Application
# Client: Clube Fluvial de Coimbra (CFC)

## Tech Stack
* Backend: Go (Standard Library `net/http` using Go 1.22+ routing)
* Configuration: `caarlos0/env` (Environment variable parsing)
* Logging: `log/slog` (Structured JSON logging for AWS CloudWatch)
* Security: `gorilla/csrf` (Strict Cross-Site Request Forgery protection)
* Database: PostgreSQL (RDS)
* Migrations: `pressly/goose`
* Data Access: `sqlc` (Type-safe Go interface generation)
* Storage: AWS S3 (via AWS SDK for Go v2) for media uploads
* UI Components: `a-h/templ` (Compile-time type-safe HTML)
* Interactivity: HTMX (`hx-get`, `hx-post`) with `response-targets` extension
* Styling: PicoCSS
* Session Management: `alexedwards/scs` (HttpOnly, server-backed cookies)

## Architecture
Monolithic, server-side rendered application with strict GitOps deployment (GitHub Actions, Terraform). Role-based access mapped via secure session tokens.

## Directory Structure
/cmd/server/         # main.go entrypoint
/internal/config/    # Environment variable structs
/internal/db/        # sqlc generated code and goose migrations
/internal/storage/   # AWS S3 integration logic
/internal/handlers/  # HTTP handlers
/internal/auth/      # Session, CSRF, and Role middleware
/internal/logger/    # slog initialization
/ui/components/      # templ components (*.templ)
