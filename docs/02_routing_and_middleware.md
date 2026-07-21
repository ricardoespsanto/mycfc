# Task: Setup Config, Router, and Security Middleware

Write the Go code to bootstrap the application.

1. **Config**: Use `caarlos0/env` to parse environment variables (`DATABASE_URL`, `PORT`, `SESSION_SECRET`, `CSRF_SECRET`, `S3_BUCKET_NAME`, `APP_VERSION`).
2. **Middleware**: 
   * Initialize `log/slog` to output structured JSON logs, wrapping all HTTP requests.
   * Integrate `gorilla/csrf` to enforce CSRF validation on all state-changing requests.
   * Integrate `alexedwards/scs` backed by PostgreSQL for HttpOnly sessions. 
   * Create an `internal/auth` middleware package that checks user roles from the session.
3. **Router**: Use Go 1.22 `net/http` ServeMux path matching. Define public routes (`/login`), the base authenticated route (`/dashboard`), and role-restricted subgroups (`/dashboard/competitor`, `/admin/*`, etc.).
