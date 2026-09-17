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

operation_allowlisted() {
	case "$1" in
		preflight | status | infrastructure-observe | policy-import | activation-disable-provision | \
		receipt-key-provision | receipt-key-rotate-prepare | receipt-key-rotate-activate | receipt-key-revoke | \
		retention-provision | retention-rotate | retention-revoke | retention-run | retention-enable | retention-disable | \
		acceptance-provision | acceptance-rotate | acceptance-revoke | acceptance-run | \
		acceptance-canary-retry | acceptance-canary-failure | acceptance-canary-aged | acceptance-canary-heartbeat | acceptance-canary-recovery | \
		legacy-inventory | legacy-purge | legacy-verify | legacy-credential-remove | \
		backup-run | backup-posture | backup-cleanup-inventory | restore-run | restore-verify | \
		activation-record | activation-courier-provision | activation-courier-rotate | activation-courier-revoke | activation-ceremony-open | activation-disable | \
		flags-enable | flags-disable | worker-enable | worker-disable) return 0 ;;
		*) return 1 ;;
	esac
}

candidate_rejected() {
	rejected_candidates=$((rejected_candidates + 1))
	printf '%s\n' "event=privacy_operation_agent_candidate_rejected digest=$digest tag=$tag reason=$1"
}

record_processed() {
	processed_digest=$1
	processed_result=$2
	processed_marker="$state_dir/processed/${processed_digest#sha256:}"
	temporary_marker=$(mktemp "$state_dir/processed/.processed.XXXXXX")
	printf '%s\t%s\n' "$processed_digest" "$processed_result" >"$temporary_marker"
	chmod 0600 "$temporary_marker"
	mv "$temporary_marker" "$processed_marker"
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
if ! printf '%s' "${GIT_SHA:-}" | grep -Eq '^[0-9a-f]{40}$' ||
	! printf '%s' "${MYCFC_IMAGE:-}" | grep -Eq "^${ECR_REPOSITORY_URL}@sha256:[0-9a-f]{64}$"; then
	fail active_release_identity_invalid
fi
registry=${ECR_REPOSITORY_URL%%/*}
repository_name=${ECR_REPOSITORY_URL#*/}

for command in aws base64 cmp docker gh jq openssl sha256sum; do
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

if ! jq -r '
	[.imageDetails[]
	 | select(.imageDigest | test("^sha256:[0-9a-f]{64}$"))
	 | . as $image
	 | (.imageTags // [])[]
	 | select(test("^privacy-op-[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$"))
	 | {digest:$image.imageDigest,tag:.,pushed:$image.imagePushedAt}]
	| sort_by(.pushed,.tag)
	| if length > 128 then .[-128:] else . end
	| .[]
	| [.digest,.tag] | @tsv
' "$work_dir/images.json" >"$work_dir/candidates.tsv"; then
	fail image_inventory_invalid
fi

candidate_digest=
rejected_candidates=0
if ! aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$registry" >/dev/null 2>&1; then
	fail registry_login_failed
fi
while IFS="$(printf '\t')" read -r digest tag; do
	[ -n "$digest" ] || continue
	marker="$state_dir/processed/${digest#sha256:}"
	[ ! -e "$marker" ] || continue
	image="$ECR_REPOSITORY_URL@$digest"
	if ! docker pull "$image" >/dev/null 2>&1; then
		fail request_pull_failed
	fi
	if ! revision=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image" 2>/dev/null) ||
		! contract=$(docker image inspect --format '{{index .Config.Labels "org.mycfc.privacy-operation-contract"}}' "$image" 2>/dev/null); then
		fail request_image_inspect_failed
	fi
	if ! printf '%s' "$revision" | grep -Eq '^[0-9a-f]{40}$' ||
		[ "$contract" != mycfc/privacy-production-operation-request/v2 ]; then
		candidate_rejected request_labels_invalid
		continue
	fi
	if ! gh attestation verify "oci://$image" \
		--repo ricardoespsanto/mycfc \
		--signer-workflow ricardoespsanto/mycfc/.github/workflows/privacy-production-operations.yml \
		--source-digest "$revision" \
		--deny-self-hosted-runners >/dev/null 2>&1; then
		candidate_rejected request_attestation_invalid
		continue
	fi

	request_candidate="$work_dir/request-${digest#sha256:}.json"
	if ! request_container=$(docker create "$image"); then
		fail request_container_create_failed
	fi
	if ! docker cp "$request_container:/privacy-operation.json" "$request_candidate" >/dev/null 2>&1; then
		if ! docker inspect "$request_container" >/dev/null 2>&1; then
			fail request_container_invalid
		fi
		docker rm "$request_container" >/dev/null
		request_container=
		candidate_rejected request_payload_missing
		continue
	fi
	docker rm "$request_container" >/dev/null
	request_container=
	chown root:root "$request_candidate"
	chmod 0600 "$request_candidate"

	canonical_request="$work_dir/canonical-${digest#sha256:}.json"
	if ! jq -cS '.' "$request_candidate" >"$canonical_request" 2>/dev/null ||
		! cmp -s "$request_candidate" "$canonical_request" ||
		! jq -e '
			type == "object" and
			(keys | sort) == ["contract","evidence_sha256","expected_image","expires_at","issued_at","operation","request_id","source_sha","workflow_run_attempt","workflow_run_id"] and
			.contract == "mycfc/privacy-production-operation-request/v2" and
			(.request_id | type == "string" and test("^[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$")) and
			(.operation | type == "string") and
			(.source_sha | type == "string" and test("^[0-9a-f]{40}$")) and
			(.expected_image | type == "string") and
			(.evidence_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
			(.issued_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
			(.expires_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
			(.workflow_run_id | type == "number" and . > 0 and floor == .) and
			(.workflow_run_attempt | type == "number" and . > 0 and floor == .) and
			.request_id == ((.workflow_run_id | tostring) + "-" + (.workflow_run_attempt | tostring))
		' "$request_candidate" >/dev/null 2>&1; then
		candidate_rejected request_contract_invalid
		continue
	fi

	request_id=$(jq -r .request_id "$request_candidate")
	source_sha=$(jq -r .source_sha "$request_candidate")
	operation=$(jq -r .operation "$request_candidate")
	target_image=$(jq -r .expected_image "$request_candidate")
	issued_at=$(jq -r .issued_at "$request_candidate")
	expires_at=$(jq -r .expires_at "$request_candidate")
	if [ "$source_sha" != "$revision" ] || [ "$tag" != "privacy-op-$request_id" ]; then
		candidate_rejected request_identity_mismatch
		continue
	fi
	if ! operation_allowlisted "$operation" ||
		! printf '%s' "$target_image" | grep -Eq "^${ECR_REPOSITORY_URL}@sha256:[0-9a-f]{64}$"; then
		candidate_rejected request_contract_invalid
		continue
	fi
	issued_epoch=$(date -u -d "$issued_at" +%s 2>/dev/null || true)
	expires_epoch=$(date -u -d "$expires_at" +%s 2>/dev/null || true)
	now_epoch=$(date -u +%s)
	case "$issued_epoch:$expires_epoch" in
		*[!0-9:]*)
			candidate_rejected request_time_invalid
			continue
			;;
	esac
	if [ "$expires_epoch" -le "$now_epoch" ]; then
		printf '%s\n' "event=privacy_operation_agent_candidate_skipped digest=$digest tag=$tag request_id=$request_id reason=request_expired"
		record_processed "$digest" "skipped:$request_id:request_expired"
		continue
	fi
	if [ "$issued_epoch" -gt "$((now_epoch + 30))" ] || [ "$expires_epoch" -le "$issued_epoch" ] ||
		[ "$((expires_epoch - issued_epoch))" -gt 900 ]; then
		candidate_rejected request_time_invalid
		continue
	fi
	if [ "$source_sha" != "$GIT_SHA" ]; then
		printf '%s\n' "event=privacy_operation_agent_candidate_skipped digest=$digest tag=$tag request_id=$request_id reason=source_sha_not_active"
		record_processed "$digest" "skipped:$request_id:source_sha_not_active"
		continue
	fi
	if [ "$target_image" != "$MYCFC_IMAGE" ]; then
		printf '%s\n' "event=privacy_operation_agent_candidate_skipped digest=$digest tag=$tag request_id=$request_id reason=image_not_active"
		record_processed "$digest" "skipped:$request_id:image_not_active"
		continue
	fi

	candidate_digest=$digest
	mv "$request_candidate" "$work_dir/request.json"
	break
done <"$work_dir/candidates.tsv"

if [ -z "$candidate_digest" ]; then
	if [ "$rejected_candidates" -ne 0 ]; then
		fail no_eligible_candidate
	fi
	printf '%s\n' 'event=privacy_operation_agent_idle'
	exit 0
fi

image="$ECR_REPOSITORY_URL@$candidate_digest"
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

record_processed "$candidate_digest" "$request_id"

if [ "$operation_status" -ne 0 ]; then
	exit "$operation_status"
fi
