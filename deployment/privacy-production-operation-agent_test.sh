#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/deployment" "$test_dir/runtime"

sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
digest=$(printf 'b%.0s' $(seq 1 64))
target_digest=$(printf 'c%.0s' $(seq 1 64))
request_id=201-1
repository=123456789012.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
path=$3
if [ -d "$path" ]; then printf '%s\n' '0:0:700'; else printf '%s\n' '0:0:600'; fi
EOF
cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$AWS_CALLS"
case "$*" in
	*'ecr describe-images'*) cat "$IMAGE_INVENTORY" ;;
	*'ecr get-login-password'*) printf '%s\n' token ;;
	*'s3api put-object'*)
		body=
		previous=
		for arg in "$@"; do [ "$previous" != --body ] || body=$arg; previous=$arg; done
		checksum=$(openssl dgst -sha256 -binary "$body" | base64 -w0)
		jq -cn --arg checksum "$checksum" '{VersionId:"receipt-version",ChecksumSHA256:$checksum,ServerSideEncryption:"aws:kms",SSEKMSKeyId:"arn:aws:kms:eu-west-1:123456789012:key/receipt"}'
		;;
	*) exit 1 ;;
esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_CALLS"
case "$1:$2" in
	login:*) cat >/dev/null ;;
	pull:*) ;;
	run:*)
		output_dir=
		previous=
		for arg in "$@"; do
			if [ "$previous" = -v ]; then case "$arg" in *:/receipt/output) output_dir=${arg%:/receipt/output} ;; esac; fi
			previous=$arg
		done
		printf '%s\n' '{"contract":"mycfc/privacy-production-operation-receipt/v2","signature_ed25519":"test"}' >"$output_dir/signed-receipt.json"
		chmod 0600 "$output_dir/signed-receipt.json"
		;;
	image:inspect)
		case "$*" in
			*org.opencontainers.image.revision*) printf '%s\n' "$EXPECTED_SHA" ;;
			*org.mycfc.privacy-operation-contract*) printf '%s\n' mycfc/privacy-production-operation-request/v2 ;;
		esac
		;;
	create:*) printf '%s\n' request-container ;;
	cp:*) cp "$REQUEST_FIXTURE" "$3" ;;
	rm:*) ;;
	*) exit 1 ;;
esac
EOF
cat >"$test_dir/bin/gh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$GH_CALLS"
[ "${GH_RESULT:-success}" = success ]
EOF
cat >"$test_dir/deployment/privacy-production-operation.sh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$WRAPPER_CALLS"
mkdir -p "$(dirname "$2")"
printf '%s\n' '{"result":"SUCCEEDED"}' >"$2"
chmod 0600 "$2"
printf '%s\n' 'event=privacy_operation_succeeded request_id=201-1 operation=status receipt_contract=mycfc/privacy-production-operation-receipt/v2'
EOF
chmod 0755 "$test_dir/bin/"* "$test_dir/deployment/privacy-production-operation.sh"

env_file="$test_dir/mycfc.env"
credentials="$test_dir/release-credentials"
receipt_key="$test_dir/receipt-private.pem"
inventory="$test_dir/images.json"
request="$test_dir/request.json"
state_dir="$test_dir/state"
calls="$test_dir/calls"
mkdir -p "$calls"
touch "$credentials" "$receipt_key"
chmod 0600 "$env_file" "$credentials" "$receipt_key" 2>/dev/null || true
cat >"$env_file" <<EOF
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=true
AWS_REGION=eu-west-1
ECR_REPOSITORY_URL=$repository
PRIVACY_OPERATION_RECEIPT_BUCKET=test-receipts
PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN=arn:aws:kms:eu-west-1:123456789012:key/receipt
EOF
chmod 0600 "$env_file"
cat >"$inventory" <<EOF
{"imageDetails":[{"imageDigest":"sha256:$digest","imageTags":["privacy-op-$request_id"],"imagePushedAt":"2026-09-16T15:00:00Z"},{"imageDigest":"sha256:$target_digest","imageTags":["release-unrelated"],"imagePushedAt":"2026-09-16T14:00:00Z"}]}
EOF
issued=$(date -u +%Y-%m-%dT%H:%M:%SZ)
expires=$(date -u -d "$issued + 10 minutes" +%Y-%m-%dT%H:%M:%SZ)
jq -cS -n --arg sha "$sha" --arg image "$repository@sha256:$target_digest" --arg issued "$issued" --arg expires "$expires" \
	'{contract:"mycfc/privacy-production-operation-request/v2",request_id:"201-1",operation:"status",source_sha:$sha,expected_image:$image,evidence_sha256:("0"*64),issued_at:$issued,expires_at:$expires,workflow_run_id:201,workflow_run_attempt:1}' >"$request"

