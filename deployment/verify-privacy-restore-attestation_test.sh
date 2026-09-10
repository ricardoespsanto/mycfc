#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin"

cat >"$work_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' '0:600'
EOF
cat >"$work_dir/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$work_dir/bin/stat" "$work_dir/bin/logger"

key=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
printf '%s\n' "$key" >"$work_dir/key"
image='registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
valid_until=$(date -u -d '+90 days' +%Y-%m-%dT%H:%M:%SZ)

jq -n \
	--arg observed_at "$now" \
	--arg valid_until "$valid_until" \
	'{contract:"mycfc/privacy-restore-drill-attestation/v2",result:"SUCCEEDED",observed_at:$observed_at,valid_until:$valid_until,policy_version:"privacy-policy-v1",executor_version:"privacy-erasure-executor/v2",plan_schema_version:"privacy-erasure-plan/v2",image_digest:"sha256:"+("a"*64),schema_migration_digest:("f"*64),contracts:{backup:"mycfc/postgres-backup/v3",ledger_input:"mycfc/privacy-restore-ledger-input/v2",replay_result:"mycfc/privacy-restore-replay-result/v2",replay:"relational-erasure-replay/v1",closure:"restore-tombstone-closure/v3",synthetic_fixture:"mycfc/privacy-restore-synthetic-fixture/v1"},backup:{created_at:"2026-09-01T02:15:00Z",manifest:{ref:"s3://test/manifest?versionId=v1",sha256:("b"*64),checksum_sha256:("b"*64),kms_key_arn:"arn:aws:kms:eu-west-1:123456789012:key/test",size_bytes:128},dump:{ref:"s3://test/dump?versionId=v2",sha256:("c"*64),checksum_sha256:("c"*64),kms_key_arn:"arn:aws:kms:eu-west-1:123456789012:key/test",size_bytes:256}},ledger:{input_source:"LIVE_LEDGER",inventory_sha256:("1"*64),object_count:1},candidate:{result_sha256:("2"*64),object_count:1,imported_count:1,replayed_count:1,already_applied_count:0,non_replayable_v1_count:0,absence_verified_count:1,synthetic_replayed_count:0,closure_v3_count:1,intent_only_count:0,legacy_closure_v2_count:0,erasure_effective_at_verified_count:1,failed_count:0},observer:{image_digest:"sha256:9f2364d2e5382f9ec8689d36d09292e6d3e442c55b83304206d6b179e56157c5",replay_count:1,source_already_applied_count:0,synthetic_count:0,verified_run_count:1,expected_checkpoint_count:5,succeeded_checkpoint_count:5,provider_absent_count:1,consent_clock_verified_count:1,closure_v3_count:1,erasure_effective_at_verified_count:1,evidence_sha256:("3"*64)},evidence:{ref:"s3://test/evidence?versionId=v3",sha256:("4"*64),checksum_sha256:("4"*64),kms_key_arn:"arn:aws:kms:eu-west-1:123456789012:key/test",size_bytes:512}}' \
	>"$work_dir/payload.json"
canonical=$(jq -Sc . "$work_dir/payload.json")
hmac=$(printf '%s' "$canonical" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$key" -binary | od -An -v -tx1 | tr -d ' \n')
jq --arg hmac "$hmac" '. + {auth_hmac_sha256:$hmac}' "$work_dir/payload.json" >"$work_dir/attestation.json"

run_verify() {
	env PATH="$work_dir/bin:$PATH" \
		MYCFC_RESTORE_ATTESTATION_FILE="$work_dir/attestation.json" \
		MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE="$work_dir/key" \
		PRIVACY_ACTIVATION_POLICY_VERSION=privacy-policy-v1 \
		sh "$deployment_dir/verify-privacy-restore-attestation.sh" "$1"
}

run_verify "$image" | grep -Eq '^privacy_restore_promotion_gate_succeeded attestation_sha256=[0-9a-f]{64} age_seconds=[0-9]+ image_digest=sha256:[0-9a-f]{64}$'

if run_verify 'registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' >/dev/null 2>&1; then
	printf '%s\n' 'An attestation for a different image passed verification.' >&2
	exit 1
fi

jq '.ledger.object_count = 3' "$work_dir/attestation.json" >"$work_dir/tampered.json"
mv "$work_dir/tampered.json" "$work_dir/attestation.json"
if run_verify "$image" >/dev/null 2>&1; then
	printf '%s\n' 'A tampered restore attestation passed verification.' >&2
	exit 1
fi

printf '%s\n' 'privacy restore attestation verification tests passed'
