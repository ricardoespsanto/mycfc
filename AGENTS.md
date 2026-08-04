# Repository Guidance

## Current Scope

- This is an implemented Go server-rendered application with authentication, membership and guardian workflows, programme dashboards, events, announcements, training, repairs, fleet operations, and administrator tooling. Treat `docs/implementation-status.md` as the concise source of truth for the remaining partial or deferred scope.
- The repository includes production Terraform, the Hetzner deployment bundle, CI workflows, templ views, PostgreSQL and MinIO integration tests, and Playwright/axe browser coverage. Do not infer missing implementation from the older foundation-slice history.

## Commands and Baseline

- Install repository tools with `make tools`; they live in `./bin`, not on the global `PATH`. templ, sqlc, and Air are versioned.
- `make verify` is the full source verification gate: generation, vet/tests, browser coverage, and containerized Terraform formatting/validation. `make verify-foundation` remains a faster focused gate, while `make test-integration` and `make test-e2e` run the Docker-backed PostgreSQL/MinIO and Playwright/axe suites.
- Run one Go test with `go test ./internal/<package> -run '^TestName$'`; for example, `go test ./internal/config -run '^TestSecretIsRedacted$'`.
- `make test` runs the Go unit suite; integration tests use the `integration` build tag and run through `make test-integration`.
- `npm test` runs Node unit tests when present. Use `make test-e2e` for browser verification; it starts the local stack and pinned Playwright container.

## Dependencies

- Before adding or upgrading a dependency, check its authoritative registry or vendor release channel and use the latest stable GA version, or the latest supported LTS version where LTS releases apply.
- Pin direct dependencies to exact versions. Document any compatibility or security exception in the change that introduces it.

## Local and Integration Traps

- Run the server through Air as intended with `PATH="$PWD/bin:$PATH" make dev`; `.air.toml` invokes `templ` by name even though `make tools` installs it under `./bin`. The process also requires `.env`, PostgreSQL, and valid startup configuration.
- `make dev-bootstrap` provisions both `mycfc` and `mycfc_test` from the baseline, then builds browser assets. Existing volumes retain their original PostgreSQL identity; use `make reset-local` when changing local database credentials.
- `make test-integration` requires Docker services and recreates `mycfc_test` from the current baseline before running the tagged PostgreSQL/MinIO integration suites.
- `make reset-local` deletes the PostgreSQL and MinIO volumes after confirmation.

## Generated Artifacts

- Keep `internal/db/schema.sql` as the complete baseline for fresh databases. For a live schema change, update that baseline and add a forward-only, expand/contract-compatible migration under `internal/db/migrations`; `cmd/server migrate` records and applies pending migrations for existing databases. Edit queries under `internal/db/queries`, then run `make generate`; never hand-edit tracked `internal/db/generated/*.go` sqlc output.
- Edit browser sources in `ui/static/src`. `npm run build` deletes and recreates tracked fingerprinted files and `manifest.json` in `ui/static/dist`, which are embedded into the Go binary.
- For architecture diagrams, edit `docs/*.dot`, not the generated SVG files; regeneration commands are in `docs/ARCHITECTURE.md`.
