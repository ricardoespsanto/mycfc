#!/bin/sh
set -eu

attestation_file=${MYCFC_RESTORE_ATTESTATION_FILE:-/etc/mycfc/privacy-restore/attestations/latest.json}
auth_key_file=${MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE:-/etc/mycfc/privacy-restore/attestation.key}
expected_image=${1:-}
maximum_age_seconds=${PRIVACY_RESTORE_ATTESTATION_MAX_AGE_SECONDS:-7776000}
expected_policy=${PRIVACY_ACTIVATION_POLICY_VERSION:-}
expected_executor=privacy-erasure-executor/v2
expected_plan_schema=privacy-erasure-plan/v2
observer_image=${MYCFC_RESTORE_OBSERVER_IMAGE:-postgres:16.9-alpine3.21@sha256:9f2364d2e5382f9ec8689d36d09292e6d3e442c55b83304206d6b179e56157c5}

fail() {
	printf '%s\n' 'privacy_restore_promotion_gate_failed'
	logger -t mycfc-privacy-restore -- 'privacy_restore_promotion_gate_failed' 2>/dev/null || true
	exit 1
}

if ! printf '%s' "$expected_image" | grep -Eq '@sha256:[0-9a-f]{64}$'; then
	fail
fi
if ! printf '%s' "$expected_policy" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$' ||
	! printf '%s' "$observer_image" | grep -Eq '^postgres:16\.9-alpine3\.21@sha256:[0-9a-f]{64}$'; then
	fail
fi
case "$maximum_age_seconds" in
	''|*[!0-9]*) fail ;;
esac
if [ "$maximum_age_seconds" -eq 0 ] || [ "$maximum_age_seconds" -gt 7776000 ]; then
	fail
fi
if [ ! -f "$attestation_file" ] || [ "$(stat -c '%u:%a' "$attestation_file")" != '0:600' ]; then
	fail
fi
if [ ! -f "$auth_key_file" ] || [ "$(stat -c '%u:%a' "$auth_key_file")" != '0:600' ]; then
	fail
fi
if ! tr -d '\n' <"$auth_key_file" | grep -Eq '^[0-9A-Fa-f]{64}$'; then
	fail
fi

