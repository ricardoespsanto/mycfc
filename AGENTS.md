# Repository Guidance

## Current Scope

- This is a Go server-rendered foundation slice, not a completed application. `cmd/server` wires configuration, PostgreSQL-backed sessions, CSRF, S3, middleware, and `internal/app/router.go`; most business routes intentionally return `501`.
- Treat `docs/implementation-status.md` as the concise implementation boundary. Production Terraform, CI workflows, templ views, and browser tests are not implemented yet.

## Commands and Baseline

- Install repository tools with `make tools`; they live in `./bin`, not on the global `PATH`. templ, sqlc, and Air are versioned, but the Makefile currently installs Goose with `@latest`.
- The current green gate is `make verify-foundation` (focused vet/tests plus deterministic asset build). `make verify` is deliberately red: e2e is a placeholder and production Terraform is absent.
- Run one Go test with `go test ./internal/<package> -run '^TestName$'`; for example, `go test ./internal/config -run '^TestSecretIsRedacted$'`.
- `make test` currently has a known baseline failure: `internal/app` router tests panic because the root route calls SCS without session data in the test context. Do not attribute that failure to unrelated changes.
- `npm test` discovers zero tests, and `npm run test:e2e` intentionally fails as unimplemented; neither is meaningful frontend verification yet.

## Local and Integration Traps

- Run the server through Air as intended with `PATH="$PWD/bin:$PATH" make dev`; `.air.toml` invokes `templ` by name even though `make tools` installs it under `./bin`. The process also requires `.env`, PostgreSQL, and valid startup configuration.
- Do not assume `make dev-bootstrap` works from `.env.example` unchanged. `scripts/local-bootstrap.sh` uses `docker-compose` and hard-codes PostgreSQL user/database names that disagree with `compose.yaml` plus `.env.example`; the example S3 settings also do not target the Compose MinIO bucket.
- `make test-integration` requires Docker services and an existing `mycfc_test` database; the target migrates that database but does not create it. Integration test directories are placeholders at present.
- `make reset-local` deletes the PostgreSQL and MinIO volumes after confirmation.

## Generated Artifacts

- Edit SQL in `internal/db/migrations` and `internal/db/queries`, then run `make generate`; never hand-edit tracked `internal/db/generated/*.go` sqlc output.
- Edit browser sources in `ui/static/src`. `npm run build` deletes and recreates tracked fingerprinted files and `manifest.json` in `ui/static/dist`, which are embedded into the Go binary.
- For architecture diagrams, edit `docs/*.dot`, not the generated SVG files; regeneration commands are in `docs/ARCHITECTURE.md`.
