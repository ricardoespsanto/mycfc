#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/deployment" "$test_dir/etc/privacy-activation-exchange" "$test_dir/etc/privacy-activation" "$test_dir/state"

source_sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
image_digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
schema_digest=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
exchange_key=arn:aws:kms:eu-west-1:123456789012:key/11111111-1111-4111-8111-111111111111
courier_arn=arn:aws:iam::123456789012:user/mycfc-production-privacy-activation-exchange-courier
bucket=mycfc-production-activation-test
state_root=$test_dir/state

cat >"$test_dir/main.env" <<EOF
GIT_SHA=$source_sha
MYCFC_IMAGE=123456789012.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@$image_digest
EOF
printf '%s' '{"contract":"mycfc/privacy-activation-signer-registry/v1"}' >"$test_dir/etc/privacy-activation/signer-registry.json"
printf '%s' executor-public-key >"$test_dir/etc/privacy-activation/executor-public-key-spki.der"
printf '%s' administrator-public-key >"$test_dir/etc/privacy-activation/administrator-public-key-spki.der"
registry_sha=$(sha256sum "$test_dir/etc/privacy-activation/signer-registry.json" | awk '{print $1}')
cat >"$test_dir/exchange.env" <<EOF
PRIVACY_ACTIVATION_EXCHANGE_ENABLED=true
AWS_REGION=eu-west-1
PRIVACY_ACTIVATION_EXCHANGE_BUCKET=$bucket
PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN=$exchange_key
PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN=$courier_arn
PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN=arn:aws:iam::123456789012:user/mycfc-production-release-agent
PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256=$registry_sha
PRIVACY_ACTIVATION_POLICY_VERSION=club-2026-09-15-v1
EOF
cat >"$test_dir/etc/privacy-activation-exchange/aws-credentials" <<'EOF'
[mycfc-privacy-activation-courier]
aws_access_key_id = AKIAABCDEFGHIJKLMNOP
aws_secret_access_key = test-secret-not-real
EOF
chmod 0600 "$test_dir/main.env" "$test_dir/exchange.env" "$test_dir/etc/privacy-activation-exchange/aws-credentials" "$test_dir/etc/privacy-activation/"*

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
for argument in "$@"; do path=$argument; done
if [ -d "$path" ]; then printf '%s\n' '0:0:700'; else printf '%s\n' '0:0:600'; fi
EOF
cat >"$test_dir/bin/flock" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/systemctl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$SYSTEMCTL_CALLS"
EOF
cat >"$test_dir/bin/install" <<'EOF'
#!/bin/sh
set -eu
for argument in "$@"; do
	case "$argument" in -*) ;; root | 0700 | 0600) ;; *) mkdir -p "$argument"; chmod 0700 "$argument" ;; esac
done
EOF
cat >"$test_dir/bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$test_dir/bin/"*

printf '%s' '{"contract":"mycfc/privacy-activation-approval/v2","role":"EXECUTOR"}' >"$test_dir/executor-source.json"
printf '%s' '{"contract":"mycfc/privacy-activation-approval/v2","role":"ADMINISTRATOR"}' >"$test_dir/administrator-source.json"
executor_checksum=$(openssl dgst -sha256 -binary "$test_dir/executor-source.json" | base64 -w0)
administrator_checksum=$(openssl dgst -sha256 -binary "$test_dir/administrator-source.json" | base64 -w0)

cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
set -eu
service=$1
operation=$2
shift 2
printf '%s:%s %s\n' "$service" "$operation" "$*" >>"$AWS_CALLS"
case "$service:$operation" in
	sts:get-caller-identity)
		printf '{"Arn":"%s","Account":"123456789012","UserId":"test"}\n' "$COURIER_ARN"
		;;
	s3api:put-object)
		body=
		key=
		while [ "$#" -gt 0 ]; do
			case "$1" in --body) body=$2; shift 2 ;; --key) key=$2; shift 2 ;; *) shift ;; esac
		done
		checksum=$(openssl dgst -sha256 -binary "$body" | base64 -w0)
		case "$key" in */material.json) version='Material+Version/Exact=' ;; */receipt.json) version='Receipt+Version/Exact=' ;; *) exit 80 ;; esac
		printf '{"VersionId":"%s","ChecksumSHA256":"%s","ServerSideEncryption":"aws:kms","SSEKMSKeyId":"%s"}\n' "$version" "$checksum" "$EXCHANGE_KEY"
		;;
	s3api:get-object-attributes)
		key=
		while [ "$#" -gt 0 ]; do case "$1" in --key) key=$2; shift 2 ;; *) shift ;; esac; done
		case "${TEST_APPROVAL_MODE:-partial}:$key" in
			partial:*/administrator.json) printf '%s\n' 'An error occurred (NoSuchKey) 404 Not Found' >&2; exit 1 ;;
			invalid-latest:*/administrator.json) printf '%s\n' '{"VersionId":"","Checksum":{},"ObjectSize":12}' ;;
			*:*/executor.json) printf '{"VersionId":"Executor+Version/Exact=","Checksum":{"ChecksumSHA256":"%s"},"ObjectSize":70}\n' "$EXECUTOR_CHECKSUM" ;;
			*:*/administrator.json) printf '{"VersionId":"Administrator+Version/Exact=","Checksum":{"ChecksumSHA256":"%s"},"ObjectSize":75}\n' "$ADMINISTRATOR_CHECKSUM" ;;
			*) exit 81 ;;
		esac
		;;
	s3api:get-object)
		key=
		destination=
		while [ "$#" -gt 0 ]; do
			case "$1" in --key) key=$2; shift 2 ;; *) destination=$1; shift ;; esac
		done
		case "$key" in
			*/executor.json)
				if [ "${TEST_APPROVAL_MODE:-}" = checksum-mismatch ]; then printf '%s' tampered >"$destination"; else cp "$EXECUTOR_SOURCE" "$destination"; fi
				version='Executor+Version/Exact='; expected=$EXECUTOR_CHECKSUM ;;
			*/administrator.json) cp "$ADMINISTRATOR_SOURCE" "$destination"; version='Administrator+Version/Exact='; expected=$ADMINISTRATOR_CHECKSUM ;;
			*) exit 82 ;;
		esac
		printf '{"VersionId":"%s","ChecksumSHA256":"%s","ServerSideEncryption":"aws:kms","SSEKMSKeyId":"%s"}\n' "$version" "$expected" "$EXCHANGE_KEY"
		;;
	*) exit 83 ;;
esac
EOF
chmod 0755 "$test_dir/bin/aws"

cat >"$test_dir/deployment/privacy-activation.sh" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$1" >>"$ACTIVATION_CALLS"
case "$1" in
	prepare-exchange)
		prepared_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
		expires_at=$(date -u -d "$prepared_at + 10 minutes" +%Y-%m-%dT%H:%M:%SZ)
		ceremony_id=${TEST_CEREMONY_ID:-11111111-1111-4111-8111-111111111111}
		jq -cS -n --arg ceremony "$ceremony_id" --arg source "$SOURCE_SHA" --arg image "$IMAGE_DIGEST" \
			--arg schema "$SCHEMA_DIGEST" --arg registry "$REGISTRY_SHA" --arg prepared "$prepared_at" --arg expires "$expires_at" \
			'{contract:"mycfc/privacy-activation-approval-material/v2",ceremony_id:$ceremony,proposal_id:"22222222-2222-4222-8222-222222222222",source_sha:$source,policy_version:"club-2026-09-15-v1",evidence_ids:["11111111-1111-4111-8111-111111111111","22222222-2222-4222-8222-222222222222","33333333-3333-4333-8333-333333333333","44444444-4444-4444-8444-444444444444"],evidence_set_sha256:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",activation_sha256:"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",executor_version:"v1",plan_schema_version:"v1",image_digest:$image,schema_migration_digest:$schema,signer_registry_sha256:$registry,prepared_at:$prepared,ceremony_expires_at:$expires}' \
			>"$MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR/prepare/material.json"
		chmod 0600 "$MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR/prepare/material.json"
		;;
	verify-exchange | activate-exchange) ;;
	*) exit 84 ;;
esac
EOF
chmod 0755 "$test_dir/deployment/privacy-activation.sh"

export AWS_CALLS="$test_dir/aws.calls" SYSTEMCTL_CALLS="$test_dir/systemctl.calls" ACTIVATION_CALLS="$test_dir/activation.calls"
export COURIER_ARN="$courier_arn" EXCHANGE_KEY="$exchange_key" EXECUTOR_SOURCE="$test_dir/executor-source.json" ADMINISTRATOR_SOURCE="$test_dir/administrator-source.json"
export EXECUTOR_CHECKSUM="$executor_checksum" ADMINISTRATOR_CHECKSUM="$administrator_checksum"
export SOURCE_SHA="$source_sha" IMAGE_DIGEST="$image_digest" SCHEMA_DIGEST="$schema_digest" REGISTRY_SHA="$registry_sha"

run_exchange() {
	PATH="$test_dir/bin:$PATH" \
		MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$test_dir/exchange.env" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIALS_FILE="$test_dir/etc/privacy-activation-exchange/aws-credentials" \
		MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$test_dir/etc/privacy-activation/signer-registry.json" \
		MYCFC_PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_FILE="$test_dir/etc/privacy-activation/executor-public-key-spki.der" \
		MYCFC_PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_FILE="$test_dir/etc/privacy-activation/administrator-public-key-spki.der" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$state_root" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_LOCK_FILE="$test_dir/exchange.lock" \
		MYCFC_DEPLOYMENT_DIR="$test_dir/deployment" \
		TEST_APPROVAL_MODE="${TEST_APPROVAL_MODE:-partial}" TEST_CEREMONY_ID="${TEST_CEREMONY_ID:-11111111-1111-4111-8111-111111111111}" \
		sh "$script_dir/privacy-activation-exchange.sh" "$1"
}

