#!/usr/bin/env bash
# Owned local databases only. Privileged setup is followed by low-authenticated
# migration/floor operations; no production configuration is sourced.
set -Eeuo pipefail
case ${1:-} in
  mycfc|mycfc_test) fixture_database=$1 ;;
  *) exit 2 ;;
esac
if [[ ${MYCFC_LOCAL_FIXTURE_CHILD:-} != yes ]]; then
  exec python3 scripts/local-db-command.py --database "$fixture_database" bootstrap -- \
    env MYCFC_LOCAL_FIXTURE_CHILD=yes bash "$0" "$fixture_database"
fi
python3 scripts/local-db-command.py --database "$fixture_database" bootstrap -- true
if [[ $fixture_database == mycfc_test ]]; then
  docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists "$fixture_database"
  docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml exec -T postgres createdb -U "$POSTGRES_USER" "$fixture_database"
fi
python3 scripts/local-db-command.py --database "$fixture_database" bootstrap -- go run ./cmd/server bootstrap-db
python3 scripts/local-db-command.py --database "$fixture_database" migration -- go run ./cmd/server migrate
python3 scripts/local-db-command.py --database "$fixture_database" migration -- go run ./cmd/server prepare-local-recipient-floor
