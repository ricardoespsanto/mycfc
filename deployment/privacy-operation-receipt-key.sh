#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
key_dir=${MYCFC_PRIVACY_OPERATION_RECEIPT_KEY_DIR:-/etc/mycfc/privacy-operation-receipt}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
mode=${1:-}
request_id=${2:-}
expected_digest=${3:-}

fail() { printf '%s\n' 'privacy_operation_receipt_key_failed' >&2; exit 1; }
protected_file() {
	[ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = '0:0:600' ]
}
publish_public_key() {
	public_file=$1
	digest=$(openssl pkey -pubin -in "$public_file" -outform DER 2>/dev/null | sha256sum | awk '{print $1}')
	checksum=$(openssl dgst -sha256 -binary "$public_file" | base64 -w0)
	response=$key_dir/.public-key-put.json
	if ! aws s3api put-object --region "$AWS_REGION" --bucket "$PRIVACY_OPERATION_RECEIPT_BUCKET" \
		--key "public-keys/$request_id.pem" --body "$public_file" --if-none-match '*' \
		--checksum-algorithm SHA256 --server-side-encryption aws:kms \
		--ssekms-key-id "$PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN" --output json >"$response"; then fail; fi
	jq -e --arg checksum "$checksum" --arg kms "$PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN" '
		(.VersionId | type == "string" and length > 0) and .ChecksumSHA256 == $checksum and
		.ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
	' "$response" >/dev/null 2>&1 || fail
	rm -f "$response"
	printf '%s\n' "$digest"
}
generate_pair() {
	directory=$1
	install -d -o root -g root -m 0700 "$directory"
	[ ! -e "$directory/private.pem" ] && [ ! -e "$directory/public.pem" ] || fail
	umask 077
	openssl genpkey -algorithm Ed25519 -out "$directory/private.pem" >/dev/null 2>&1 || fail
	openssl pkey -in "$directory/private.pem" -pubout -out "$directory/public.pem" >/dev/null 2>&1 || fail
	chown root:root "$directory/private.pem" "$directory/public.pem"
	chmod 0600 "$directory/private.pem" "$directory/public.pem"
}

[ "$(id -u 2>/dev/null || true)" = 0 ] || fail
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || fail
case "$mode" in provision|rotate-prepare|rotate-activate|finalize-rotate|rollback-rotate|stage-revoke|finalize-revoke) ;; *) fail ;; esac
printf '%s' "$request_id" | grep -Eq '^[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$' || fail
protected_file "$env_file" || fail
protected_file "$release_credentials_file" || fail
set -a
. "$env_file"
set +a
: "${AWS_REGION:?}"
: "${PRIVACY_OPERATION_RECEIPT_BUCKET:?}"
: "${PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN:?}"
install -d -o root -g root -m 0700 "$key_dir"
[ ! -L "$key_dir" ] && [ "$(stat -c '%u:%g:%a' "$key_dir" 2>/dev/null || true)" = '0:0:700' ] || fail
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_SHARED_CREDENTIALS_FILE="$release_credentials_file" AWS_PROFILE="$release_aws_profile" AWS_PAGER=''

