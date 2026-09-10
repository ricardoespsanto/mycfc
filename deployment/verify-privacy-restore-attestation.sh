#!/bin/sh
set -eu

attestation_file=${MYCFC_RESTORE_ATTESTATION_FILE:-/etc/mycfc/privacy-restore/attestations/latest.json}
auth_key_file=${MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE:-/etc/mycfc/privacy-restore/attestation.key}
expected_image=${1:-}
maximum_age_seconds=${PRIVACY_RESTORE_ATTESTATION_MAX_AGE_SECONDS:-7776000}

fail() {
	printf '%s\n' 'privacy_restore_promotion_gate_failed'
	logger -t mycfc-privacy-restore -- 'privacy_restore_promotion_gate_failed' 2>/dev/null || true
	exit 1
}

if ! printf '%s' "$expected_image" | grep -Eq '@sha256:[0-9a-f]{64}$'; then
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
if ! jq -e --arg expected_digest "$expected_digest" '
	(type == "object")
	and (keys | sort == ["auth_hmac_sha256","backup","completed_at","contract","image_digest","ledger","replay","result","schema_migration_digest","valid_until"])
	and .contract == "mycfc/privacy-restore-drill-attestation/v1"
	and .result == "SUCCEEDED"
	and .image_digest == $expected_digest
	and (.completed_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	and (.valid_until | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
	and (.auth_hmac_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
	and (.schema_migration_digest | type == "string" and test("^[0-9a-f]{64}$"))
	and (.backup | type == "object" and (keys | sort == ["created_at","dump_key_sha256","dump_sha256","dump_version","manifest_key_sha256","manifest_sha256","manifest_version"]))
	and (.backup.created_at | type == "string" and length > 0)
	and (.backup.dump_key_sha256 | test("^[0-9a-f]{64}$"))
	and (.backup.dump_sha256 | test("^[0-9a-f]{64}$"))
	and (.backup.dump_version | type == "string" and length > 0)
	and (.backup.manifest_key_sha256 | test("^[0-9a-f]{64}$"))
	and (.backup.manifest_sha256 | test("^[0-9a-f]{64}$"))
	and (.backup.manifest_version | type == "string" and length > 0)
	and (.ledger | type == "object" and (keys | sort == ["inventory_sha256","object_count"]))
	and (.ledger.inventory_sha256 | test("^[0-9a-f]{64}$"))
	and (.ledger.object_count | type == "number" and . >= 0 and floor == .)
	and (.replay | type == "object" and (keys | sort == ["absence_verified_count","already_applied_count","imported_count","replayed_count"]))
	and (all(.replay[]; type == "number" and . >= 0 and floor == .))
	and .replay.replayed_count > 0
	and .ledger.object_count >= .replay.replayed_count
	and .replay.replayed_count == (.replay.imported_count + .replay.already_applied_count)
	and .replay.absence_verified_count == .replay.replayed_count
' "$attestation_file" >/dev/null; then
	fail
fi

canonical=$(jq -Sc 'del(.auth_hmac_sha256)' "$attestation_file")
actual_hmac=$(printf '%s' "$canonical" |
	python3 -c 'import hashlib,hmac,pathlib,sys; key=bytes.fromhex(pathlib.Path(sys.argv[1]).read_text().strip()); sys.stdout.write(hmac.new(key,sys.stdin.buffer.read(),hashlib.sha256).hexdigest())' "$auth_key_file")
[ "$actual_hmac" = "$(jq -r .auth_hmac_sha256 "$attestation_file")" ] || fail

completed_epoch=$(date -u -d "$(jq -r .completed_at "$attestation_file")" +%s) || fail
valid_until_epoch=$(date -u -d "$(jq -r .valid_until "$attestation_file")" +%s) || fail
now_epoch=$(date -u +%s)
age_seconds=$((now_epoch - completed_epoch))
validity_seconds=$((valid_until_epoch - completed_epoch))
if [ "$age_seconds" -lt 0 ] || [ "$age_seconds" -gt "$maximum_age_seconds" ] || [ "$validity_seconds" -le 0 ] || [ "$validity_seconds" -gt 7776000 ] || [ "$now_epoch" -gt "$valid_until_epoch" ]; then
	fail
fi

attestation_sha256=$(sha256sum "$attestation_file" | awk '{print $1}')
event="privacy_restore_promotion_gate_succeeded attestation_sha256=$attestation_sha256 age_seconds=$age_seconds image_digest=$expected_digest"
printf '%s\n' "$event"
logger -t mycfc-privacy-restore -- "$event" 2>/dev/null || true