open_output=$(run_exchange open)
printf '%s' "$open_output" | grep -Eq '^event=privacy_activation_ceremony_opened ceremony_id=[0-9a-f-]{36} material_sha256=[0-9a-f]{64} material_version_id=Material\+Version/Exact= expires_at=.* source_sha=[0-9a-f]{40} image_digest=sha256:[0-9a-f]{64} schema_migration_digest=[0-9a-f]{64}$'
[ -f "$state_root/active/material.json" ] && [ -f "$state_root/active/state.json" ]
grep -q -- 'put-object .*--if-none-match \*' "$AWS_CALLS"
grep -q '^start mycfc-privacy-activation-collector.timer$' "$SYSTEMCTL_CALLS"

partial_output=$(TEST_APPROVAL_MODE=partial run_exchange collect)
printf '%s' "$partial_output" | grep -q 'approvals_present=1'
[ -d "$state_root/active" ]
if grep -q '^activate-exchange$' "$ACTIVATION_CALLS"; then
	printf '%s\n' 'one approval unexpectedly activated privacy processing' >&2
	exit 1
fi

complete_output=$(TEST_APPROVAL_MODE=complete run_exchange collect)
printf '%s' "$complete_output" | grep -q 'result=SUCCEEDED independent_signatures=2'
grep -q '^verify-exchange$' "$ACTIVATION_CALLS"
grep -q '^activate-exchange$' "$ACTIVATION_CALLS"
[ ! -e "$state_root/active" ]
receipt="$state_root/receipts/11111111-1111-4111-8111-111111111111.json"
jq -e '
	.contract == "mycfc/privacy-activation-exchange-receipt/v1" and .result == "SUCCEEDED" and .reason == null and
	(.executor_approval_version_id | type == "string") and (.administrator_approval_version_id | type == "string") and
	(keys | all(. != "actor_ref" and . != "signature_der_base64"))
' "$receipt" >/dev/null

second=33333333-3333-4333-8333-333333333333
TEST_CEREMONY_ID=$second run_exchange open >/dev/null
jq '.ceremony_expires_at="2000-01-01T00:00:00Z"' "$state_root/active/state.json" >"$state_root/active/.state" && mv "$state_root/active/.state" "$state_root/active/state.json"
chmod 0600 "$state_root/active/state.json"
before_activations=$(grep -c '^activate-exchange$' "$ACTIVATION_CALLS")
expired_output=$(TEST_CEREMONY_ID=$second TEST_APPROVAL_MODE=partial run_exchange collect)
printf '%s' "$expired_output" | grep -q 'result=EXPIRED reason=material_expired'
[ "$(grep -c '^activate-exchange$' "$ACTIVATION_CALLS")" -eq "$before_activations" ]
[ ! -e "$state_root/active" ]

third=44444444-4444-4444-8444-444444444444
TEST_CEREMONY_ID=$third run_exchange open >/dev/null
if TEST_CEREMONY_ID=$third TEST_APPROVAL_MODE=checksum-mismatch run_exchange collect >"$test_dir/checksum.out" 2>&1; then
	printf '%s\n' 'checksum-mismatched approval unexpectedly activated' >&2
	exit 1
fi
grep -q 'reason=approval_checksum_or_version_mismatch' "$test_dir/checksum.out"
[ ! -e "$state_root/active" ]

fourth=55555555-5555-4555-8555-555555555555
TEST_CEREMONY_ID=$fourth run_exchange open >/dev/null
if TEST_CEREMONY_ID=$fourth TEST_APPROVAL_MODE=invalid-latest run_exchange collect >"$test_dir/invalid-latest.out" 2>&1; then
	printf '%s\n' 'invalid latest approval object unexpectedly remained active' >&2
	exit 1
fi
grep -q 'reason=approval_transport_invalid' "$test_dir/invalid-latest.out"
[ ! -e "$state_root/active" ]

ln -s "$test_dir/exchange.env" "$test_dir/exchange-link.env"
if PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$test_dir/exchange-link.env" \
	MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIALS_FILE="$test_dir/etc/privacy-activation-exchange/aws-credentials" \
	sh "$script_dir/privacy-activation-exchange.sh" collect >"$test_dir/symlink.out" 2>&1; then
	printf '%s\n' 'symlinked exchange environment unexpectedly passed' >&2
	exit 1
fi
grep -q 'reason=exchange_environment_invalid' "$test_dir/symlink.out"

printf '%s\n' 'privacy activation exchange open, collector, expiry, checksum and cleanup tests passed'
