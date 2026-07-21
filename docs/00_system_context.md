# Project: MyCFC Web Application
# Client: Clube Fluvial de Coimbra (CFC)

## Tech Stack
* Backend: Go (Standard Library `net/http` using Go 1.22+ routing)
* Database: SQLite (via `mattn/go-sqlite3` or `modernc.org/sqlite`)
* Frontend: HTML/Templates (Go standard library)
* Interactivity: HTMX (hx-get, hx-post, hx-swap)
* Styling: PicoCSS (for simple, semantic HTML styling without build steps)

## Architecture
The application uses a monolithic, server-side rendered architecture. We are building role-based dashboards for a paddling club.

## User Roles
1. `Competitor`: Access to competitive calendar, training logs, and specific squads (e.g., Master A, Senior Polo).
2. `Leisure`: Simplified view focused on social events, river cleans, and news.
3. `Guardian`: Tutor view for 'iniciantes' (youth), focusing on youth schedules and payment tracking.
4. `Admin`: Full access to fleet management, equipment logs, and member management.

## Directory Structure
/cmd/server/         # main.go entrypoint
/internal/models/    # Structs and DB queries
/internal/handlers/  # HTTP handlers mapped to routes
/internal/auth/      # Session and Role-based middleware
/ui/templates/       # HTML templates (base.html, dashboard.html, etc.)
/ui/static/          # CSS, images, HTMX library
