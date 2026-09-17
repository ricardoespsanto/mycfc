#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

role=${PRIVACY_ACTIVATION_ROLE:-}
environment=${PRIVACY_ACTIVATION_GITHUB_ENVIRONMENT:-}
expected_actor_id=${PRIVACY_ACTIVATION_EXPECTED_GITHUB_ACTOR_ID:-}
actor_id=${GITHUB_ACTOR_ID:-}
run_id=${GITHUB_RUN_ID:-}
run_attempt=${GITHUB_RUN_ATTEMPT:-}
region=${AWS_REGION:-}
bucket=${PRIVACY_ACTIVATION_EXCHANGE_BUCKET:-}
exchange_key=${PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN:-}
signing_key=${PRIVACY_ACTIVATION_SIGNING_KEY_ARN:-}
registry=${PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE:-signer-registry.json}
registry_sha256=${PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256:-}
approval_cli=${PRIVACY_ACTIVATION_APPROVAL_CLI:-./mycfc-privacy-activation-approval}
ceremony_id=${PRIVACY_ACTIVATION_CEREMONY_ID:-}
material_version_id=${PRIVACY_ACTIVATION_MATERIAL_VERSION_ID:-}
material_sha256=${PRIVACY_ACTIVATION_MATERIAL_SHA256:-}
source_sha=${PRIVACY_ACTIVATION_SOURCE_SHA:-}
policy_version=${PRIVACY_ACTIVATION_POLICY_VERSION:-}
image_digest=${PRIVACY_ACTIVATION_IMAGE_DIGEST:-}
schema_digest=${PRIVACY_ACTIVATION_SCHEMA_MIGRATION_DIGEST:-}
work_dir=${PRIVACY_ACTIVATION_SIGNING_WORK_DIR:-}

fail() {
	printf '%s\n' "privacy_activation_approval_failed reason=$1" >&2
	exit 1
}

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if [[ -n "$work_dir" && -d "$work_dir" ]]; then
		find "$work_dir" -type f -exec chmod 0600 {} + 2>/dev/null || true
		rm -rf -- "$work_dir"
	fi
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

case "$role:$environment" in
	EXECUTOR:privacy-activation-executor) approval_name=executor.json ;;
	ADMINISTRATOR:privacy-activation-administrator) approval_name=administrator.json ;;
	*) fail fixed_role_or_environment_invalid ;;
