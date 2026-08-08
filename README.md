# MyCFC

MyCFC is the server-rendered Clube Fluvial de Coimbra member and fleet application described by the production implementation specification.

## Local development

```bash
cp .env.example .env
make tools
make dev-bootstrap
make dev
```

The application listens on `http://localhost:8080`. MinIO is available at `http://localhost:9001` using the local-only credentials from `.env.example`.

Never reuse local credentials in production.

## Common commands

```bash
make db-provision
make db-provision-test
make test
make test-integration
make verify
make reset-local
```

`internal/db/schema.sql` is the complete baseline for fresh databases. Forward-only migrations for existing databases live in `internal/db/migrations` and are applied by `cmd/server migrate`; normal server startup also applies pending migrations before serving traffic. `make reset-local` deletes local PostgreSQL and MinIO data after confirmation, then recreates the local and test databases from the baseline.

Create or manage an administrator with a password file rather than shell input:

```bash
MYCFC_ADMIN_PASSWORD_FILE=/path/to/password-file go run ./cmd/admin create --email admin@example.com --name "Admin User" --date-of-birth 1990-01-01
```

The CLI also supports `set-password --email ...` and `deactivate --email ...`. Without `MYCFC_ADMIN_PASSWORD_FILE`, it reads and confirms a password only from an interactive terminal.

## Current scope

The application includes authentication and consent, membership-aware programme dashboards, guardian and dependant management, events and RSVP workflows, targeted announcements, training plans and outcomes, repair reporting, fleet operations, and administrator tooling. See `docs/implementation-status.md` for the authoritative implemented, partial, and deferred boundaries.
