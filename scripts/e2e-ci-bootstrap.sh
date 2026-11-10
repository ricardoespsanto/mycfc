#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${CI:-} != true ]]; then
	printf '%s\n' 'e2e-ci-bootstrap.sh is CI-only; use make test-e2e locally.' >&2
	exit 1
fi

if [[ ! -e .env && ! -L .env ]]; then
	(umask 077; cp -n .env.example .env)
fi

if [[ ${E2E_BOOTSTRAP_VALIDATE_ENV_ONLY:-false} == true ]]; then
	printf 'COMPOSE_PROJECT_NAME=%s\n' "${COMPOSE_PROJECT_NAME:-}"
	printf 'DOCKER_HOST=%s\n' "${DOCKER_HOST:-}"
	printf 'DOCKER_CONTEXT=%s\n' "${DOCKER_CONTEXT:-}"
	printf 'COMPOSE_FILE=%s\n' "${COMPOSE_FILE:-}"
	printf 'COMPOSE_PROFILES=%s\n' "${COMPOSE_PROFILES:-}"
	printf 'COMPOSE_ENV_FILES=%s\n' "${COMPOSE_ENV_FILES:-}"
	exit 0
fi

# Treat owned local .env as data, preserve caller Docker/Compose targeting, and
# refuse inherited production/admission context before any infrastructure action.
if [[ ${MYCFC_LOCAL_E2E_CHILD:-} != yes ]]; then
  script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
  exec python3 "$script_dir/local-db-command.py" bootstrap -- env MYCFC_LOCAL_E2E_CHILD=yes bash "$0"
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
python3 "$script_dir/local-db-command.py" bootstrap -- true

compose=(docker compose --env-file /dev/null --project-name "${COMPOSE_PROJECT_NAME:-mycfc}" -f compose.yaml -f compose.e2e-ci.yaml)

# Pull the pinned browser and CI app images, and cross-build the server, while
# the independent database bootstrap runs. Use the same Compose files as the
# E2E command so e2e-app resolves to its distroless CI image rather than its
# local development image.
e2e_image_pull_pid=''
e2e_server_build_pid=''
minio_init_pid=''
mailpit_ready_pid=''
cleanup_e2e_background_jobs() {
	for background_pid in "$e2e_image_pull_pid" "$e2e_server_build_pid" "$minio_init_pid" "$mailpit_ready_pid"; do
		if [[ -n $background_pid ]]; then
			kill "$background_pid" 2>/dev/null || true
			wait "$background_pid" 2>/dev/null || true
		fi
	done
}
trap cleanup_e2e_background_jobs EXIT

"${compose[@]}" --profile e2e pull e2e e2e-app &
e2e_image_pull_pid=$!

mkdir -p bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o bin/e2e-server ./cmd/server &
e2e_server_build_pid=$!

# The database reset needs PostgreSQL health, but MinIO initialization and
# Mailpit health are independent of it. Start all services first, then overlap
# those two readiness paths with the reset.
"${compose[@]}" up -d postgres minio mailpit
"${compose[@]}" run --rm minio-init &
minio_init_pid=$!
"${compose[@]}" up -d --wait mailpit &
mailpit_ready_pid=$!
"${compose[@]}" up -d --wait postgres

e2e_database=mycfc_test

"${compose[@]}" exec -T postgres dropdb -U "$POSTGRES_USER" --if-exists --force "$e2e_database"
"${compose[@]}" exec -T postgres createdb -U "$POSTGRES_USER" "$e2e_database"

# Provision fixed application/migration roles and explicitly install the unsigned
# local Reader3 fixture before starting a low-privilege browser runtime.
python3 scripts/local-db-command.py --database mycfc_test bootstrap -- go run ./cmd/server bootstrap-db
python3 scripts/local-db-command.py --database mycfc_test migration -- go run ./cmd/server migrate
python3 scripts/local-db-command.py --database mycfc_test migration -- go run ./cmd/server prepare-local-recipient-floor

if ! wait "$minio_init_pid"; then
	printf '%s\n' 'failed to initialize the E2E MinIO bucket' >&2
	exit 1
fi
minio_init_pid=''

if ! wait "$mailpit_ready_pid"; then
	printf '%s\n' 'failed to start Mailpit for E2E' >&2
	exit 1
fi
mailpit_ready_pid=''

if ! wait "$e2e_server_build_pid"; then
	printf '%s\n' 'failed to cross-build the E2E server' >&2
	exit 1
fi
e2e_server_build_pid=''

if ! wait "$e2e_image_pull_pid"; then
	printf '%s\n' 'failed to pull the pinned E2E images' >&2
	exit 1
fi
e2e_image_pull_pid=''
trap - EXIT
