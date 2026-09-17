#!/bin/sh
set -eu
umask 077

mode=${1:-collect}
env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
exchange_env_file=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE:-/etc/mycfc/privacy-activation-exchange.env}
credentials_file=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIALS_FILE:-/etc/mycfc/privacy-activation-exchange/aws-credentials}
registry_file=${MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE:-/etc/mycfc/privacy-activation/signer-registry.json}
executor_public_key=${MYCFC_PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_FILE:-/etc/mycfc/privacy-activation/executor-public-key-spki.der}
administrator_public_key=${MYCFC_PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_FILE:-/etc/mycfc/privacy-activation/administrator-public-key-spki.der}
state_root=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR:-/var/lib/mycfc/privacy-activation-exchange}
prepare_dir=$state_root/prepare
active_dir=$state_root/active
receipt_dir=$state_root/receipts
lock_file=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_LOCK_FILE:-/run/mycfc-privacy-activation-exchange.lock}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
profile=mycfc-privacy-activation-courier
work_dir=

event() {
	printf '%s\n' "$1"
}

fail() {
	event "event=privacy_activation_exchange_failed reason=$1"
	exit 1
}

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	[ -z "$work_dir" ] || rm -rf -- "$work_dir"
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

protected_file() {
	[ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = '0:0:600' ]
}

protected_directory() {
	[ -d "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = '0:0:700' ]
}

valid_version_id() {
	[ -n "$1" ] && [ "${#1}" -le 1024 ] && printf '%s' "$1" | grep -Eq '^[A-Za-z0-9._~+/=-]+$'
}

valid_ceremony_id() {
	printf '%s' "$1" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
}

validate_environment_file() {
	protected_file "$exchange_env_file" || return 1
	for required in \
		PRIVACY_ACTIVATION_EXCHANGE_ENABLED AWS_REGION PRIVACY_ACTIVATION_EXCHANGE_BUCKET \
		PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN \
		PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN \
		PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256 PRIVACY_ACTIVATION_POLICY_VERSION; do
		[ "$(grep -c "^$required=" "$exchange_env_file" 2>/dev/null || true)" -eq 1 ] || return 1
	done
	while IFS= read -r line || [ -n "$line" ]; do
		case "$line" in
			'' | \#*) ;;
			PRIVACY_ACTIVATION_EXCHANGE_ENABLED=* | AWS_REGION=* | PRIVACY_ACTIVATION_EXCHANGE_BUCKET=* | \
				PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN=* | PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN=* | \
				PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN=* | \
				PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256=* | PRIVACY_ACTIVATION_POLICY_VERSION=*) ;;
			*) return 1 ;;
		esac
	done <"$exchange_env_file"
	awk -F= '
		$1 == "PRIVACY_ACTIVATION_EXCHANGE_ENABLED" { if ($2 != "true" && $2 != "false") exit 1; next }
		$1 == "AWS_REGION" { if ($2 !~ /^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$/) exit 1; next }
		$1 == "PRIVACY_ACTIVATION_EXCHANGE_BUCKET" { if ($2 !~ /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/) exit 1; next }
		$1 == "PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN" { if ($2 !~ /^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key\/[0-9a-f-]{36}$/) exit 1; next }
		$1 == "PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN" || $1 == "PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN" { if ($2 !~ /^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:user\/[A-Za-z0-9+=,.@_\/-]+$/) exit 1; next }
		$1 == "PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256" { if ($2 !~ /^[0-9a-f]{64}$/) exit 1; next }
		$1 == "PRIVACY_ACTIVATION_POLICY_VERSION" { if (length($2) == 0 || length($2) > 128 || $2 !~ /^[A-Za-z0-9._:-]+$/) exit 1; next }
		/^[[:space:]]*($|#)/ { next }
		{ exit 1 }
	' "$exchange_env_file"
}

