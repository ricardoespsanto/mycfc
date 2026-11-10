#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -e .env && ! -L .env ]]; then
  (umask 077; cp -n .env.example .env)
  echo "Created .env from .env.example. Review the local development values." >&2
fi

# Parse local configuration as data and refuse a remote/production target before
# any Docker or DB action. Child inherits bootstrap credentials only in memory.
if [[ ${MYCFC_LOCAL_BOOTSTRAP_CHILD:-} != yes ]]; then
  exec python3 scripts/local-db-command.py bootstrap -- env MYCFC_LOCAL_BOOTSTRAP_CHILD=yes bash "$0"
fi

python3 scripts/local-db-command.py bootstrap -- true

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

docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml up -d --wait postgres minio mailpit
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml run --rm minio-init
python3 scripts/local-db-command.py bootstrap -- go run ./cmd/server bootstrap-db
python3 scripts/local-db-command.py migration -- go run ./cmd/server migrate
python3 scripts/local-db-command.py migration -- go run ./cmd/server prepare-local-recipient-floor
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists mycfc_test
docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" mycfc_test
python3 scripts/local-db-command.py --database mycfc_test bootstrap -- go run ./cmd/server bootstrap-db
python3 scripts/local-db-command.py --database mycfc_test migration -- go run ./cmd/server migrate
python3 scripts/local-db-command.py --database mycfc_test migration -- go run ./cmd/server prepare-local-recipient-floor
npm ci
npm run build
