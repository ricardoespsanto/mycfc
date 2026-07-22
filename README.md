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
make migrate-status
make migrate-up
make migrate-down-one
make test
make test-integration
make verify
make reset-local
```

`make reset-local` deletes local PostgreSQL and MinIO data after confirmation.

## Current scope

The first implementation slice provides the database, configuration, middleware, health, storage and local-development foundations. The business UI is tracked in `docs/implementation-status.md` and incomplete routes return `501 Not Implemented` rather than fake successful responses.
