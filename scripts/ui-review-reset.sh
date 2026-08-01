#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "Created .env from .env.example. Review it before running the application." >&2
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

review_database=mycfc_ui_review

docker compose up -d --wait postgres minio
docker compose run --rm minio-init
docker compose exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists "$review_database"
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$review_database"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" < internal/db/schema.sql
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" < scripts/ui-review-seed.sql

echo "UI-review database recreated: $review_database"
echo "Personas use password: correct horse 7"
