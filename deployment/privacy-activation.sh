#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
activation_env_file=${MYCFC_PRIVACY_ACTIVATION_ENV_FILE:-/etc/mycfc/privacy-activation.env}
disable_env_file=${MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE:-/etc/mycfc/privacy-activation-disable.env}
evidence_dir=${MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR:-/etc/mycfc/privacy-activation/evidence}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
mode=${1:-record-evidence}

fail() {
	printf '%s\n' 'privacy_activation_runtime_failed' >&2
	exit 1
}

if [ "$#" -gt 1 ] || [ ! -f "$env_file" ]; then
	fail
fi

if [ "$mode" = disable ] || [ "$mode" = provision-disable ]; then
	if [ "$(id -u 2>/dev/null || true)" != 0 ] || [ ! -f "$disable_env_file" ] || [ -L "$disable_env_file" ] ||
		[ "$(stat -c '%u:%g:%a' "$disable_env_file" 2>/dev/null || true)" != '0:0:600' ] ||
		[ "$(grep -c '^PRIVACY_ACTIVATION_DISABLE_DATABASE_URL=' "$disable_env_file" 2>/dev/null || true)" -ne 1 ] ||
		[ "$(grep -c '^PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE=' "$disable_env_file" 2>/dev/null || true)" -ne 1 ] ||
		[ "$(grep -c '^PRIVACY_ACTIVATION_DISABLE_ACTOR_REF=' "$disable_env_file" 2>/dev/null || true)" -ne 1 ]; then
		fail
	fi
	while IFS= read -r disable_line || [ -n "$disable_line" ]; do
		case "$disable_line" in
			'' | \#*) ;;
			PRIVACY_ACTIVATION_DISABLE_DATABASE_URL=* | PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE=* | PRIVACY_ACTIVATION_DISABLE_ACTOR_REF=*) ;;
			*) fail ;;
		esac
	done <"$disable_env_file"
	if [ "$mode" = provision-disable ]; then
		exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-activation-disable-bootstrap run --rm privacy-activation-disable-bootstrap
	fi
	exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-activation-disable run --rm --no-deps privacy-activation-disable
fi

if [ ! -f "$activation_env_file" ] ||
	[ "$(stat -c '%u:%a' "$activation_env_file" 2>/dev/null || true)" != '0:600' ] ||
	[ "$(stat -c '%u:%g:%a' "$evidence_dir" 2>/dev/null || true)" != '0:0:700' ]; then
	fail
fi

set -a
. "$env_file"
set +a
case "${MYCFC_IMAGE:-}" in
	*@sha256:[0-9a-f][0-9a-f]*) PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST=${MYCFC_IMAGE##*@} ;;
	*) fail ;;
esac
if ! printf '%s' "$PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
	fail
fi
export PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST

case "$mode" in
	record-evidence) protected_files='restore-attestation.json restore-attestation.key infrastructure.json provider-registry.json schema-inventory.json artifact-public.key' ;;
	prepare-approvals) protected_files='' ;;
	activate) protected_files='approval-material.json executor-approval.json administrator-approval.json executor-approval-public.key administrator-approval-public.key' ;;
	*) fail ;;
esac
for protected_file in $protected_files; do
	path=$evidence_dir/$protected_file
	if [ ! -f "$path" ] || [ "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" != '0:0:600' ]; then
		fail
	fi
done

exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-activation run --rm --no-deps privacy-activation "$mode"
