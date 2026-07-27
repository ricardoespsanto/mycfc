# MyCFC

MyCFC is the server-rendered Clube Fluvial de Coimbra member and fleet application described by the production implementation specification.

## Local development

```bash
cp .env.example .env
# fill Google Calendar development values
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

`internal/db/schema.sql` is a reset-only baseline, not a migration history. `make reset-local` deletes local PostgreSQL and MinIO data after confirmation, then recreates the local and test databases from it.

Create or manage an administrator with a password file rather than shell input:

```bash
MYCFC_ADMIN_PASSWORD_FILE=/path/to/password-file go run ./cmd/admin create --email admin@example.com --name "Admin User" --date-of-birth 1990-01-01
```

The CLI also supports `set-password --email ...` and `deactivate --email ...`. Without `MYCFC_ADMIN_PASSWORD_FILE`, it reads and confirms a password only from an interactive terminal.

## Current scope

The foundation now includes login, adult registration, database-authoritative authorization, and administrator credential management. The remaining business UI is tracked in `docs/implementation-status.md`; incomplete routes return `501 Not Implemented` rather than fake successful responses.