case "$mode" in
	provision)
		[ ! -e "$key_dir/private.pem" ] && [ ! -e "$key_dir/public.pem" ] && [ ! -e "$key_dir/pending" ] || fail
		generate_pair "$key_dir/provisioning"
		digest=$(publish_public_key "$key_dir/provisioning/public.pem")
		mv "$key_dir/provisioning/private.pem" "$key_dir/private.pem"
		mv "$key_dir/provisioning/public.pem" "$key_dir/public.pem"
		rmdir "$key_dir/provisioning"
		printf '%s\n' "event=privacy_operation_receipt_key_provisioned public_key_sha256=$digest request_id=$request_id"
		;;
	rotate-prepare)
		if ! protected_file "$key_dir/private.pem" || ! protected_file "$key_dir/public.pem"; then
			fail
		fi
		[ ! -e "$key_dir/pending" ] || fail
		generate_pair "$key_dir/pending"
		digest=$(publish_public_key "$key_dir/pending/public.pem")
		printf '%s\n' "event=privacy_operation_receipt_key_rotation_prepared public_key_sha256=$digest request_id=$request_id"
		;;
	rotate-activate)
		if ! protected_file "$key_dir/private.pem" || ! protected_file "$key_dir/public.pem"; then
			fail
		fi
		if ! protected_file "$key_dir/pending/private.pem" || ! protected_file "$key_dir/pending/public.pem"; then
			fail
		fi
		printf '%s' "$expected_digest" | grep -Eq '^[0-9a-f]{64}$' || fail
		actual_digest=$(openssl pkey -pubin -in "$key_dir/pending/public.pem" -outform DER 2>/dev/null | sha256sum | awk '{print $1}')
		[ "$expected_digest" = "$actual_digest" ] || fail
		[ ! -e "$key_dir/previous" ] || fail
		install -d -o root -g root -m 0700 "$key_dir/previous"
		mv "$key_dir/private.pem" "$key_dir/previous/private.pem"
		mv "$key_dir/public.pem" "$key_dir/previous/public.pem"
		mv "$key_dir/pending/private.pem" "$key_dir/private.pem"
		mv "$key_dir/pending/public.pem" "$key_dir/public.pem"
		rmdir "$key_dir/pending"
		printf '%s\n' "$expected_digest" >"$key_dir/previous/activate-after-$request_id"
		chmod 0600 "$key_dir/previous/activate-after-$request_id"
		printf '%s\n' "event=privacy_operation_receipt_key_rotation_staged public_key_sha256=$expected_digest request_id=$request_id"
		;;
	finalize-rotate)
		if ! protected_file "$key_dir/previous/private.pem" ||
			! protected_file "$key_dir/previous/public.pem" ||
			! protected_file "$key_dir/previous/activate-after-$request_id"; then
			fail
		fi
		rm -f "$key_dir/previous/private.pem" "$key_dir/previous/public.pem" "$key_dir/previous/activate-after-$request_id"
		rmdir "$key_dir/previous"
		printf '%s\n' "event=privacy_operation_receipt_key_rotation_activated request_id=$request_id"
		;;
	rollback-rotate)
		if ! protected_file "$key_dir/private.pem" ||
			! protected_file "$key_dir/public.pem" ||
			! protected_file "$key_dir/previous/private.pem" ||
			! protected_file "$key_dir/previous/public.pem" ||
			! protected_file "$key_dir/previous/activate-after-$request_id"; then
			fail
		fi
		install -d -o root -g root -m 0700 "$key_dir/pending"
		mv "$key_dir/private.pem" "$key_dir/pending/private.pem"
		mv "$key_dir/public.pem" "$key_dir/pending/public.pem"
		mv "$key_dir/previous/private.pem" "$key_dir/private.pem"
		mv "$key_dir/previous/public.pem" "$key_dir/public.pem"
		rm -f "$key_dir/previous/activate-after-$request_id"
		rmdir "$key_dir/previous"
		printf '%s\n' "event=privacy_operation_receipt_key_rotation_rolled_back request_id=$request_id"
		;;
	stage-revoke)
		if ! protected_file "$key_dir/private.pem" || ! protected_file "$key_dir/public.pem"; then
			fail
		fi
		[ ! -e "$key_dir/revoke-after-$request_id" ] || fail
		: >"$key_dir/revoke-after-$request_id"
		chmod 0600 "$key_dir/revoke-after-$request_id"
		;;
	finalize-revoke)
		protected_file "$key_dir/revoke-after-$request_id" || fail
		rm -f "$key_dir/private.pem" "$key_dir/public.pem" "$key_dir/revoke-after-$request_id"
		printf '%s\n' "event=privacy_operation_receipt_key_revoked request_id=$request_id"
		;;
esac
