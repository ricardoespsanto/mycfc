#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
activation_env_file=${MYCFC_PRIVACY_ACTIVATION_ENV_FILE:-/etc/mycfc/privacy-activation.env}
disable_env_file=${MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE:-/etc/mycfc/privacy-activation-disable.env}
exchange_env_file=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE:-/etc/mycfc/privacy-activation-exchange.env}
evidence_dir=${MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR:-/etc/mycfc/privacy-activation/evidence}
exchange_state_dir=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR:-/var/lib/mycfc/privacy-activation-exchange}
registry_file=${MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE:-/etc/mycfc/privacy-activation/signer-registry.json}
executor_public_key=${MYCFC_PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_FILE:-/etc/mycfc/privacy-activation/executor-public-key-spki.der}
administrator_public_key=${MYCFC_PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_FILE:-/etc/mycfc/privacy-activation/administrator-public-key-spki.der}
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

if [ "$(id -u 2>/dev/null || true)" != 0 ] || [ ! -f "$activation_env_file" ] || [ -L "$activation_env_file" ] ||
	[ "$(stat -c '%u:%a' "$activation_env_file" 2>/dev/null || true)" != '0:600' ]; then
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
	prepare-exchange | verify-exchange | activate-exchange) protected_files='' ;;
	*) fail ;;
esac
for protected_file in $protected_files; do
	path=$evidence_dir/$protected_file
	if [ ! -f "$path" ] || [ "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" != '0:0:600' ]; then
		fail
	fi
done

if [ "$mode" = record-evidence ]; then
	if [ ! -d "$evidence_dir" ] || [ -L "$evidence_dir" ] ||
		[ "$(stat -c '%u:%g:%a' "$evidence_dir" 2>/dev/null || true)" != '0:0:700' ]; then
		fail
	fi
	exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-activation run --rm --no-deps privacy-activation "$mode"
fi

if [ ! -f "$exchange_env_file" ] || [ -L "$exchange_env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$exchange_env_file" 2>/dev/null || true)" != '0:0:600' ] ||
	[ ! -f "$registry_file" ] || [ -L "$registry_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$registry_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail
fi
registry_sha256=$(sed -n 's/^PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256=//p' "$exchange_env_file")
policy_version=$(sed -n 's/^PRIVACY_ACTIVATION_POLICY_VERSION=//p' "$exchange_env_file")
if ! printf '%s' "$registry_sha256" | grep -Eq '^[0-9a-f]{64}$' ||
	[ "$registry_sha256" != "$(sha256sum "$registry_file" | awk '{print $1}')" ] ||
	! printf '%s' "$policy_version" | grep -Eq '^[A-Za-z0-9._:-]{1,128}$'; then
	fail
fi
export PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256="$registry_sha256"
export PRIVACY_ACTIVATION_POLICY_VERSION="$policy_version"
export PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST
export GIT_SHA

case "$mode" in
	prepare-exchange)
		prepare_dir=$exchange_state_dir/prepare
		if [ ! -d "$prepare_dir" ] || [ -L "$prepare_dir" ] ||
			[ "$(stat -c '%u:%g:%a' "$prepare_dir" 2>/dev/null || true)" != '0:0:700' ] ||
			[ -e "$prepare_dir/material.json" ]; then
			fail
		fi
		exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" \
			--profile privacy-activation-prepare run --rm --no-deps privacy-activation-prepare
		;;
	verify-exchange | activate-exchange)
		active_dir=$exchange_state_dir/active
		if [ ! -d "$active_dir" ] || [ -L "$active_dir" ] ||
			[ "$(stat -c '%u:%g:%a' "$active_dir" 2>/dev/null || true)" != '0:0:700' ]; then
			fail
		fi
		for file in material.json executor.json administrator.json; do
			path=$active_dir/$file
			if [ ! -f "$path" ] || [ -L "$path" ] || [ "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" != '0:0:600' ]; then
				fail
			fi
		done
		for path in "$executor_public_key" "$administrator_public_key"; do
			if [ ! -f "$path" ] || [ -L "$path" ] || [ "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" != '0:0:600' ]; then
				fail
			fi
		done
		ceremony_id=$(jq -r '.ceremony_id // empty' "$active_dir/material.json" 2>/dev/null || true)
		source_sha=$(jq -r '.source_sha // empty' "$active_dir/material.json" 2>/dev/null || true)
		image_digest=$(jq -r '.image_digest // empty' "$active_dir/material.json" 2>/dev/null || true)
		schema_digest=$(jq -r '.schema_migration_digest // empty' "$active_dir/material.json" 2>/dev/null || true)
		if ! printf '%s' "$ceremony_id" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' ||
			[ "$source_sha" != "$GIT_SHA" ] || [ "$image_digest" != "$PRIVACY_ACTIVATION_CURRENT_IMAGE_DIGEST" ] ||
			! printf '%s' "$schema_digest" | grep -Eq '^[0-9a-f]{64}$'; then
			fail
		fi
		if [ "$mode" = verify-exchange ]; then
			exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" \
				--profile privacy-activation-approval run --rm --no-deps privacy-activation-approval bundle-verify \
				--material /run/privacy-activation/material.json \
				--registry /run/privacy-activation/signer-registry.json \
				--registry-sha256 "$registry_sha256" \
				--expected-ceremony-id "$ceremony_id" \
				--expected-source-sha "$source_sha" \
				--expected-policy-version "$policy_version" \
				--expected-image-digest "$image_digest" \
				--expected-schema-migration-digest "$schema_digest" \
				--executor-approval /run/privacy-activation/executor.json \
				--administrator-approval /run/privacy-activation/administrator.json \
				--executor-public-key-spki /run/privacy-activation/executor-public-key-spki.der \
				--administrator-public-key-spki /run/privacy-activation/administrator-public-key-spki.der
		fi
		export PRIVACY_ACTIVATION_CEREMONY_ID="$ceremony_id"
		exec docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" \
			--profile privacy-activation-exchange run --rm --no-deps privacy-activation-exchange
		;;
esac
