#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -e .env && ! -L .env ]]; then
  (umask 077; cp -n .env.example .env)
  echo "Created .env from .env.example. Review it before running the application." >&2
fi

if [[ ${MYCFC_LOCAL_REVIEW_CHILD:-} != yes ]]; then
  exec python3 scripts/local-db-command.py bootstrap -- env MYCFC_LOCAL_REVIEW_CHILD=yes bash "$0"
fi

python3 scripts/local-db-command.py bootstrap -- true

review_database=mycfc_ui_review

docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml up -d --wait postgres minio mailpit
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml run --rm minio-init
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists "$review_database"
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" "$review_database"
python3 scripts/local-db-command.py --database mycfc_ui_review bootstrap -- go run ./cmd/server bootstrap-db
python3 scripts/local-db-command.py --database mycfc_ui_review migration -- go run ./cmd/server migrate
python3 scripts/local-db-command.py --database mycfc_ui_review migration -- go run ./cmd/server prepare-local-recipient-floor

docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$review_database" < scripts/ui-review-seed.sql

echo "UI-review database recreated: $review_database"
echo "Personas use password: correct horse 7"
