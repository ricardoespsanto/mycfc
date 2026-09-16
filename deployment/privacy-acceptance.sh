#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
acceptance_env_file=${MYCFC_PRIVACY_ACCEPTANCE_ENV_FILE:-/etc/mycfc/privacy-acceptance.env}
acceptance_dir=${MYCFC_PRIVACY_ACCEPTANCE_DIR:-/etc/mycfc/privacy-acceptance}
admin_url_file=${MYCFC_PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE:-$acceptance_dir/admin-database-url}
login_url_file=${MYCFC_PRIVACY_ACCEPTANCE_DATABASE_URL_FILE:-$acceptance_dir/login-database-url}
app_url_file=${MYCFC_PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE:-$acceptance_dir/app-database-url}
executor_url_file=${MYCFC_PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE:-$acceptance_dir/executor-database-url}
signing_key_file=${MYCFC_PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE:-$acceptance_dir/signing-private.key}
signing_public_key_file=${MYCFC_PRIVACY_ACCEPTANCE_SIGNING_PUBLIC_KEY_FILE:-$acceptance_dir/signing-public.key}
signing_key_id_file=${MYCFC_PRIVACY_ACCEPTANCE_SIGNING_KEY_ID_FILE:-$acceptance_dir/signing-key-id}
tombstone_public_key_file=${MYCFC_PRIVACY_ACCEPTANCE_TOMBSTONE_PUBLIC_KEY_FILE:-$acceptance_dir/tombstone-public.key}
tombstone_locator_key_file=${MYCFC_PRIVACY_ACCEPTANCE_TOMBSTONE_LOCATOR_KEY_FILE:-$acceptance_dir/tombstone-locator.key}
aws_credentials_file=${MYCFC_PRIVACY_ACCEPTANCE_AWS_CREDENTIALS_FILE:-$acceptance_dir/aws-credentials}
aws_profile=${MYCFC_PRIVACY_ACCEPTANCE_AWS_PROFILE:-mycfc-privacy-worker}
evidence_output=${MYCFC_PRIVACY_ACCEPTANCE_EVIDENCE_OUTPUT:-}
operation_state_dir=${MYCFC_PRIVACY_OPERATION_STATE_DIR:-/var/lib/mycfc/privacy-operations}
runtime_root=${MYCFC_RUNTIME_DIR:-/run}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
mode=${1:-}

fail() { printf '%s\n' 'privacy_acceptance_runtime_failed' >&2; exit 1; }

protected_file() {
	[ -f "$1" ] && [ ! -L "$1" ] &&
		[ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = '0:0:600' ]
}

if [ "$(id -u 2>/dev/null || true)" != 0 ] || [ "$#" -ne 1 ]; then fail; fi
case "$mode" in
	provision | rotate | revoke | run | canary-retry | canary-failure | canary-aged | canary-heartbeat | canary-recovery) ;;
	*) fail ;;
esac
for protected in "$env_file" "$acceptance_env_file"; do
	protected_file "$protected" || fail
done

set -- docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-acceptance \
	run --rm --no-deps

case "$mode" in
	provision | rotate | revoke)
		protected_file "$admin_url_file" || fail
		set -- "$@" \
			-e PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-acceptance-admin-database-url \
			-v "$admin_url_file:/run/secrets/mycfc/privacy-acceptance-admin-database-url:ro"
		if [ "$mode" != revoke ]; then
			protected_file "$login_url_file" || fail
			set -- "$@" \
				-e PRIVACY_ACCEPTANCE_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-acceptance-database-url \
				-v "$login_url_file:/run/secrets/mycfc/privacy-acceptance-database-url:ro"
		fi
		if ! "$@" privacy-acceptance "$mode" >/dev/null 2>&1; then fail; fi
		exit 0
		;;
esac