export AWS_CALLS="$calls/aws" DOCKER_CALLS="$calls/docker" GH_CALLS="$calls/gh" WRAPPER_CALLS="$calls/wrapper"
export IMAGE_INVENTORY="$inventory" REQUEST_FIXTURE="$request" EXPECTED_SHA="$sha"

run_agent() {
	PATH="$test_dir/bin:$PATH" \
		MYCFC_ENV_FILE="$env_file" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$credentials" \
		MYCFC_PRIVACY_OPERATION_RECEIPT_SIGNING_KEY_FILE="$receipt_key" \
		MYCFC_PRIVACY_OPERATION_STATE_DIR="$state_dir" \
		MYCFC_DEPLOYMENT_DIR="$test_dir/deployment" \
		MYCFC_RUNTIME_DIR="$test_dir/runtime" \
		sh "$script_dir/privacy-production-operation-agent.sh"
}

output=$(run_agent)
printf '%s' "$output" | grep -q 'event=privacy_operation_succeeded request_id=201-1 operation=status'
grep -q "^$test_dir/runtime/.*request.json $state_dir/receipts/201-1.json$" "$WRAPPER_CALLS"
grep -q -- '--signer-workflow ricardoespsanto/mycfc/.github/workflows/privacy-production-operations.yml' "$GH_CALLS"
grep -q -- "--source-digest $sha" "$GH_CALLS"
grep -q -- '--key receipts/201-1.json' "$AWS_CALLS"
grep -q -- '--if-none-match \*' "$AWS_CALLS"
grep -q -- '--checksum-algorithm SHA256' "$AWS_CALLS"
grep -q -- '--ssekms-key-id arn:aws:kms:eu-west-1:123456789012:key/receipt' "$AWS_CALLS"
jq -e '.contract == "mycfc/privacy-production-operation-receipt/v2" and .signature_ed25519 == "test"' "$state_dir/receipts/201-1.json" >/dev/null
test -f "$state_dir/processed/$digest"
if grep -R -q 'token' "$state_dir"; then
	printf '%s\n' 'operation state retained registry credentials' >&2
	exit 1
fi

idle=$(run_agent)
[ "$idle" = 'event=privacy_operation_agent_idle' ] || { printf '%s\n' 'processed request was replayed' >&2; exit 1; }
[ "$(wc -l <"$WRAPPER_CALLS")" -eq 1 ]

rm -rf "$state_dir"
: >"$WRAPPER_CALLS"
if GH_RESULT=failure run_agent >"$test_dir/attestation.out" 2>&1; then
	printf '%s\n' 'unattested operation request was accepted' >&2
	exit 1
fi
grep -q 'reason=request_attestation_invalid' "$test_dir/attestation.out"
[ ! -s "$WRAPPER_CALLS" ]

cat >"$env_file" <<EOF
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=false
AWS_REGION=eu-west-1
ECR_REPOSITORY_URL=$repository
PRIVACY_OPERATION_RECEIPT_BUCKET=test-receipts
PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN=arn:aws:kms:eu-west-1:123456789012:key/receipt
EOF
: >"$AWS_CALLS"
disabled=$(run_agent)
[ "$disabled" = 'event=privacy_operation_agent_disabled' ]
[ ! -s "$AWS_CALLS" ]

printf '%s\n' 'privacy production operation agent tests passed'
