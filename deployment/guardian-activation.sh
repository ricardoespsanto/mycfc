#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
operator_env_file=${MYCFC_GUARDIAN_ACTIVATION_ENV_FILE:-/etc/mycfc/guardian-activation.env}
approval_dir=${MYCFC_GUARDIAN_ACTIVATION_APPROVAL_DIR:-/etc/mycfc/guardian-activation}
state_dir=${MYCFC_STATE_DIR:-/etc/mycfc/deployment}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
release_lock_file="$runtime_dir/mycfc-pull-release.lock"
mode=${1:-status}

fail() {
	printf '%s\n' 'event=guardian_activation_runtime_failed error_class=configuration' >&2
	exit 1
}

if [ "$#" -gt 1 ] || [ "$(id -u 2>/dev/null || true)" != 0 ] || [ ! -f "$env_file" ] || [ -L "$env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$env_file" 2>/dev/null || true)" != '0:0:600' ] ||
	[ ! -f "$operator_env_file" ] || [ -L "$operator_env_file" ] || [ "$(stat -c '%u:%g:%a' "$operator_env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail
fi
case "$mode" in status|preflight|enable|disable|provision) ;; *) fail ;; esac

for required in GUARDIAN_ACTIVATION_DATABASE_URL GUARDIAN_ACTIVATION_EXPECTED_DATABASE GUARDIAN_ACTIVATION_ACTOR_REF; do
	if [ "$(grep -c "^$required=" "$operator_env_file" 2>/dev/null || true)" -ne 1 ]; then fail; fi
done
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
		''|\#*) ;;
		GUARDIAN_ACTIVATION_DATABASE_URL=*|GUARDIAN_ACTIVATION_EXPECTED_DATABASE=*|GUARDIAN_ACTIVATION_ACTOR_REF=*) ;;
		*) fail ;;
	esac
done <"$operator_env_file"

exec 9>"$release_lock_file" || fail
if ! flock -n 9; then
	printf '%s\n' 'event=guardian_activation_runtime_failed error_class=release_in_progress' >&2
	exit 1
fi

if [ "$mode" = preflight ] || [ "$mode" = enable ]; then
	approval_file=$approval_dir/approval.json
	if [ ! -d "$approval_dir" ] || [ -L "$approval_dir" ] || [ "$(stat -c '%u:%g:%a' "$approval_dir" 2>/dev/null || true)" != '0:0:700' ] ||
		[ ! -f "$approval_file" ] || [ -L "$approval_file" ] || [ "$(stat -c '%u:%g:%a' "$approval_file" 2>/dev/null || true)" != '0:0:600' ]; then fail; fi
fi

set -a
. "$env_file"
set +a
case "${MYCFC_IMAGE:-}" in *@sha256:[0-9a-f][0-9a-f]*) image_digest=${MYCFC_IMAGE##*@} ;; *) fail ;; esac
printf '%s' "$image_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail

active_slot=$(cat "$state_dir/active-slot" 2>/dev/null || true)
case "$active_slot" in blue|green) ;; *) fail ;; esac
running_image=$(docker inspect --format '{{.Config.Image}}' "mycfc-production-app-$active_slot-1" 2>/dev/null || true)
if [ "$running_image" != "$MYCFC_IMAGE" ]; then fail; fi
export GUARDIAN_ACTIVATION_CURRENT_IMAGE_DIGEST="$image_digest"

if [ "$mode" = provision ]; then
	exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile guardian-activation-bootstrap run --rm guardian-activation-bootstrap
fi
exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile guardian-activation run --rm --no-deps guardian-activation "$mode"
