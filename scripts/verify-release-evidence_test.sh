#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin"
cat >"$work_dir/bin/aws" <<'EOF'
#!/bin/sh
printf '%s\n' "$TEST_LOG_JSON"
EOF
chmod +x "$work_dir/bin/aws"

sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
schema=cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
manifest=dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
now=$(date -u +%s)
started=$(date -u -d "@$((now - 2))" +%Y-%m-%dT%H:%M:%SZ)
finished=$(date -u -d "@$now" +%Y-%m-%dT%H:%M:%SZ)
receipt=$work_dir/source-receipt.json
env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST=$schema PUBLICATION_MANIFEST_SHA256=$manifest \
	RELEASE_RESULT=succeeded RELEASE_SLOT=green TRAFFIC_SWITCHED=true ROLLBACK_PERFORMED=false \
	GUARDIAN_INTAKE_ACTIVE=false PRIVACY_WORKER_ACTIVE=true PRIVACY_WORKER_ACTIVATION_REQUIRED=false \
	RELEASE_STARTED_AT="$started" RELEASE_FINISHED_AT="$finished" RELEASE_EVIDENCE_OUTPUT="$receipt" \
	sh "$script_dir/release-evidence.sh" receipt
receipt_sha=$(sha256sum "$receipt" | awk '{print $1}')
event="event=deployment_receipt result=succeeded version=v1.25.0 sha=$sha digest=$digest schema_migration_digest=$schema manifest_sha256=$manifest slot=green failure_phase=none traffic_switched=true rollback_performed=false guardian_intake_active=false privacy_worker_active=true privacy_worker_activation_required=false started_at=$started finished_at=$finished receipt_sha256=$receipt_sha"
event_ms=$((now * 1000))
wrapped_event="host=mycfc-production exit_status=0
$event
event=deployment_succeeded sha=$sha"
log_json=$(jq -cn --argjson timestamp "$event_ms" --arg message "$wrapped_event" '[{timestamp:$timestamp,message:$message}]')
output=$work_dir/verified-receipt.json

run_verify() {
	PATH="$work_dir/bin:$PATH" TEST_LOG_JSON="$1" AWS_REGION=eu-west-1 CLOUDWATCH_LOG_GROUP=/mycfc/production/deployment \
		RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
		RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST=$schema PUBLICATION_MANIFEST_SHA256=$manifest \
		EXPECTED_GATES_JSON='{"guardian_intake":false,"privacy_worker":true}' \
		RELEASE_EVIDENCE_START_TIME_MS=$((event_ms - 1000)) RELEASE_EVIDENCE_OUTPUT="$output" RELEASE_EVIDENCE_ATTEMPTS=1 \
		sh "$script_dir/verify-release-evidence.sh"
}

run_verify "$log_json" | grep -Fq "$event"
cmp "$receipt" "$output"

bad_json=$(jq -cn --argjson timestamp "$event_ms" --arg message "host=mycfc-production exit_status=0\n$event unexpected=value" '[{timestamp:$timestamp,message:$message}]')
if run_verify "$bad_json" >/dev/null 2>&1; then printf '%s\n' 'unexpected receipt field was accepted' >&2; exit 1; fi
secret_json=$(jq -cn --argjson timestamp "$event_ms" --arg message "host=mycfc-production exit_status=0\n$event password=canary" '[{timestamp:$timestamp,message:$message}]')
if run_verify "$secret_json" >/dev/null 2>&1; then printf '%s\n' 'credential canary was accepted' >&2; exit 1; fi
old_json=$(jq -cn --argjson timestamp "$((event_ms - 5000))" --arg message "$wrapped_event" '[{timestamp:$timestamp,message:$message}]')
if run_verify "$old_json" >/dev/null 2>&1; then printf '%s\n' 'receipt before publication boundary was accepted' >&2; exit 1; fi

printf '%s\n' 'verify-release-evidence tests passed'
