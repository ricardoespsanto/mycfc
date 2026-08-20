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

docker compose up -d --wait postgres minio mailpit
docker compose run --rm minio-init

e2e_database=mycfc_test

docker compose exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists --force "$e2e_database"
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$e2e_database"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$e2e_database" < internal/db/schema.sql

mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/e2e-server ./cmd/server
