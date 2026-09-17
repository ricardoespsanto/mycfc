#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
state_dir=${MYCFC_PRIVACY_OPERATION_STATE_DIR:-/var/lib/mycfc/privacy-operations}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
receipt_signing_key_file=${MYCFC_PRIVACY_OPERATION_RECEIPT_SIGNING_KEY_FILE:-/etc/mycfc/privacy-operation-receipt/private.pem}
receipt_key_dir=${MYCFC_PRIVACY_OPERATION_RECEIPT_KEY_DIR:-/etc/mycfc/privacy-operation-receipt}

work_dir=
docker_config=
request_container=

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	[ -z "$request_container" ] || docker rm "$request_container" >/dev/null 2>&1 || true
	[ -z "$work_dir" ] || rm -rf "$work_dir"
	[ -z "$docker_config" ] || rm -rf "$docker_config"
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

fail() {
	printf '%s\n' "event=privacy_operation_agent_rejected reason=$1"
	exit 1
}

receipt_fail() {
	reason=$1
	if [ "${operation:-}" = receipt-key-rotate-activate ] && [ -e "$receipt_key_dir/previous/activate-after-${request_id:-}" ]; then
		"$deployment_dir/privacy-operation-receipt-key.sh" rollback-rotate "$request_id" >/dev/null 2>&1 || true
	fi
	fail "$reason"
}

if [ "$(id -u)" -ne 0 ]; then
	fail root_required
fi
if [ "$#" -ne 0 ]; then
	fail arguments_invalid
