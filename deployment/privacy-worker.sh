#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
worker_env_file=${MYCFC_PRIVACY_WORKER_ENV_FILE:-/etc/mycfc/privacy-worker.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
compose_file=$deployment_dir/compose.yaml

fail() {
	printf '%s\n' 'privacy_worker_runtime_failed' >&2
	exit 1
}

if [ ! -f "$env_file" ] || [ ! -f "$worker_env_file" ]; then
	fail
fi
set -a
. "$env_file"
set +a

if [ "${PRIVACY_WORKER_ENABLED:-false}" != true ]; then
	printf '%s\n' 'privacy_worker_runtime_disabled'
	exit 0
fi
if [ "${PRIVACY_REQUESTS_ENABLED:-false}" != true ]; then
	fail
fi
if [ "${PRIVACY_COMPLETION_ENABLED:-false}" != true ]; then
	fail
fi
if [ "$(stat -c '%u:%a' "$worker_env_file" 2>/dev/null || true)" != '0:600' ]; then
	fail
fi

case "${1:-start}" in
	readiness)
		docker compose --env-file "$env_file" -f "$compose_file" --profile privacy-worker run --rm --no-deps privacy-worker readiness
		;;
	start)
		exec docker compose --env-file "$env_file" -f "$compose_file" --profile privacy-worker up --no-deps --no-color privacy-worker
		;;
	stop)
		docker compose --env-file "$env_file" -f "$compose_file" --profile privacy-worker stop --timeout 30 privacy-worker >/dev/null
		;;
	*) fail ;;
esac