validate_credentials() {
	protected_file "$credentials_file" || return 1
	awk '
		/^[[:space:]]*(#|;|$)/ { next }
		/^\[mycfc-privacy-activation-courier\][[:space:]]*$/ { section = "courier"; next }
		/^\[/ { invalid = 1; next }
		section != "courier" { invalid = 1; next }
		/^[[:space:]]*aws_access_key_id[[:space:]]*=[[:space:]]*AKIA[A-Z0-9]{16}[[:space:]]*$/ { access++; next }
		/^[[:space:]]*aws_secret_access_key[[:space:]]*=[[:space:]]*[^[:space:]]+[[:space:]]*$/ { secret++; next }
		{ invalid = 1 }
		END { exit !(access == 1 && secret == 1 && !invalid) }
	' "$credentials_file"
}

aws_call() {
	env -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN -u AWS_DEFAULT_PROFILE -u AWS_PROFILE \
		AWS_SHARED_CREDENTIALS_FILE="$credentials_file" AWS_PROFILE="$profile" AWS_PAGER='' aws "$@"
}

validate_runtime() {
	[ "$(id -u)" -eq 0 ] || fail root_required
	[ "$#" -eq 0 ] || fail arguments_invalid
	protected_file "$env_file" || fail host_environment_invalid
	validate_environment_file || fail exchange_environment_invalid
	# This file has a fixed allowlist above, so sourcing cannot introduce an
	# arbitrary command or unrelated host setting.
	set -a
	. "$env_file"
	. "$exchange_env_file"
	set +a
	[ "$PRIVACY_ACTIVATION_EXCHANGE_ENABLED" = true ] || fail exchange_disabled
	printf '%s' "$AWS_REGION" | grep -Eq '^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$' || fail region_invalid
	printf '%s' "$PRIVACY_ACTIVATION_EXCHANGE_BUCKET" | grep -Eq '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$' || fail bucket_invalid
	printf '%s' "$PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN" | grep -Eq '^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f-]{36}$' || fail exchange_key_invalid
	printf '%s' "$PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN" | grep -Eq '^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:user/[A-Za-z0-9+=,.@_/-]+$' || fail courier_arn_invalid
	printf '%s' "$PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256" | grep -Eq '^[0-9a-f]{64}$' || fail registry_digest_invalid
	if [ -z "$PRIVACY_ACTIVATION_POLICY_VERSION" ] || [ "${#PRIVACY_ACTIVATION_POLICY_VERSION}" -gt 128 ] ||
		! printf '%s' "$PRIVACY_ACTIVATION_POLICY_VERSION" | grep -Eq '^[A-Za-z0-9._:-]+$'; then
		fail policy_version_invalid
	fi
	case "${MYCFC_IMAGE:-}" in *@sha256:[0-9a-f]*) image_digest=${MYCFC_IMAGE##*@} ;; *) fail image_invalid ;; esac
	printf '%s' "$image_digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail image_invalid
	printf '%s' "${GIT_SHA:-}" | grep -Eq '^[0-9a-f]{40}$' || fail source_sha_invalid
	validate_credentials || fail courier_credentials_invalid
	for file in "$registry_file" "$executor_public_key" "$administrator_public_key"; do
		protected_file "$file" || fail signer_input_invalid
	done
	[ "$(sha256sum "$registry_file" | awk '{print $1}')" = "$PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256" ] || fail signer_registry_digest_mismatch
	for command in aws base64 date docker env flock jq openssl sha256sum stat systemctl; do
		command -v "$command" >/dev/null 2>&1 || fail runtime_dependency_missing
	done
	install -d -o root -g root -m 0700 "$state_root" "$receipt_dir"
	if ! protected_directory "$state_root" || ! protected_directory "$receipt_dir"; then fail state_directory_invalid; fi
	exec 9>"$lock_file"
	flock -n 9 || fail exchange_locked
	identity=$(aws_call sts get-caller-identity --region "$AWS_REGION" --output json 2>/dev/null) || fail courier_identity_unavailable
	[ "$(printf '%s' "$identity" | jq -r '.Arn // empty')" = "$PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN" ] || fail courier_identity_mismatch
}

file_checksum_base64() {
	openssl dgst -sha256 -binary "$1" | base64 -w0
}

put_fixed_object() {
	file=$1
	key=$2
	response=$3
	checksum=$(file_checksum_base64 "$file")
	aws_call s3api put-object --region "$AWS_REGION" --bucket "$PRIVACY_ACTIVATION_EXCHANGE_BUCKET" \
		--key "$key" --body "$file" --server-side-encryption aws:kms \
		--ssekms-key-id "$PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN" \
		--checksum-algorithm SHA256 --checksum-sha256 "$checksum" --if-none-match '*' --output json >"$response" 2>/dev/null || return 1
	jq -e --arg checksum "$checksum" --arg key "$PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN" '
		(.VersionId | type == "string" and length > 0) and .ChecksumSHA256 == $checksum and
		.ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $key
	' "$response" >/dev/null 2>&1 || return 1
	valid_version_id "$(jq -r .VersionId "$response")"
}

write_receipt() {
	state=$1
	result=$2
	reason=$3
	executor_version=${4:-}
	administrator_version=${5:-}
	executor_sha=${6:-}
	administrator_sha=${7:-}
	ceremony_id=$(jq -r .ceremony_id "$state")
	receipt=$receipt_dir/$ceremony_id.json
	[ ! -e "$receipt" ] || return 1
	temporary=$(mktemp "$receipt_dir/.receipt.XXXXXX")
	jq -cS -n \
		--arg contract 'mycfc/privacy-activation-exchange-receipt/v1' \
		--arg ceremony_id "$ceremony_id" \
		--arg source_sha "$(jq -r .source_sha "$state")" \
		--arg image_digest "$(jq -r .image_digest "$state")" \
		--arg schema_digest "$(jq -r .schema_migration_digest "$state")" \
		--arg material_sha "$(jq -r .material_sha256 "$state")" \
		--arg material_version "$(jq -r .material_version_id "$state")" \
		--arg executor_version "$executor_version" \
		--arg administrator_version "$administrator_version" \
		--arg executor_sha "$executor_sha" \
		--arg administrator_sha "$administrator_sha" \
		--arg result "$result" --arg reason "$reason" \
		--arg finished_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		'{contract:$contract,ceremony_id:$ceremony_id,source_sha:$source_sha,image_digest:$image_digest,schema_migration_digest:$schema_digest,material_sha256:$material_sha,material_version_id:$material_version,executor_approval_version_id:(if $executor_version == "" then null else $executor_version end),administrator_approval_version_id:(if $administrator_version == "" then null else $administrator_version end),executor_approval_sha256:(if $executor_sha == "" then null else $executor_sha end),administrator_approval_sha256:(if $administrator_sha == "" then null else $administrator_sha end),result:$result,reason:(if $reason == "" then null else $reason end),finished_at:$finished_at}' \
		>"$temporary"
	chmod 0600 "$temporary"
	mv "$temporary" "$receipt"
	receipt_response=$receipt_dir/.$ceremony_id.put.json
	if ! put_fixed_object "$receipt" "ceremonies/$ceremony_id/receipt.json" "$receipt_response"; then
		rm -f "$receipt_response"
		return 1
	fi
	rm -f "$receipt_response"
}

close_active() {
	result=$1
	reason=$2
	executor_version=${3:-}
	administrator_version=${4:-}
	executor_sha=${5:-}
	administrator_sha=${6:-}
	state=$active_dir/state.json
	ceremony_id=$(jq -r '.ceremony_id // empty' "$state" 2>/dev/null || true)
	if valid_ceremony_id "$ceremony_id"; then
		write_receipt "$state" "$result" "$reason" "$executor_version" "$administrator_version" "$executor_sha" "$administrator_sha" ||
			event "event=privacy_activation_exchange_receipt_failed ceremony_id=$ceremony_id result=$result reason=receipt_transport_failed"
	fi
	rm -rf -- "$active_dir"
	systemctl stop mycfc-privacy-activation-collector.timer >/dev/null 2>&1 || true
}

validate_material() {
	material=$1
	protected_file "$material" || return 1
	jq -e --arg registry "$PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256" --arg source "$GIT_SHA" \
		--arg image "$image_digest" --arg policy "$PRIVACY_ACTIVATION_POLICY_VERSION" '
		type == "object" and
		(keys | sort) == ["activation_sha256","ceremony_expires_at","ceremony_id","contract","evidence_ids","evidence_set_sha256","executor_version","image_digest","plan_schema_version","policy_version","prepared_at","proposal_id","schema_migration_digest","signer_registry_sha256","source_sha"] and
		.contract == "mycfc/privacy-activation-approval-material/v2" and
		(.ceremony_id | test("^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
		(.proposal_id | test("^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")) and
		.source_sha == $source and .policy_version == $policy and .image_digest == $image and .signer_registry_sha256 == $registry and
		(.evidence_ids | type == "array") and (.evidence_ids | length == 4) and (.evidence_ids | unique | length == 4) and
		(.evidence_ids | all(.[]; type == "string" and test("^[0-9a-f-]{36}$"))) and
		([.evidence_set_sha256,.activation_sha256,.schema_migration_digest] | all(.[]; test("^[0-9a-f]{64}$"))) and
		([.executor_version,.plan_schema_version] | all(.[]; type == "string" and length > 0 and length <= 128)) and
		(.prepared_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
		(.ceremony_expires_at | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	' "$material" >/dev/null 2>&1 || return 1
	prepared_epoch=$(date -u -d "$(jq -r .prepared_at "$material")" +%s 2>/dev/null || true)
	expires_epoch=$(date -u -d "$(jq -r .ceremony_expires_at "$material")" +%s 2>/dev/null || true)
	now_epoch=$(date -u +%s)
	case "$prepared_epoch:$expires_epoch" in *[!0-9:]*) return 1 ;; esac
	[ "$prepared_epoch" -le "$((now_epoch + 30))" ] && [ "$expires_epoch" -gt "$now_epoch" ] &&
		[ "$expires_epoch" -gt "$prepared_epoch" ] && [ "$((expires_epoch - prepared_epoch))" -le 900 ]
}

open_ceremony() {
	[ ! -e "$prepare_dir" ] || fail prepare_directory_collision
	if [ -e "$active_dir" ]; then
		if ! protected_directory "$active_dir" || ! protected_file "$active_dir/state.json"; then fail active_state_invalid; fi
		active_expiry=$(date -u -d "$(jq -r '.ceremony_expires_at // empty' "$active_dir/state.json")" +%s 2>/dev/null || true)
		case "$active_expiry" in '' | *[!0-9]*) fail active_state_invalid ;; esac
		if [ "$active_expiry" -gt "$(date -u +%s)" ]; then fail ceremony_already_active; fi
		close_active EXPIRED material_expired
	fi
	install -d -o root -g root -m 0700 "$prepare_dir"
	if ! MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$state_root" \
		MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$registry_file" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$exchange_env_file" \
		"$deployment_dir/privacy-activation.sh" prepare-exchange >/dev/null; then
		rm -rf "$prepare_dir"
		fail material_prepare_failed
	fi
	material=$prepare_dir/material.json
	validate_material "$material" || { rm -rf "$prepare_dir"; fail material_invalid; }
	ceremony_id=$(jq -r .ceremony_id "$material")
	material_sha=$(sha256sum "$material" | awk '{print $1}')
	response=$prepare_dir/.material-put.json
	if ! put_fixed_object "$material" "ceremonies/$ceremony_id/material.json" "$response"; then
		rm -rf "$prepare_dir"
		fail material_upload_failed
	fi
	material_version=$(jq -r .VersionId "$response")
	material_checksum=$(jq -r .ChecksumSHA256 "$response")
	rm -f "$response"
	state_tmp=$prepare_dir/.state.tmp
	jq -cS -n \
		--arg contract 'mycfc/privacy-activation-exchange-state/v1' \
		--arg ceremony_id "$ceremony_id" --arg material_version_id "$material_version" \
		--arg material_checksum_sha256 "$material_checksum" --arg material_sha256 "$material_sha" \
		--arg source_sha "$(jq -r .source_sha "$material")" --arg image_digest "$(jq -r .image_digest "$material")" \
		--arg policy_version "$(jq -r .policy_version "$material")" \
		--arg schema_migration_digest "$(jq -r .schema_migration_digest "$material")" \
		--arg prepared_at "$(jq -r .prepared_at "$material")" --arg ceremony_expires_at "$(jq -r .ceremony_expires_at "$material")" \
		'{contract:$contract,ceremony_id:$ceremony_id,material_version_id:$material_version_id,material_checksum_sha256:$material_checksum_sha256,material_sha256:$material_sha256,source_sha:$source_sha,image_digest:$image_digest,policy_version:$policy_version,schema_migration_digest:$schema_migration_digest,prepared_at:$prepared_at,ceremony_expires_at:$ceremony_expires_at}' \
		>"$state_tmp"
	chmod 0600 "$state_tmp"
	mv "$state_tmp" "$prepare_dir/state.json"
	mv "$prepare_dir" "$active_dir"
	systemctl start mycfc-privacy-activation-collector.timer >/dev/null
	event "event=privacy_activation_ceremony_opened ceremony_id=$ceremony_id material_sha256=$material_sha material_version_id=$material_version expires_at=$(jq -r .ceremony_expires_at "$active_dir/state.json") source_sha=$(jq -r .source_sha "$active_dir/state.json") image_digest=$(jq -r .image_digest "$active_dir/state.json") schema_migration_digest=$(jq -r .schema_migration_digest "$active_dir/state.json")"
}

approval_attributes() {
	key=$1
	output=$2
	error_file=$3
	if aws_call s3api get-object-attributes --region "$AWS_REGION" --bucket "$PRIVACY_ACTIVATION_EXCHANGE_BUCKET" \
		--key "$key" --object-attributes Checksum,ObjectSize,StorageClass --output json >"$output" 2>"$error_file"; then
		jq -e '
			(.VersionId | type == "string" and length > 0) and
			(.Checksum.ChecksumSHA256 | type == "string" and length > 0) and
			(.ObjectSize | type == "number" and . > 0 and . <= 65536)
		' "$output" >/dev/null 2>&1 || return 2
		valid_version_id "$(jq -r .VersionId "$output")" || return 2
		return 0
	fi
	if grep -Eq '(NoSuchKey|Not Found|404)' "$error_file"; then return 1; fi
	return 2
}

download_approval() {
	key=$1
	attributes=$2
	destination=$3
	response=$4
	version=$(jq -r .VersionId "$attributes")
	expected_checksum=$(jq -r .Checksum.ChecksumSHA256 "$attributes")
	aws_call s3api get-object --region "$AWS_REGION" --bucket "$PRIVACY_ACTIVATION_EXCHANGE_BUCKET" \
		--key "$key" --version-id "$version" --checksum-mode ENABLED "$destination" >"$response" 2>/dev/null || return 1
	chmod 0600 "$destination" "$response"
	actual_checksum=$(file_checksum_base64 "$destination")
	[ "$actual_checksum" = "$expected_checksum" ] || return 1
	jq -e --arg version "$version" --arg checksum "$expected_checksum" --arg key "$PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN" '
		.VersionId == $version and .ChecksumSHA256 == $checksum and
		.ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $key
	' "$response" >/dev/null 2>&1
}

collect_approvals() {
	if [ ! -e "$active_dir" ]; then event 'event=privacy_activation_collector_idle'; return 0; fi
	if ! protected_directory "$active_dir" || ! protected_file "$active_dir/state.json" || ! protected_file "$active_dir/material.json"; then
		fail active_state_invalid
	fi
	state=$active_dir/state.json
	ceremony_id=$(jq -r '.ceremony_id // empty' "$state")
	valid_ceremony_id "$ceremony_id" || fail active_state_invalid
	expires_epoch=$(date -u -d "$(jq -r '.ceremony_expires_at // empty' "$state")" +%s 2>/dev/null || true)
	case "$expires_epoch" in '' | *[!0-9]*) close_active REJECTED state_invalid; fail active_state_invalid ;; esac
	if [ "$expires_epoch" -le "$(date -u +%s)" ]; then
		close_active EXPIRED material_expired
		event "event=privacy_activation_ceremony_closed ceremony_id=$ceremony_id result=EXPIRED reason=material_expired"
		return 0
	fi
	work_dir=$(mktemp -d "$state_root/.collect.XXXXXX")
	chmod 0700 "$work_dir"
	executor_key="ceremonies/$ceremony_id/approvals/executor.json"
	administrator_key="ceremonies/$ceremony_id/approvals/administrator.json"
	executor_status=0
	administrator_status=0
	approval_attributes "$executor_key" "$work_dir/executor-attributes.json" "$work_dir/executor-error" || executor_status=$?
	approval_attributes "$administrator_key" "$work_dir/administrator-attributes.json" "$work_dir/administrator-error" || administrator_status=$?
	if [ "$executor_status" -eq 2 ] || [ "$administrator_status" -eq 2 ]; then
		close_active REJECTED approval_transport_invalid
		fail approval_transport_invalid
	fi
	if [ "$executor_status" -ne 0 ] || [ "$administrator_status" -ne 0 ]; then
		event "event=privacy_activation_collector_waiting ceremony_id=$ceremony_id approvals_present=$((2 - executor_status - administrator_status))"
		return 0
	fi
	if [ "$(date -u +%s)" -ge "$expires_epoch" ]; then
		close_active EXPIRED material_expired
		fail material_expired
	fi
	if ! download_approval "$executor_key" "$work_dir/executor-attributes.json" "$work_dir/executor.json" "$work_dir/executor-get.json" ||
		! download_approval "$administrator_key" "$work_dir/administrator-attributes.json" "$work_dir/administrator.json" "$work_dir/administrator-get.json"; then
		close_active REJECTED approval_checksum_or_version_mismatch
		fail approval_checksum_or_version_mismatch
	fi
	executor_version=$(jq -r .VersionId "$work_dir/executor-attributes.json")
	administrator_version=$(jq -r .VersionId "$work_dir/administrator-attributes.json")
	executor_sha=$(sha256sum "$work_dir/executor.json" | awk '{print $1}')
	administrator_sha=$(sha256sum "$work_dir/administrator.json" | awk '{print $1}')
	for role in executor administrator; do
		temporary=$active_dir/.$role.tmp
		cp "$work_dir/$role.json" "$temporary"
		chown root:root "$temporary"
		chmod 0600 "$temporary"
		mv "$temporary" "$active_dir/$role.json"
	done
	if ! MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$state_root" \
		MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$registry_file" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$exchange_env_file" \
		"$deployment_dir/privacy-activation.sh" verify-exchange >/dev/null; then
		close_active REJECTED approval_bundle_invalid "$executor_version" "$administrator_version" "$executor_sha" "$administrator_sha"
		fail approval_bundle_invalid
	fi
	if [ "$(date -u +%s)" -ge "$expires_epoch" ]; then
		close_active EXPIRED material_expired "$executor_version" "$administrator_version" "$executor_sha" "$administrator_sha"
		fail material_expired
	fi
	if ! MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$state_root" \
		MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$registry_file" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$exchange_env_file" \
		"$deployment_dir/privacy-activation.sh" activate-exchange >/dev/null; then
		close_active REJECTED broker_activation_failed "$executor_version" "$administrator_version" "$executor_sha" "$administrator_sha"
		fail broker_activation_failed
	fi
	close_active SUCCEEDED '' "$executor_version" "$administrator_version" "$executor_sha" "$administrator_sha"
	event "event=privacy_activation_ceremony_closed ceremony_id=$ceremony_id result=SUCCEEDED independent_signatures=2"
}

case "$mode" in
	open | collect) ;;
	*) fail mode_invalid ;;
esac
shift
validate_runtime "$@"
case "$mode" in
	open) open_ceremony ;;
	collect) collect_approvals ;;
esac
