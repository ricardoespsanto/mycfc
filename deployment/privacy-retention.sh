#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
retention_env_file=${MYCFC_RETENTION_ENV_FILE:-/etc/mycfc/privacy-retention.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
admin_url_file=${MYCFC_RETENTION_ADMIN_DATABASE_URL_FILE:-/etc/mycfc/privacy-retention/admin-database-url}
login_url_file=${MYCFC_RETENTION_LOGIN_DATABASE_URL_FILE:-/etc/mycfc/privacy-retention/login-database-url}
mode=${1:-run}

fail() {
	printf '%s\n' 'privacy_retention_failed' >&2
	exit 1
}

if [ "$#" -gt 1 ] || [ ! -f "$env_file" ]; then
	fail
fi
set -a
. "$env_file"
set +a
case "$mode" in
	run)
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
		;;
	provision | rotate | revoke)
		if [ "$(id -u)" -ne 0 ]; then
			fail
		fi
		if [ ! -f "$retention_env_file" ] || [ "$(stat -c '%u:%a' "$retention_env_file" 2>/dev/null || true)" != '0:600' ]; then
			fail
		fi
		for protected_file in "$admin_url_file" "$login_url_file"; do
			if [ ! -f "$protected_file" ] || [ -L "$protected_file" ] ||
				[ "$(stat -c '%u:%g:%a' "$protected_file" 2>/dev/null || true)" != '0:0:600' ]; then
				fail
			fi
		done
		if ! docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile maintenance run --rm --no-deps \
			-e PRIVACY_RETENTION_ADMIN_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-retention-admin-database-url \
			-e PRIVACY_RETENTION_LOGIN_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-retention-login-database-url \
			-v "$admin_url_file:/run/secrets/mycfc/privacy-retention-admin-database-url:ro" \
			-v "$login_url_file:/run/secrets/mycfc/privacy-retention-login-database-url:ro" \
			privacy-retention "$mode"; then
			fail
		fi
		;;
	*) fail ;;
esac