esac
[[ "$actor_id" =~ ^[1-9][0-9]{0,19}$ && "$actor_id" == "$expected_actor_id" ]] || fail github_actor_invalid
[[ "$run_id" =~ ^[1-9][0-9]{0,19}$ && "$run_attempt" =~ ^[1-9][0-9]{0,4}$ ]] || fail github_run_invalid
[[ "$region" =~ ^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$ ]] || fail region_invalid
[[ "$bucket" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]] || fail bucket_invalid
[[ "$exchange_key" =~ ^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f-]{36}$ ]] || fail exchange_key_invalid
[[ "$signing_key" =~ ^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/[0-9a-f-]{36}$ ]] || fail signing_key_invalid
[[ "$ceremony_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] || fail ceremony_id_invalid
[[ "$material_version_id" =~ ^[-A-Za-z0-9._~+/=]{1,1024}$ ]] || fail material_version_invalid
[[ "$material_sha256" =~ ^[0-9a-f]{64}$ && "$registry_sha256" =~ ^[0-9a-f]{64}$ ]] || fail digest_invalid
[[ "$source_sha" =~ ^[0-9a-f]{40}$ && "$image_digest" =~ ^sha256:[0-9a-f]{64}$ && "$schema_digest" =~ ^[0-9a-f]{64}$ ]] || fail release_binding_invalid
[[ -n "$policy_version" && ${#policy_version} -le 128 && "$policy_version" =~ ^[A-Za-z0-9._:-]+$ ]] || fail policy_version_invalid
[[ -x "$approval_cli" ]] || fail approval_cli_invalid

for command in aws base64 jq openssl sha256sum stat; do
	command -v "$command" >/dev/null 2>&1 || fail runtime_dependency_missing
done
if [[ ! -f "$registry" || -L "$registry" || "$(stat -c '%u:%a' "$registry" 2>/dev/null || true)" != "$(id -u):600" ]]; then
	fail signer_registry_invalid
fi
[[ "$(sha256sum "$registry" | awk '{print $1}')" == "$registry_sha256" ]] || fail signer_registry_digest_mismatch

if [[ -z "$work_dir" ]]; then
	work_dir=$(mktemp -d "${RUNNER_TEMP:-/tmp}/mycfc-privacy-approval.XXXXXX")
else
	[[ "$work_dir" == /* && ! -e "$work_dir" ]] || fail work_directory_invalid
	mkdir -m 0700 -- "$work_dir"
fi
chmod 0700 "$work_dir"
material=$work_dir/material.json
get_response=$work_dir/material-get.json
public_key_response=$work_dir/public-key.json
public_key=$work_dir/public-key.der
unsigned=$work_dir/unsigned.json
digest_file=$work_dir/digest.bin
sign_response=$work_dir/sign.json
signature=$work_dir/signature.txt
approval=$work_dir/$approval_name
put_response=$work_dir/put.json

material_key="ceremonies/$ceremony_id/material.json"
approval_key="ceremonies/$ceremony_id/approvals/$approval_name"
aws s3api get-object \
	--region "$region" --bucket "$bucket" --key "$material_key" --version-id "$material_version_id" \
	--checksum-mode ENABLED "$material" >"$get_response"
chmod 0600 "$material" "$get_response"

actual_material_sha=$(sha256sum "$material" | awk '{print $1}')
actual_material_checksum=$(openssl dgst -sha256 -binary "$material" | base64 -w0)
jq -e \
	--arg version "$material_version_id" --arg checksum "$actual_material_checksum" --arg kms "$exchange_key" \
	'.VersionId == $version and .ChecksumSHA256 == $checksum and .ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms' \
	"$get_response" >/dev/null || fail material_transport_binding_invalid
[[ "$actual_material_sha" == "$material_sha256" ]] || fail material_sha256_mismatch

common_flags=(
	--material "$material"
	--registry "$registry"
	--registry-sha256 "$registry_sha256"
	--expected-ceremony-id "$ceremony_id"
	--expected-source-sha "$source_sha"
	--expected-policy-version "$policy_version"
	--expected-image-digest "$image_digest"
	--expected-schema-migration-digest "$schema_digest"
)
"$approval_cli" material-verify "${common_flags[@]}" >/dev/null

aws kms get-public-key --region "$region" --key-id "$signing_key" --output json >"$public_key_response"
jq -e --arg key "$signing_key" '
	.KeyId == $key and .KeyUsage == "SIGN_VERIFY" and .KeySpec == "ECC_NIST_P256" and
	(.SigningAlgorithms == ["ECDSA_SHA_256"] or (.SigningAlgorithms | index("ECDSA_SHA_256") != null)) and
	(.PublicKey | type == "string" and length > 0)
' "$public_key_response" >/dev/null || fail signing_key_posture_invalid
jq -jr '.PublicKey' "$public_key_response" | base64 -d >"$public_key"
chmod 0600 "$public_key"

"$approval_cli" approval-unsigned "${common_flags[@]}" \
	--role "$role" --github-actor-id "$actor_id" --github-run-id "$run_id" --github-run-attempt "$run_attempt" \
	--output "$unsigned" --digest-output "$digest_file" >/dev/null
[[ "$(stat -c '%s' "$digest_file")" == 32 ]] || fail signing_digest_invalid

aws kms sign --region "$region" --key-id "$signing_key" --signing-algorithm ECDSA_SHA_256 \
	--message-type DIGEST --message "fileb://$digest_file" --output json >"$sign_response"
jq -e --arg key "$signing_key" '
	.KeyId == $key and .SigningAlgorithm == "ECDSA_SHA_256" and (.Signature | type == "string" and length > 0)
' "$sign_response" >/dev/null || fail kms_signature_response_invalid
jq -jr '.Signature' "$sign_response" >"$signature"
chmod 0600 "$signature"

"$approval_cli" approval-assemble "${common_flags[@]}" \
	--role "$role" --unsigned "$unsigned" --signature "$signature" --public-key-spki "$public_key" --output "$approval" >/dev/null
"$approval_cli" approval-verify "${common_flags[@]}" \
	--role "$role" --approval "$approval" --public-key-spki "$public_key" >/dev/null

approval_sha256=$(sha256sum "$approval" | awk '{print $1}')
approval_checksum=$(openssl dgst -sha256 -binary "$approval" | base64 -w0)
aws s3api put-object \
	--region "$region" --bucket "$bucket" --key "$approval_key" --body "$approval" \
	--server-side-encryption aws:kms --ssekms-key-id "$exchange_key" \
	--checksum-algorithm SHA256 --checksum-sha256 "$approval_checksum" --if-none-match '*' \
	--output json >"$put_response"
jq -e --arg checksum "$approval_checksum" --arg kms "$exchange_key" '
	(.VersionId | type == "string" and length > 0) and .ChecksumSHA256 == $checksum and
	.ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
' "$put_response" >/dev/null || fail approval_transport_binding_invalid

approval_version_id=$(jq -r '.VersionId' "$put_response")
printf '%s\n' "privacy_activation_approval_submitted role=$role ceremony_id=$ceremony_id approval_sha256=$approval_sha256 version_id=$approval_version_id"
