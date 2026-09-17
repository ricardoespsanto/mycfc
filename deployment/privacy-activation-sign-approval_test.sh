#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

ceremony_id=11111111-1111-4111-8111-111111111111
source_sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
image_digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
schema_digest=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
registry_sha=dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
version='Version+One/Exact='
exchange_key=arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111
signing_key=arn:aws:kms:eu-west-1:123456789012:key/22222222-2222-4222-8222-222222222222
bucket=mycfc-production-activation-test

prepared_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
expires_at=$(date -u -d "$prepared_at + 10 minutes" +%Y-%m-%dT%H:%M:%SZ)
jq -cS -n \
	--arg ceremony "$ceremony_id" --arg source "$source_sha" --arg image "$image_digest" --arg schema "$schema_digest" \
	--arg registry "$registry_sha" --arg prepared "$prepared_at" --arg expires "$expires_at" \
	'{contract:"mycfc/privacy-activation-approval-material/v2",ceremony_id:$ceremony,proposal_id:"22222222-2222-4222-8222-222222222222",source_sha:$source,policy_version:"club-2026-09-15-v1",evidence_ids:["11111111-1111-4111-8111-111111111111","22222222-2222-4222-8222-222222222222","33333333-3333-4333-8333-333333333333","44444444-4444-4444-8444-444444444444"],evidence_set_sha256:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",activation_sha256:"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",executor_version:"v1",plan_schema_version:"v1",image_digest:$image,schema_migration_digest:$schema,signer_registry_sha256:$registry,prepared_at:$prepared,ceremony_expires_at:$expires}' \
	>"$test_dir/material-source.json"
material_sha=$(sha256sum "$test_dir/material-source.json" | awk '{print $1}')
material_checksum=$(openssl dgst -sha256 -binary "$test_dir/material-source.json" | base64 -w0)
printf '%s' '{"contract":"mycfc/privacy-activation-signer-registry/v1"}' >"$test_dir/registry.json"
chmod 0600 "$test_dir/registry.json"
actual_registry_sha=$(sha256sum "$test_dir/registry.json" | awk '{print $1}')

cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$AWS_CALLS"
service=$1
operation=$2
shift 2
case "$service:$operation" in
	s3api:get-object)
		destination=
		for argument in "$@"; do destination=$argument; done
		cp "$MATERIAL_SOURCE" "$destination"
		printf '{"VersionId":"%s","ChecksumSHA256":"%s","ServerSideEncryption":"aws:kms","SSEKMSKeyId":"%s"}\n' \
			"$MATERIAL_VERSION" "$MATERIAL_CHECKSUM" "$EXCHANGE_KEY"
		;;
	kms:get-public-key)
		printf '{"KeyId":"%s","KeyUsage":"SIGN_VERIFY","KeySpec":"ECC_NIST_P256","SigningAlgorithms":["ECDSA_SHA_256"],"PublicKey":"%s"}\n' \
			"$SIGNING_KEY" "$(printf public-key | base64 -w0)"
		;;
	kms:sign)
		printf '{"KeyId":"%s","SigningAlgorithm":"ECDSA_SHA_256","Signature":"%s"}\n' \
			"$SIGNING_KEY" "$(printf der-signature | base64 -w0)"
		;;
	s3api:put-object)
		printf '{"VersionId":"ApprovalVersionExact","ChecksumSHA256":"%s","ServerSideEncryption":"aws:kms","SSEKMSKeyId":"%s"}\n' \
			"$APPROVAL_CHECKSUM" "$EXCHANGE_KEY"
		;;
	*) exit 90 ;;
esac
EOF

cat >"$test_dir/bin/approval-cli" <<'EOF'
#!/bin/sh
set -eu
mode=$1
shift
printf '%s\n' "$mode $*" >>"$CLI_CALLS"
output=
digest=
while [ "$#" -gt 0 ]; do
	case "$1" in
		--output) output=$2; shift 2 ;;
		--digest-output) digest=$2; shift 2 ;;
		*) shift ;;
	esac
done
case "$mode" in
	material-verify | approval-verify) ;;
	approval-unsigned)
		printf '%s' '{"unsigned":true}' >"$output"
		dd if=/dev/zero of="$digest" bs=32 count=1 status=none
		chmod 0600 "$output" "$digest"
		;;
	approval-assemble)
		printf '%s' '{"contract":"mycfc/privacy-activation-approval/v2","signature_der_base64":"safe-test"}' >"$output"
		chmod 0600 "$output"
		;;
	*) exit 91 ;;
