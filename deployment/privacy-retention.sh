#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
retention_env_file=${MYCFC_RETENTION_ENV_FILE:-/etc/mycfc/privacy-retention.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}

fail() {
	printf '%s\n' 'privacy_retention_failed' >&2
	exit 1
}

if [ ! -f "$env_file" ]; then
	fail
fi
set -a
. "$env_file"
set +a
if [ "${PRIVACY_RETENTION_ENABLED:-false}" != true ]; then
	printf '%s\n' 'privacy_retention_disabled'
	exit 0
fi
if [ ! -f "$retention_env_file" ] || [ "$(stat -c '%u:%a' "$retention_env_file" 2>/dev/null || true)" != '0:600' ]; then
	fail
fi

if ! docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile maintenance run --rm --no-deps privacy-retention; then
	fail
fi