case "$aws_profile" in '' | *[!A-Za-z0-9+=,.@_-]*) fail ;; esac
case "$evidence_output" in
	"$operation_state_dir"/acceptance/*.json) ;;
	*) fail ;;
esac
if [ -e "$evidence_output" ]; then fail; fi
for protected in "$login_url_file" "$app_url_file" "$executor_url_file" "$signing_key_file" \
	"$signing_public_key_file" "$signing_key_id_file" "$tombstone_public_key_file" \
	"$tombstone_locator_key_file" "$aws_credentials_file"; do
	protected_file "$protected" || fail
done
expected_key_id=$(tr -d '\n' <"$signing_key_id_file")
if ! printf '%s' "$expected_key_id" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'; then fail; fi
case "${MYCFC_IMAGE:-}" in
	*@sha256:[0-9a-f][0-9a-f]*) image_digest=${MYCFC_IMAGE##*@} ;;
	*) fail ;;
esac
if ! printf '%s' "$image_digest" | grep -Eq '^sha256:[0-9a-f]{64}$'; then fail; fi

evidence_dir=$(dirname -- "$evidence_output")
install -d -m 0700 "$evidence_dir"
if [ -L "$evidence_dir" ] || [ "$(stat -c '%u:%g:%a' "$evidence_dir" 2>/dev/null || true)" != '0:0:700' ]; then fail; fi
runtime_dir=$(mktemp -d "$runtime_root/mycfc-privacy-acceptance.XXXXXX")
trap 'rm -rf "$runtime_dir"' EXIT HUP INT TERM
stdout_file=$runtime_dir/stdout
stderr_file=$runtime_dir/stderr

set -- "$@" \
	-e PRIVACY_ACCEPTANCE_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-acceptance-database-url \
	-e PRIVACY_ACCEPTANCE_APP_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-acceptance-app-database-url \
	-e PRIVACY_ACCEPTANCE_EXECUTOR_DATABASE_URL_FILE=/run/secrets/mycfc/privacy-acceptance-executor-database-url \
	-e PRIVACY_ACCEPTANCE_SIGNING_KEY_FILE=/run/secrets/mycfc/privacy-acceptance-signing-private.key \
	-e PRIVACY_ACCEPTANCE_SIGNING_KEY_ID="$expected_key_id" \
	-e PRIVACY_ACCEPTANCE_IMAGE_DIGEST="$image_digest" \
	-e PRIVACY_TOMBSTONE_PUBLIC_KEY_FILE=/run/secrets/mycfc/tombstone-public.key \
	-e PRIVACY_TOMBSTONE_LOCATOR_KEY_FILE=/run/secrets/mycfc/tombstone-locator.key \
	-e AWS_SHARED_CREDENTIALS_FILE=/run/secrets/mycfc/aws-credentials \
	-e AWS_PROFILE="$aws_profile" \
	-v "$login_url_file:/run/secrets/mycfc/privacy-acceptance-database-url:ro" \
	-v "$app_url_file:/run/secrets/mycfc/privacy-acceptance-app-database-url:ro" \
	-v "$executor_url_file:/run/secrets/mycfc/privacy-acceptance-executor-database-url:ro" \
	-v "$signing_key_file:/run/secrets/mycfc/privacy-acceptance-signing-private.key:ro" \
	-v "$tombstone_public_key_file:/run/secrets/mycfc/tombstone-public.key:ro" \
	-v "$tombstone_locator_key_file:/run/secrets/mycfc/tombstone-locator.key:ro" \
	-v "$aws_credentials_file:/run/secrets/mycfc/aws-credentials:ro"
if ! "$@" privacy-acceptance "$mode" >"$stdout_file" 2>"$stderr_file"; then fail; fi

case "$mode" in
	run) expected_events='' ; expected_outcome=COMPLETED ; expected_conditions='[]' ;;
	canary-retry)
		expected_events='event=privacy_acceptance_canary_retry_observed count=1
event=privacy_acceptance_canary_recovery_observed count=1'
		expected_outcome=COMPLETED
		expected_conditions='["retry","recovery"]'
		;;
	canary-failure)
		expected_events='event=privacy_acceptance_canary_failure_observed count=1'
		expected_outcome=CANARY_VERIFIED
		expected_conditions='["failure"]'
		;;
	canary-aged)
		expected_events='event=privacy_acceptance_canary_aged_observed count=1
event=privacy_acceptance_canary_recovery_observed count=1'
		expected_outcome=COMPLETED
		expected_conditions='["aged","recovery"]'
		;;
	canary-heartbeat)
		expected_events='event=privacy_acceptance_canary_heartbeat_missing_observed count=1
event=privacy_acceptance_canary_recovery_observed count=1'
		expected_outcome=COMPLETED
		expected_conditions='["heartbeat_missing","recovery"]'
		;;
	canary-recovery)
		expected_events='event=privacy_acceptance_canary_heartbeat_missing_observed count=1
event=privacy_acceptance_canary_retry_observed count=1
event=privacy_acceptance_canary_recovery_observed count=1'
		expected_outcome=COMPLETED
		expected_conditions='["heartbeat_missing","retry","recovery"]'
		;;
esac

line_count=$(wc -l <"$stdout_file" | tr -d ' ')
case "$line_count" in '' | *[!0-9]*) fail ;; esac
expected_line_count=$(printf '%s\n' "$expected_events" | awk 'NF { n++ } END { print n + 1 }')
if [ "$line_count" -ne "$expected_line_count" ]; then fail; fi
envelope=$runtime_dir/envelope.json
tail -n 1 "$stdout_file" >"$envelope"
events=$runtime_dir/events
if [ "$line_count" -gt 1 ]; then head -n "$((line_count - 1))" "$stdout_file" >"$events"; else : >"$events"; fi
printf '%s\n' "$expected_events" | awk 'NF' >"$runtime_dir/expected-events"
cmp -s "$events" "$runtime_dir/expected-events" || fail

if ! jq -e --arg mode "$mode" --arg outcome "$expected_outcome" --arg key "$expected_key_id" --arg image "$image_digest" --argjson conditions "$expected_conditions" '
	type == "object" and
	(keys | sort) == ["contract","key_id","payload","payload_sha256","signature"] and
	.contract == "mycfc/privacy-synthetic-acceptance/v1" and .key_id == $key and
	(.payload_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
	(.signature | type == "string" and test("^[A-Za-z0-9+/]+={0,2}$")) and
	(.payload | type == "object" and .contract == "mycfc/privacy-synthetic-acceptance/v1" and
	 .mode == $mode and .outcome == $outcome and .image_digest == $image and .conditions == $conditions)
' "$envelope" >/dev/null 2>&1; then fail; fi
payload=$runtime_dir/payload.json
jq -c '.payload' "$envelope" >"$payload"
# jq writes one trailing newline; the signed compact payload does not include it.
payload_sha256=$(head -c -1 "$payload" | sha256sum | awk '{print $1}')
if [ "$payload_sha256" != "$(jq -r .payload_sha256 "$envelope")" ]; then fail; fi
message=$runtime_dir/message
{
	printf '%s\0%s\0' 'mycfc/privacy-synthetic-acceptance/v1' "$expected_key_id"
	head -c -1 "$payload"
} >"$message"
signature=$runtime_dir/signature
public_der=$runtime_dir/public.der
if ! jq -r .signature "$envelope" | base64 -d >"$signature" 2>/dev/null; then fail; fi
printf '\060\052\060\005\006\003\053\145\160\003\041\000' >"$public_der"
if ! base64 -d <"$signing_public_key_file" >>"$public_der" 2>/dev/null; then fail; fi
if [ "$(wc -c <"$public_der" | tr -d ' ')" -ne 44 ]; then fail; fi
if ! openssl pkeyutl -verify -pubin -keyform DER -inkey "$public_der" -rawin -in "$message" -sigfile "$signature" >/dev/null 2>&1; then fail; fi

temporary=$(mktemp "$evidence_dir/.acceptance-evidence.XXXXXX")
chmod 0600 "$temporary"
cat "$envelope" >"$temporary"
mv "$temporary" "$evidence_output"
trap - EXIT HUP INT TERM
rm -rf "$runtime_dir"
if [ -n "$expected_events" ]; then printf '%s\n' "$expected_events"; fi
