#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "Created .env from .env.example. Review the local development values." >&2
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

# Older local .env files predate the split runtime/migration roles. Supply
# development-only defaults without weakening production configuration.
export APP_DB_USER="${APP_DB_USER:-mycfc_app}"
export APP_DB_PASSWORD="${APP_DB_PASSWORD:-mycfc_local_app_only}"
export MIGRATION_DB_USER="${MIGRATION_DB_USER:-mycfc_migration}"
export MIGRATION_DB_PASSWORD="${MIGRATION_DB_PASSWORD:-mycfc_local_migration_only}"

for consent_url in \
  "CONSENT_TERMS_URL=$BASE_URL/legal/termos-gerais" \
  "CONSENT_IMAGE_URL=$BASE_URL/legal/uso-imagem" \
  "CONSENT_MINOR_URL=$BASE_URL/legal/responsabilidade-menor" \
  "PRIVACY_NOTICE_URL=$BASE_URL/legal/privacidade" \
  "COOKIE_NOTICE_URL=$BASE_URL/legal/cookies" \
  "DATA_RIGHTS_CONTACT=privacy@example.test"; do
  key=${consent_url%%=*}
  if [[ -z ${!key:-} ]]; then
    printf '\n%s\n' "$consent_url" >> .env
    # The variable contains a deliberate NAME=value assignment.
    # shellcheck disable=SC2163
    export "$consent_url"
  fi
done

docker compose up -d --wait postgres minio mailpit
docker compose run --rm minio-init
go run ./cmd/server bootstrap-db
go run ./cmd/server migrate
docker compose exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists mycfc_test
docker compose exec -T postgres createdb -U "$POSTGRES_USER" mycfc_test
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d mycfc_test < internal/db/schema.sql
npm ci
npm run build