esac
EOF
chmod 0755 "$test_dir/bin/aws" "$test_dir/bin/approval-cli"

approval_checksum=$(printf '%s' '{"contract":"mycfc/privacy-activation-approval/v2","signature_der_base64":"safe-test"}' | openssl dgst -sha256 -binary | base64 -w0)
export AWS_CALLS="$test_dir/aws.calls" CLI_CALLS="$test_dir/cli.calls" MATERIAL_SOURCE="$test_dir/material-source.json"
export MATERIAL_VERSION="$version" MATERIAL_CHECKSUM="$material_checksum" EXCHANGE_KEY="$exchange_key" SIGNING_KEY="$signing_key"
export APPROVAL_CHECKSUM="$approval_checksum"

run_signer() {
	PATH="$test_dir/bin:$PATH" \
		AWS_REGION=eu-west-1 GITHUB_ACTOR_ID="${TEST_ACTOR_ID:-12345}" GITHUB_RUN_ID=98765 GITHUB_RUN_ATTEMPT=1 \
		PRIVACY_ACTIVATION_ROLE=EXECUTOR PRIVACY_ACTIVATION_GITHUB_ENVIRONMENT=privacy-activation-executor \
		PRIVACY_ACTIVATION_EXPECTED_GITHUB_ACTOR_ID=12345 \
		PRIVACY_ACTIVATION_EXCHANGE_BUCKET="$bucket" PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN="$exchange_key" \
		PRIVACY_ACTIVATION_SIGNING_KEY_ARN="$signing_key" PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$test_dir/registry.json" \
		PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256="$actual_registry_sha" PRIVACY_ACTIVATION_APPROVAL_CLI="$test_dir/bin/approval-cli" \
		PRIVACY_ACTIVATION_CEREMONY_ID="$ceremony_id" PRIVACY_ACTIVATION_MATERIAL_VERSION_ID="$version" \
		PRIVACY_ACTIVATION_MATERIAL_SHA256="${TEST_MATERIAL_SHA:-$material_sha}" PRIVACY_ACTIVATION_SOURCE_SHA="$source_sha" \
		PRIVACY_ACTIVATION_POLICY_VERSION=club-2026-09-15-v1 PRIVACY_ACTIVATION_IMAGE_DIGEST="$image_digest" \
		PRIVACY_ACTIVATION_SCHEMA_MIGRATION_DIGEST="$schema_digest" \
		bash "$script_dir/privacy-activation-sign-approval.sh"
}

output=$(run_signer)
printf '%s' "$output" | grep -Eq '^privacy_activation_approval_submitted role=EXECUTOR ceremony_id=[0-9a-f-]{36} approval_sha256=[0-9a-f]{64} version_id=ApprovalVersionExact$'
grep -q -- "s3api get-object .*--version-id $version --checksum-mode ENABLED" "$AWS_CALLS"
grep -q -- 'kms sign .*--signing-algorithm ECDSA_SHA_256 .*--message-type DIGEST' "$AWS_CALLS"
grep -q -- "s3api put-object .*ceremonies/$ceremony_id/approvals/executor.json .*--checksum-algorithm SHA256 .*--if-none-match \*" "$AWS_CALLS"
grep -q '^material-verify ' "$CLI_CALLS"
grep -q '^approval-unsigned ' "$CLI_CALLS"
grep -q '^approval-assemble ' "$CLI_CALLS"
grep -q '^approval-verify ' "$CLI_CALLS"
if grep -Eq 'safe-test|signature_der_base64|actor_ref' "$AWS_CALLS"; then
	printf '%s\n' 'raw approval material entered the command log' >&2
	exit 1
fi

: >"$AWS_CALLS"
if TEST_ACTOR_ID=54321 run_signer >"$test_dir/wrong-actor.out" 2>&1; then
	printf '%s\n' 'wrong GitHub actor unexpectedly signed' >&2
	exit 1
fi
grep -q 'reason=github_actor_invalid' "$test_dir/wrong-actor.out"
[ ! -s "$AWS_CALLS" ]

: >"$AWS_CALLS"
if TEST_MATERIAL_SHA=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff run_signer >"$test_dir/wrong-material.out" 2>&1; then
	printf '%s\n' 'mismatched material digest unexpectedly signed' >&2
	exit 1
fi
grep -q 'reason=material_sha256_mismatch' "$test_dir/wrong-material.out"
if grep -q 'kms sign' "$AWS_CALLS"; then
	printf '%s\n' 'KMS signing occurred before material verification succeeded' >&2
	exit 1
fi

printf '%s\n' 'privacy activation fixed signer workflow helper tests passed'
