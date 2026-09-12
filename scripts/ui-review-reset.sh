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

docker compose up -d --wait postgres minio mailpit
docker compose run --rm minio-init
docker compose exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists "$review_database"
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$review_database"
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" < internal/db/schema.sql
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" -c \
  "CREATE SCHEMA IF NOT EXISTS mycfc_meta; CREATE TABLE IF NOT EXISTS mycfc_meta.schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO mycfc_meta.schema_migrations(version) VALUES ('reset-baseline-v1') ON CONFLICT DO NOTHING"
for migration_path in internal/db/migrations/*.sql; do
  migration_version=${migration_path##*/}
  migration_version=${migration_version%.sql}
  docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" -c \
    "INSERT INTO mycfc_meta.schema_migrations(version) VALUES ('$migration_version') ON CONFLICT DO NOTHING"
done
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" < scripts/ui-review-seed.sql

echo "UI-review database recreated: $review_database"
echo "Personas use password: correct horse 7"
