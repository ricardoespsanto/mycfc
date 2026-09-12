#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
release_env_file=${MYCFC_GUARDIAN_RELEASE_BIND_ENV_FILE:-/etc/mycfc/guardian-release-bind.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
mode=${1:-provision}
provision_image=${MYCFC_GUARDIAN_RELEASE_BIND_PROVISION_IMAGE:-}

fail() {
	printf '%s\n' 'event=guardian_release_bind_provision_failed error_class=configuration' >&2
	exit 1
}

if [ "$#" -gt 1 ] || [ "$mode" != provision ] || [ "$(id -u 2>/dev/null || true)" != 0 ] ||
	[ ! -f "$env_file" ] || [ -L "$env_file" ] || [ "$(stat -c '%u:%g:%a' "$env_file" 2>/dev/null || true)" != '0:0:600' ] ||
	[ ! -f "$release_env_file" ] || [ -L "$release_env_file" ] || [ "$(stat -c '%u:%g:%a' "$release_env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail
fi

for required in GUARDIAN_RELEASE_BIND_DATABASE_URL GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE; do
	if [ "$(grep -c "^$required=" "$release_env_file" 2>/dev/null || true)" -ne 1 ]; then fail; fi
done
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
		''|\#*) ;;
		GUARDIAN_RELEASE_BIND_DATABASE_URL=*|GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=*) ;;
		*) fail ;;
	esac
done <"$release_env_file"
GUARDIAN_RELEASE_BIND_ENV_FILE=$release_env_file
export GUARDIAN_RELEASE_BIND_ENV_FILE

if [ -n "$provision_image" ]; then
	case "$provision_image" in *@sha256:*) ;; *) fail ;; esac
	if ! printf '%s' "${provision_image##*@}" | grep -Eq '^sha256:[0-9a-f]{64}$'; then fail; fi
	MYCFC_IMAGE=$provision_image
	export MYCFC_IMAGE
fi

exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile guardian-release-bind-bootstrap run --rm guardian-release-bind-bootstrap