fi
if [ ! -f "$env_file" ] || [ -L "$env_file" ] || [ "$(stat -c '%u:%g:%a' "$env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail host_environment_invalid
fi
if [ ! -f "$release_credentials_file" ] || [ -L "$release_credentials_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$release_credentials_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail release_credentials_invalid
fi

set -a
. "$env_file"
set +a
if [ "${PRIVACY_PRODUCTION_OPERATIONS_ENABLED:-false}" != true ]; then
	printf '%s\n' 'event=privacy_operation_agent_disabled'
	exit 0
fi
: "${AWS_REGION:?}"
: "${ECR_REPOSITORY_URL:?}"
: "${PRIVACY_OPERATION_RECEIPT_BUCKET:?}"
: "${PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN:?}"
if ! printf '%s' "$ECR_REPOSITORY_URL" | grep -Eq '^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/mycfc-production$'; then
	fail repository_invalid
fi
registry=${ECR_REPOSITORY_URL%%/*}
repository_name=${ECR_REPOSITORY_URL#*/}

for command in aws base64 docker gh jq openssl sha256sum; do
	command -v "$command" >/dev/null 2>&1 || fail runtime_dependency_missing
done

install -d -m 0700 "$state_dir" "$state_dir/processed" "$state_dir/receipts"
for protected_dir in "$state_dir" "$state_dir/processed" "$state_dir/receipts"; do
	if [ -L "$protected_dir" ] || [ "$(stat -c '%u:%g:%a' "$protected_dir" 2>/dev/null || true)" != '0:0:700' ]; then
		fail state_directory_invalid
	fi
done

work_dir=$(mktemp -d "$runtime_dir/mycfc-privacy-operation.XXXXXX")
chmod 0700 "$work_dir"
docker_config=$(mktemp -d "$runtime_dir/mycfc-privacy-operation-docker.XXXXXX")
chmod 0700 "$docker_config"
export DOCKER_CONFIG="$docker_config"
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_SHARED_CREDENTIALS_FILE="$release_credentials_file"
export AWS_PROFILE="$release_aws_profile"
export AWS_PAGER=""

aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --output json >"$work_dir/images.json"
if ! jq -e '.imageDetails | type == "array"' "$work_dir/images.json" >/dev/null; then
	fail image_inventory_invalid
fi

jq -r '
	[.imageDetails[]
	 | select(.imageDigest | test("^sha256:[0-9a-f]{64}$"))
	 | . as $image
	 | (.imageTags // [])[]
	 | select(test("^privacy-op-[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$"))
	 | {digest:$image.imageDigest,tag:.,pushed:$image.imagePushedAt}]
	| sort_by(.pushed,.tag)[]
	| [.digest,.tag] | @tsv
' "$work_dir/images.json" >"$work_dir/candidates.tsv"

candidate_digest=
candidate_tag=
while IFS="$(printf '\t')" read -r digest tag; do
	[ -n "$digest" ] || continue
	marker="$state_dir/processed/${digest#sha256:}"
	if [ ! -e "$marker" ]; then
		candidate_digest=$digest
		candidate_tag=$tag
		break
	fi
done <"$work_dir/candidates.tsv"

if [ -z "$candidate_digest" ]; then
	printf '%s\n' 'event=privacy_operation_agent_idle'
	exit 0
fi

image="$ECR_REPOSITORY_URL@$candidate_digest"
if ! aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$registry" >/dev/null 2>&1; then
	fail registry_login_failed
fi
if ! docker pull "$image" >/dev/null 2>&1; then
	fail request_pull_failed
fi
revision=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image" 2>/dev/null || true)
contract=$(docker image inspect --format '{{index .Config.Labels "org.mycfc.privacy-operation-contract"}}' "$image" 2>/dev/null || true)
if ! printf '%s' "$revision" | grep -Eq '^[0-9a-f]{40}$' ||
	[ "$contract" != mycfc/privacy-production-operation-request/v2 ]; then
	fail request_labels_invalid
fi
if ! gh attestation verify "oci://$image" \
	--repo ricardoespsanto/mycfc \
	--signer-workflow ricardoespsanto/mycfc/.github/workflows/privacy-production-operations.yml \
	--source-digest "$revision" \
	--deny-self-hosted-runners >/dev/null 2>&1; then
	fail request_attestation_invalid
fi

request_container=$(docker create "$image")
docker cp "$request_container:/privacy-operation.json" "$work_dir/request.json" >/dev/null
docker rm "$request_container" >/dev/null
request_container=
chown root:root "$work_dir/request.json"
chmod 0600 "$work_dir/request.json"

request_id=$(jq -r '.request_id // empty' "$work_dir/request.json" 2>/dev/null || true)
source_sha=$(jq -r '.source_sha // empty' "$work_dir/request.json" 2>/dev/null || true)
operation=$(jq -r '.operation // empty' "$work_dir/request.json" 2>/dev/null || true)
target_image=$(jq -r '.expected_image // empty' "$work_dir/request.json" 2>/dev/null || true)
if [ "$source_sha" != "$revision" ] || [ "$candidate_tag" != "privacy-op-$request_id" ]; then
	fail request_identity_mismatch
fi
case "$request_id" in
	*[!0-9-]* | '' | *-*-*) fail request_identity_invalid ;;
esac
if [ ! -f "$receipt_signing_key_file" ] || [ -L "$receipt_signing_key_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$receipt_signing_key_file" 2>/dev/null || true)" != '0:0:600' ]; then
	[ "$operation" = receipt-key-provision ] || fail receipt_signing_key_invalid
fi
if ! printf '%s' "$target_image" | grep -Eq "^${ECR_REPOSITORY_URL}@sha256:[0-9a-f]{64}$" ||
	! docker pull "$target_image" >/dev/null 2>&1; then
	fail receipt_signer_image_invalid
fi

receipt="$state_dir/receipts/$request_id.json"
set +e
"$deployment_dir/privacy-production-operation.sh" "$work_dir/request.json" "$receipt"
operation_status=$?
set -e

if [ ! -f "$receipt" ] || [ -L "$receipt" ] || [ "$(stat -c '%u:%g:%a' "$receipt" 2>/dev/null || true)" != '0:0:600' ]; then
	fail unsigned_receipt_invalid
fi
signed_receipt="$work_dir/signed-receipt.json"
receipt_signing_key="$receipt_signing_key_file"
if [ "$operation" = receipt-key-rotate-activate ]; then
	receipt_signing_key="$receipt_key_dir/previous/private.pem"
fi
if [ ! -f "$receipt_signing_key" ] || [ -L "$receipt_signing_key" ] ||
	[ "$(stat -c '%u:%g:%a' "$receipt_signing_key" 2>/dev/null || true)" != '0:0:600' ]; then
	receipt_fail receipt_signing_key_transition_invalid
fi
if ! docker run --rm --network none --read-only --user 0:0 \
	-v "$receipt:/receipt/unsigned.json:ro" \
	-v "$receipt_signing_key:/receipt/private.pem:ro" \
	-v "$work_dir:/receipt/output" \
	--entrypoint /app/privacy-operation-receipt "$target_image" \
	sign --input /receipt/unsigned.json --output /receipt/output/signed-receipt.json \
	--private-key /receipt/private.pem >/dev/null; then
	receipt_fail receipt_signing_failed
fi
if [ ! -f "$signed_receipt" ] || [ -L "$signed_receipt" ] ||
	[ "$(stat -c '%u:%g:%a' "$signed_receipt" 2>/dev/null || true)" != '0:0:600' ]; then
	receipt_fail signed_receipt_invalid
fi
receipt_sha256=$(sha256sum "$signed_receipt" | awk '{print $1}')
receipt_checksum=$(openssl dgst -sha256 -binary "$signed_receipt" | base64 -w0)
if [ "$operation" = receipt-key-revoke ]; then
	if ! "$deployment_dir/privacy-operation-receipt-key.sh" finalize-revoke "$request_id"; then
		fail receipt_key_revoke_finalize_failed
	fi
fi
receipt_response="$work_dir/receipt-put.json"
if ! aws s3api put-object --region "$AWS_REGION" \
	--bucket "$PRIVACY_OPERATION_RECEIPT_BUCKET" --key "receipts/$request_id.json" \
	--body "$signed_receipt" --if-none-match '*' --checksum-algorithm SHA256 \
	--server-side-encryption aws:kms --ssekms-key-id "$PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN" \
	--output json >"$receipt_response"; then
	receipt_fail receipt_transport_failed
fi
if ! jq -e --arg checksum "$receipt_checksum" --arg kms "$PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN" '
	(.VersionId | type == "string" and length > 0) and .ChecksumSHA256 == $checksum and
	.ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
' "$receipt_response" >/dev/null 2>&1; then
	receipt_fail receipt_transport_response_invalid
fi
mv "$signed_receipt" "$receipt"
printf '%s\n' "event=privacy_operation_receipt_published request_id=$request_id receipt_sha256=$receipt_sha256"
if [ "$operation" = receipt-key-rotate-activate ]; then
	if ! "$deployment_dir/privacy-operation-receipt-key.sh" finalize-rotate "$request_id"; then
		printf '%s\n' "event=privacy_operation_receipt_key_rotation_cleanup_failed request_id=$request_id"
	fi
fi

marker="$state_dir/processed/${candidate_digest#sha256:}"
temporary_marker=$(mktemp "$state_dir/processed/.processed.XXXXXX")
printf '%s\t%s\n' "$candidate_digest" "$request_id" >"$temporary_marker"
chmod 0600 "$temporary_marker"
mv "$temporary_marker" "$marker"

if [ "$operation_status" -ne 0 ]; then
	exit "$operation_status"
fi
