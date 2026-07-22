# 09 — Deterministic local development with PostgreSQL, MinIO and Air

## 1. Objective

A fresh clone must reach a working local application using documented commands, without manually creating databases, users or buckets. Local behaviour must exercise the same pgx, Goose, sqlc and S3 code paths as production.

This file is the only canonical local-development specification; no CI document may redefine its Make targets.

## 2. Files

```text
compose.yaml
.env.example
.air.toml
Makefile
scripts/wait-for-http.sh
scripts/local-bootstrap.sh
```

Never commit `.env`, local data, MinIO credentials outside `.env.example`, or generated temporary uploads.

## 3. Compose services

Pin image versions/digests and include health checks.

### `postgres`

- PostgreSQL 16 Alpine.
- Port `127.0.0.1:5432:5432`.
- Database `mycfc`, user `mycfc`, password `mycfc_local_only`.
- `TZ=Europe/Lisbon`, PostgreSQL option `timezone=Europe/Lisbon`.
- Named volume `pgdata`.
- Health check `pg_isready -U mycfc -d mycfc`.
- Stop grace period 30 seconds.

### `minio`

- MinIO server with console.
- Ports bound to loopback: 9000 API, 9001 console.
- Root user `mycfc_local`, root password `mycfc_local_password_change_not_prod` in `.env.example` only.
- Command `server /data --console-address :9001`.
- Named volume `miniodata`.
- Health check against `/minio/health/live`.

### `minio-init`

Use pinned `minio/mc`. Wait for MinIO health, configure alias, create bucket `mycfc-local` idempotently, set anonymous access to none, and exit 0. A rerun must be safe.

Compose has a named network and no application container by default; Go/air runs on host for fast development. CI may use the same Compose services.

## 4. `.env.example`

Include every configuration variable from file 02 with safe local values:

```dotenv
APP_ENV=local
APP_VERSION=dev
GIT_SHA=0000000000000000000000000000000000000000
PORT=8080
BASE_URL=http://localhost:8080
DATABASE_URL=postgres://mycfc:mycfc_local_only@localhost:5432/mycfc?sslmode=disable
CSRF_AUTH_KEY_B64=<document command to generate; example must decode to 32 non-production bytes>
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=mycfc_local
AWS_SECRET_ACCESS_KEY=mycfc_local_password_change_not_prod
S3_BUCKET_NAME=mycfc-local
S3_ENDPOINT=http://localhost:9000
S3_FORCE_PATH_STYLE=true
GOOGLE_CALENDAR_API_KEY=replace-with-restricted-local-browser-key
CALENDAR_COMPETITION_ID=replace-with-public-calendar-id
CALENDAR_TRAINING_ID=replace-with-public-calendar-id
CALENDAR_SOCIAL_ID=replace-with-public-calendar-id
CALENDAR_CLEANUPS_ID=replace-with-public-calendar-id
GALLERY_URL=https://example.invalid/gallery
CONSENT_TERMS_VERSION=dev-v1
CONSENT_TERMS_SHA256=<64 lowercase zeroes allowed only in local env>
CONSENT_IMAGE_VERSION=dev-v1
CONSENT_IMAGE_SHA256=<...>
CONSENT_MINOR_VERSION=dev-v1
CONSENT_MINOR_SHA256=<...>
```

Application startup in local mode may accept `.invalid` URLs and documented zero hashes; production validation must reject them.

## 5. Make targets

These names and meanings are canonical:

```text
make tools              install pinned Go tools into ./bin
make dev-infra          docker compose up -d --wait postgres minio minio-init
make dev-infra-down     docker compose down
make dev-infra-clean    docker compose down -v (requires interactive confirmation unless CI=true)
make generate           templ generate; sqlc generate; npm run build
make migrate-up         goose up against DATABASE_URL
make migrate-down-one   goose down once; forbidden when APP_ENV=production
make migrate-status     goose status
make dev-bootstrap      copy .env.example to .env if absent, start infra, migrate, build assets
make dev                load .env and run ./bin/air
make test               unit tests
make test-integration   starts infra and runs tagged/integration suite
make test-e2e           builds app and runs Playwright/axe
make verify             format check, generate-diff check, vet, tests, frontend build/audit, Terraform checks
make reset-local        clean volumes then bootstrap; interactive confirmation
```

Targets use bash with `.SHELLFLAGS := -Eeuo pipefail -c`. Commands fail immediately; do not prefix failures with `-` or `|| true` except cleanup traps.

## 6. Air configuration

Air watches `.go`, `.templ`, `.sql`, `.js`, `.css`, `go.mod`, `package*.json`, and excludes generated output/temp directories.

On change, Air runs one deterministic build command:

```text
make generate-fast && go build -o .tmp/mycfc ./cmd/server
```

`generate-fast` runs templ/sqlc and esbuild incremental build as needed. It must not start separate orphan watcher processes. Air then runs `.tmp/mycfc`. Stop signals must reach the Go child process.

Do not use `go run` for `make dev`.

## 7. Local S3 parity

- Same `ObjectStore` implementation and S3 API calls as production.
- Force path style only when configured.
- Integration tests verify private bucket, upload, delete and pre-sign.
- Tests create unique prefixes and clean them in `t.Cleanup`.
- No test assumes MinIO-specific response text.

## 8. Database test isolation

Integration tests use a separate database `mycfc_test` created by bootstrap or create a per-test schema. Parallel tests must not share mutable rows unless designed for concurrency testing. Migrations run before tests.

## 9. Documentation

Root `README.md` includes exactly:

```bash
cp .env.example .env
# fill Google Calendar development values
make tools
make dev-bootstrap
make dev
```

Also document MinIO console URL, local credentials warning, migration commands, reset command and how to run verification.

## 10. Acceptance criteria

- Fresh clone on supported Linux/macOS with Go, Node, Docker and Make reaches healthy app via commands above.
- `docker compose up -d --wait` is reliable and idempotent.
- Bucket is auto-created and remains private.
- Data persists across `dev-infra-down` and is removed only by clean/reset.
- Air rebuilds after Go, templ and SQL edits; sqlc/templ errors stop the build and display clearly.
- Local upload/presign flow passes against MinIO.
- All Make targets have help text and no duplicated/conflicting migration target names.