expected_digest=${expected_image##*@}
expected_observer_digest=${observer_image##*@}
if ! jq -e --arg expected_digest "$expected_digest" --arg expected_policy "$expected_policy" \
	--arg expected_executor "$expected_executor" --arg expected_plan_schema "$expected_plan_schema" --arg expected_observer_digest "$expected_observer_digest" '
	(type == "object")
	and (keys | sort == ["auth_hmac_sha256","backup","candidate","contract","contracts","evidence","executor_version","image_digest","ledger","observed_at","observer","plan_schema_version","policy_version","result","schema_migration_digest","valid_until"])
	and .contract == "mycfc/privacy-restore-drill-attestation/v2"
	and .result == "SUCCEEDED"
	and .image_digest == $expected_digest
	and .policy_version == $expected_policy
	and .executor_version == $expected_executor
	and .plan_schema_version == $expected_plan_schema
	and (.observed_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	and (.valid_until | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	and (.auth_hmac_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
	and (.schema_migration_digest | type == "string" and test("^[0-9a-f]{64}$"))
	and (.contracts == {backup:"mycfc/postgres-backup/v3",ledger_input:"mycfc/privacy-restore-ledger-input/v2",replay_result:"mycfc/privacy-restore-replay-result/v2",replay:"relational-erasure-replay/v1",closure:"restore-tombstone-closure/v3",synthetic_fixture:"mycfc/privacy-restore-synthetic-fixture/v1"})
	and (.backup | type == "object" and (keys | sort == ["created_at","dump","manifest"]))
	and (.backup.created_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	and (all([.backup.manifest,.backup.dump,.evidence][];
		type == "object" and (keys | sort == ["checksum_sha256","kms_key_arn","ref","sha256","size_bytes"])
		and (.ref | type == "string" and test("^s3://[^/]+/.+\\?versionId=[^&]+$"))
		and (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and .checksum_sha256 == .sha256
		and (.kms_key_arn | type == "string" and test("^arn:aws:kms:[^:]+:[0-9]{12}:key/.+$"))
		and (.size_bytes | type == "number" and . > 0 and floor == .)))
	and (.ledger | type == "object" and (keys | sort == ["input_source","inventory_sha256","object_count"]))
	and (.ledger.input_source == "LIVE_LEDGER" or .ledger.input_source == "SYNTHETIC_BOOTSTRAP")
	and (.ledger.inventory_sha256 | test("^[0-9a-f]{64}$"))
	and (.ledger.object_count | type == "number" and . >= 0 and floor == .)
	and (.candidate | type == "object" and (keys | sort == ["absence_verified_count","already_applied_count","closure_v3_count","erasure_effective_at_verified_count","failed_count","imported_count","intent_only_count","legacy_closure_v2_count","non_replayable_v1_count","object_count","replayed_count","result_sha256","synthetic_replayed_count"]))
	and (.candidate.result_sha256 | test("^[0-9a-f]{64}$"))
	and (all(.candidate | to_entries[] | select(.key != "result_sha256") | .value; type == "number" and . >= 0 and floor == .))
	and .candidate.object_count == .ledger.object_count and .candidate.replayed_count > 0
	and .candidate.replayed_count == (.candidate.imported_count + .candidate.already_applied_count)
	and .candidate.absence_verified_count == .candidate.replayed_count
	and .candidate.closure_v3_count == .candidate.replayed_count
	and .candidate.erasure_effective_at_verified_count == .candidate.replayed_count
	and .candidate.non_replayable_v1_count == 0 and .candidate.intent_only_count == 0 and .candidate.legacy_closure_v2_count == 0 and .candidate.failed_count == 0
	and (if .ledger.input_source == "LIVE_LEDGER" then .candidate.synthetic_replayed_count == 0 else .candidate.synthetic_replayed_count == .candidate.replayed_count end)
	and (.observer | type == "object" and (keys | sort == ["closure_v3_count","consent_clock_verified_count","erasure_effective_at_verified_count","evidence_sha256","expected_checkpoint_count","image_digest","provider_absent_count","replay_count","source_already_applied_count","succeeded_checkpoint_count","synthetic_count","verified_run_count"]))
	and .observer.image_digest == $expected_observer_digest and (.observer.evidence_sha256 | test("^[0-9a-f]{64}$"))
	and .observer.replay_count == .candidate.replayed_count and .observer.source_already_applied_count == .candidate.already_applied_count
	and .observer.synthetic_count == .candidate.synthetic_replayed_count and .observer.verified_run_count == .candidate.replayed_count
	and .observer.expected_checkpoint_count > 0 and .observer.succeeded_checkpoint_count == .observer.expected_checkpoint_count
	and .observer.provider_absent_count == .candidate.replayed_count and .observer.consent_clock_verified_count == .candidate.replayed_count
	and .observer.closure_v3_count == .candidate.replayed_count and .observer.erasure_effective_at_verified_count == .candidate.replayed_count
' "$attestation_file" >/dev/null; then
	fail
fi

canonical=$(jq -Sc 'del(.auth_hmac_sha256)' "$attestation_file")
actual_hmac=$(printf '%s' "$canonical" |
	python3 -c 'import hashlib,hmac,pathlib,sys; key=bytes.fromhex(pathlib.Path(sys.argv[1]).read_text().strip()); sys.stdout.write(hmac.new(key,sys.stdin.buffer.read(),hashlib.sha256).hexdigest())' "$auth_key_file")
[ "$actual_hmac" = "$(jq -r .auth_hmac_sha256 "$attestation_file")" ] || fail

observed_epoch=$(date -u -d "$(jq -r .observed_at "$attestation_file")" +%s) || fail
valid_until_epoch=$(date -u -d "$(jq -r .valid_until "$attestation_file")" +%s) || fail
backup_created_epoch=$(date -u -d "$(jq -r .backup.created_at "$attestation_file")" +%s) || fail
now_epoch=$(date -u +%s)
age_seconds=$((now_epoch - observed_epoch))
validity_seconds=$((valid_until_epoch - observed_epoch))
if [ "$age_seconds" -lt 0 ] || [ "$age_seconds" -gt "$maximum_age_seconds" ] || [ "$validity_seconds" -ne 7776000 ] ||
	[ "$backup_created_epoch" -gt "$observed_epoch" ] || [ "$now_epoch" -gt "$valid_until_epoch" ]; then
	fail
fi

attestation_sha256=$(sha256sum "$attestation_file" | awk '{print $1}')
event="privacy_restore_promotion_gate_succeeded attestation_sha256=$attestation_sha256 age_seconds=$age_seconds image_digest=$expected_digest"
printf '%s\n' "$event"
logger -t mycfc-privacy-restore -- "$event" 2>/dev/null || true
