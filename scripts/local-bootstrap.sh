#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "Created .env from .env.example. Review the Google Calendar values." >&2
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

docker compose up -d --wait postgres minio
docker compose run --rm minio-init
if ! docker compose exec -T postgres psql -U "$POSTGRES_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='mycfc_test'" | grep -qx 1; then
  docker compose exec -T postgres createdb -U "$POSTGRES_USER" mycfc_test
fi
./bin/goose -dir internal/db/migrations postgres "$DATABASE_URL" up
npm ci
npm run build
