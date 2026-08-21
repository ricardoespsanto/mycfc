#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${CI:-} != true ]]; then
	printf '%s\n' 'e2e-ci-bootstrap.sh is CI-only; use make test-e2e locally.' >&2
	exit 1
fi

if [[ ! -f .env ]]; then
	cp .env.example .env
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

# Pull the pinned browser and CI app images while the independent database
# bootstrap and server cross-build run. Use the same Compose files as the E2E
# command so e2e-app resolves to its distroless CI image rather than its local
# development image.
e2e_image_pull_pid=''
cleanup_e2e_image_pull() {
	if [[ -n $e2e_image_pull_pid ]]; then
		kill "$e2e_image_pull_pid" 2>/dev/null || true
		wait "$e2e_image_pull_pid" 2>/dev/null || true
	fi
}
trap cleanup_e2e_image_pull EXIT

docker compose -f compose.yaml -f compose.e2e-ci.yaml --profile e2e pull e2e e2e-app &
e2e_image_pull_pid=$!

docker compose up -d --wait postgres minio mailpit
docker compose run --rm minio-init

e2e_database=mycfc_test

docker compose exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists --force "$e2e_database"
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$e2e_database"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$e2e_database" < internal/db/schema.sql

mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/e2e-server ./cmd/server

if ! wait "$e2e_image_pull_pid"; then
	printf '%s\n' 'failed to pull the pinned E2E images' >&2
	exit 1
fi
e2e_image_pull_pid=''
trap - EXIT
