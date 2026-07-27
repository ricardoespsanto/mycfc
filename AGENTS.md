# Repository Guidance

## Current Scope

- This is a Go server-rendered foundation slice, not a completed application. `cmd/server` wires configuration, PostgreSQL-backed sessions, CSRF, S3, middleware, and `internal/app/router.go`; most business routes intentionally return `501`.
- Treat `docs/implementation-status.md` as the concise implementation boundary. Production Terraform, CI workflows, templ views, and browser tests are not implemented yet.

## Commands and Baseline

- Install repository tools with `make tools`; they live in `./bin`, not on the global `PATH`. templ, sqlc, and Air are versioned.
- The current green gate is `make verify-foundation` (focused vet/tests plus deterministic asset build). `make test-e2e` runs the initial containerized Playwright/axe coverage. `make verify` remains deliberately red until production Terraform and the remaining acceptance coverage exist.
- Run one Go test with `go test ./internal/<package> -run '^TestName$'`; for example, `go test ./internal/config -run '^TestSecretIsRedacted$'`.
- `make test` runs the current Go unit suite; integration packages remain placeholders and are not included.
- `npm test` discovers zero tests. Use `make test-e2e` for browser verification; it starts the local stack and pinned Playwright container.

## Dependencies

- Before adding or upgrading a dependency, check its authoritative registry or vendor release channel and use the latest stable GA version, or the latest supported LTS version where LTS releases apply.
- Pin direct dependencies to exact versions. Document any compatibility or security exception in the change that introduces it.

## Local and Integration Traps

- Run the server through Air as intended with `PATH="$PWD/bin:$PATH" make dev`; `.air.toml` invokes `templ` by name even though `make tools` installs it under `./bin`. The process also requires `.env`, PostgreSQL, and valid startup configuration.
- `make dev-bootstrap` provisions both `mycfc` and `mycfc_test` from the reset-only baseline, then builds browser assets. Existing volumes retain their original PostgreSQL identity; use `make reset-local` when changing local database credentials or schema.
- `make test-integration` requires Docker services; it recreates `mycfc_test` from the baseline, but integration test directories are placeholders at present.
- `make reset-local` deletes the PostgreSQL and MinIO volumes after confirmation.

## Generated Artifacts

- Edit `internal/db/schema.sql` and `internal/db/queries`, then run `make generate`; never hand-edit tracked `internal/db/generated/*.go` sqlc output. The schema is reset-only: changing it requires recreating databases, not an in-place upgrade.
- Edit browser sources in `ui/static/src`. `npm run build` deletes and recreates tracked fingerprinted files and `manifest.json` in `ui/static/dist`, which are embedded into the Go binary.
- For architecture diagrams, edit `docs/*.dot`, not the generated SVG files; regeneration commands are in `docs/ARCHITECTURE.md`.
