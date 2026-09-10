#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin" "$work_dir/fixtures" "$work_dir/attestations" "$work_dir/runtime"

manifest_key=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
attestation_key=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
data_key_hex=3030303030303030303030303030303030303030303030303030303030303030
printf '%s' "$data_key_hex" >"$work_dir/fixtures/data-key.hex"
chmod 0600 "$work_dir/fixtures/data-key.hex"
printf '%s' 'synthetic-pg-dump' >"$work_dir/fixtures/dump"
openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 -salt -pass "file:$work_dir/fixtures/data-key.hex" -in "$work_dir/fixtures/dump" -out "$work_dir/fixtures/dump.enc"
dump_sha256=$(sha256sum "$work_dir/fixtures/dump.enc" | awk '{print $1}')

printf '%s\n' '{"legacy":"unsigned"}' >"$work_dir/fixtures/invalid-manifest.json"
jq -n \
	--arg contract 'mycfc/postgres-backup/v3' \
	--arg ciphertext "$(printf 'encrypted-data-key' | base64 | tr -d '\n')" \
	--arg cipher 'AES-256-CBC' --arg kdf 'PBKDF2-HMAC-SHA256' --argjson kdf_iterations 600000 \
	--arg sha256 "$dump_sha256" --arg database mycfc \
	--arg created_at '2026-09-02T02:15:00Z' \
	--arg dump_key 'daily/2026-09-02T02-15-00Z.dump.enc' \
	--arg dump_version dump-version \
	'{contract:$contract,cipher:$cipher,kdf:$kdf,kdf_iterations:$kdf_iterations,ciphertext:$ciphertext,sha256:$sha256,database:$database,created_at:$created_at,dump_key:$dump_key,dump_version:$dump_version}' \
	>"$work_dir/fixtures/manifest-payload.json"
manifest_canonical=$(jq -Sc . "$work_dir/fixtures/manifest-payload.json")
manifest_hmac=$(printf '%s' "$manifest_canonical" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$manifest_key" -binary | od -An -v -tx1 | tr -d ' \n')
jq --arg hmac "$manifest_hmac" '. + {auth_hmac_sha256:$hmac}' "$work_dir/fixtures/manifest-payload.json" >"$work_dir/fixtures/manifest.json"

printf '%s\n' '{"kind":"closure","synthetic":"sealed-v2"}' >"$work_dir/fixtures/ledger-object.json"
ledger_size=$(wc -c <"$work_dir/fixtures/ledger-object.json" | tr -d ' ')

cat >"$work_dir/mycfc.env" <<'EOF'
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=application-key
AWS_SECRET_ACCESS_KEY=application-secret
BACKUP_S3_BUCKET=test-backups
BACKUP_KMS_KEY_ID=arn:aws:kms:eu-west-1:123456789012:key/backup
BACKUP_MANIFEST_AUTH_ENABLED=true
PRIVACY_RESTORE_DRILL_ENABLED=true
PRIVACY_RESTORE_LEDGER_BUCKET=test-ledger
PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN=arn:aws:kms:eu-west-1:123456789012:key/ledger
PRIVACY_ACTIVATION_POLICY_VERSION=privacy-policy-v1
POSTGRES_DB=mycfc
MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
SMTP_HOST=must-not-reach-restore
PROVIDER_API_TOKEN=must-not-reach-restore
EOF
chmod 0600 "$work_dir/mycfc.env"
for file in backup-credentials ledger-credentials replay.key; do
	printf '%s\n' 'synthetic-secret' >"$work_dir/$file"
	chmod 0600 "$work_dir/$file"
done
printf '%s\n' "$manifest_key" >"$work_dir/manifest.key"
printf '%s\n' "$attestation_key" >"$work_dir/attestation.key"
chmod 0600 "$work_dir/manifest.key" "$work_dir/attestation.key"
: >"$work_dir/operations.log"

cat >"$work_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' '0'
EOF
cat >"$work_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' '0:600'
EOF
cat >"$work_dir/bin/flock" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$work_dir/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$work_dir/bin/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$work_dir/bin/openssl" <<'EOF'
#!/bin/sh
printf 'openssl %s\n' "$*" >>"$TEST_OPERATIONS_LOG"
exec "$REAL_OPENSSL" "$@"
EOF
cat >"$work_dir/bin/chown" <<'EOF'
#!/bin/sh
# The production drill hands replay files to the image's fixed nonroot UID.
# This test runs without host root privileges, so ownership transfer is inert.
printf 'chown %s\n' "$*" >>"$TEST_OPERATIONS_LOG"
exit 0
EOF
cat >"$work_dir/bin/aws" <<'EOF'
#!/bin/sh
if [ -n "${AWS_ACCESS_KEY_ID:-}" ] || [ -n "${AWS_SECRET_ACCESS_KEY:-}" ] || [ -n "${AWS_SESSION_TOKEN:-}" ]; then
	printf '%s\n' 'application AWS credentials reached restore AWS command' >&2
	exit 1
fi
printf 'aws %s\n' "$*" >>"$TEST_OPERATIONS_LOG"
bucket=
key=
version=
destination=
previous=
for argument in "$@"; do
	case "$previous" in
		--bucket) bucket=$argument ;;
		--key) key=$argument ;;
		--version-id) version=$argument ;;
	esac
	previous=$argument
	destination=$argument
done
case "$*" in
	*test-ledger*)
		[ "$AWS_SHARED_CREDENTIALS_FILE" = "$TEST_LEDGER_CREDENTIALS" ] && [ "$AWS_PROFILE" = mycfc-privacy-restore ] || exit 1
		;;
	*)
		[ "$AWS_SHARED_CREDENTIALS_FILE" = "$TEST_BACKUP_CREDENTIALS" ] && [ "$AWS_PROFILE" = mycfc-backup ] || exit 1
		;;
esac
checksum() {
	openssl dgst -sha256 -binary "$1" | base64 | tr -d '\n'
}
head_json() {
	file=$1
	kms=$2
	version_id=$3
	extra=${4:-}
	printf '{"VersionId":"%s","ServerSideEncryption":"aws:kms","SSEKMSKeyId":"%s","ContentLength":%s,"ChecksumSHA256":"%s","LastModified":"2026-09-02T02:15:01.123000000+00:00","Metadata":{"locator-key-id":"locator-v2"}%s}\n' \
		"$version_id" "$kms" "$(wc -c <"$file" | tr -d ' ')" "$(checksum "$file")" "$extra"
}
case "$1 $2" in
	's3api list-object-versions')
		case "$bucket:$*" in
			test-backups:*--prefix\ daily/*)
				cat <<JSON
{"Versions":[
 {"Key":"daily/2026-09-01T02-15-00Z.json","VersionId":"unsigned-version","IsLatest":true,"LastModified":"2026-09-01T02:15:00Z","Size":20},
 {"Key":"daily/2026-09-02T02-15-00Z.json","VersionId":"manifest-version","IsLatest":true,"LastModified":"2026-09-02T02:15:00Z","Size":512}
],"DeleteMarkers":[]}
JSON
				;;
			 test-backups:*--prefix\ monthly/*) printf '%s\n' '{"Versions":[],"DeleteMarkers":[]}' ;;
			test-ledger:*)
				if [ "${TEST_EMPTY_LEDGER:-false}" = true ]; then
					printf '%s\n' '{"Versions":[],"DeleteMarkers":[]}'
				else
					printf '{"Versions":[{"Key":"tombstones/closure/%064d.json","VersionId":"ledger-version","IsLatest":true,"LastModified":"2026-09-02T02:15:01Z","Size":%s}],"DeleteMarkers":[]}\n' 0 "$TEST_LEDGER_SIZE"
				fi
				;;
			*) exit 1 ;;
		esac
		;;
	's3api head-object')
		case "$key" in
			daily/2026-09-01T02-15-00Z.json) head_json "$TEST_FIXTURES/invalid-manifest.json" 'arn:aws:kms:eu-west-1:123456789012:key/backup' "$version" ;;
			daily/2026-09-02T02-15-00Z.json) head_json "$TEST_FIXTURES/manifest.json" 'arn:aws:kms:eu-west-1:123456789012:key/backup' "$version" ;;
			daily/2026-09-02T02-15-00Z.dump.enc) head_json "$TEST_FIXTURES/dump.enc" 'arn:aws:kms:eu-west-1:123456789012:key/backup' "$version" ;;
			tombstones/closure/*) head_json "$TEST_FIXTURES/ledger-object.json" 'arn:aws:kms:eu-west-1:123456789012:key/ledger' "$version" ',"ObjectLockMode":"COMPLIANCE","ObjectLockRetainUntilDate":"2028-09-02T02:15:01.123000000+00:00"' ;;
			restore-evidence/*) head_json "$TEST_UPLOADED_EVIDENCE" 'arn:aws:kms:eu-west-1:123456789012:key/backup' "$version" ;;
			restore-attestations/*) head_json "$TEST_UPLOADED_ATTESTATION" 'arn:aws:kms:eu-west-1:123456789012:key/backup' "$version" ;;
			*) exit 1 ;;
		esac
		;;
	's3api get-object')
		case "$key" in
			daily/2026-09-01T02-15-00Z.json) cp "$TEST_FIXTURES/invalid-manifest.json" "$destination" ;;
			daily/2026-09-02T02-15-00Z.json) cp "$TEST_FIXTURES/manifest.json" "$destination" ;;
			daily/2026-09-02T02-15-00Z.dump.enc) cp "$TEST_FIXTURES/dump.enc" "$destination" ;;
			tombstones/closure/*) cp "$TEST_FIXTURES/ledger-object.json" "$destination" ;;
			*) exit 1 ;;
		esac
		printf '%s\n' '{"ok":true}'
		;;
	'kms decrypt')
		printf '{"Plaintext":"%s"}\n' "$(printf '00000000000000000000000000000000' | base64 | tr -d '\n')"
		;;
	's3api put-object')
		body=
		previous=
		for argument in "$@"; do
			[ "$previous" != --body ] || body=$argument
			previous=$argument
		done
		case "$key" in
			restore-evidence/*) cp "$body" "$TEST_UPLOADED_EVIDENCE"; printf '%s\n' '{"VersionId":"evidence-version"}' ;;
			restore-attestations/*) cp "$body" "$TEST_UPLOADED_ATTESTATION"; printf '%s\n' '{"VersionId":"attestation-version"}' ;;
			*) exit 1 ;;
		 esac
		;;
	*) printf 'unexpected aws invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat >"$work_dir/bin/docker" <<'EOF'
#!/bin/sh
printf 'docker %s\n' "$*" >>"$TEST_OPERATIONS_LOG"
case "$1" in
	network) exit 0 ;;
	rm) exit 0 ;;
	cp) exit 0 ;;
	exec)
		case "$*" in
			*pg_isready*) exit 0 ;;
			*pg_restore*) exit 0 ;;
			*'CREATE ROLE'*) exit 0 ;;
			*mycfc_restore_observer*) exit 0 ;;
			*'SELECT version FROM mycfc_meta.schema_migrations'*) printf '%s\n' 202609100008_restore_replay 202609100009_privacy_provider_execution 202609100010_privacy_retention_completion 202609100011_privacy_completion_control 202609100012_privacy_restore_replay_hardening 202609100013_privacy_worker_release_guard 202609100014_privacy_activation_broker 202609100015_privacy_membership_postcondition reset-baseline-v1 ;;
			*) printf 'unexpected docker exec: %s\n' "$*" >&2; exit 1 ;;
		esac
		;;
	run)
		case "$*" in
			*-d\ --name*) exit 0 ;;
			*postgres:16.9-alpine3.21@sha256:*psql*)
				if [ "${TEST_EMPTY_LEDGER:-false}" = true ]; then
					printf '%s\n' '1|0|1|1|5|5|1|1|1|1|mycfc/membership-history-postcondition/v1|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|1|1|1|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
				else
					printf '%s\n' '1|0|0|1|5|5|1|1|1|1|mycfc/membership-history-postcondition/v1|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb|1|1|1|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
				fi
				;;
			*--entrypoint\ /app/privacy-restore-replay*)
				output_src=
				for argument in "$@"; do
					case "$argument" in
						type=bind,src=*,dst=/output) output_src=${argument#type=bind,src=}; output_src=${output_src%%,dst=*} ;;
					 esac
				done
				case "$*" in
					*--bootstrap-synthetic-fixture*)
						jq -n '{contract:"mycfc/privacy-restore-ledger-input/v2",source:"SYNTHETIC_BOOTSTRAP",inventory_sha256:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",objects:[{}]}' >"$output_src/ledger.json"
						exit 0
						;;
				esac
				ledger_src=
				for argument in "$@"; do
					case "$argument" in type=bind,src=*,dst=/input/ledger.json,readonly) ledger_src=${argument#type=bind,src=}; ledger_src=${ledger_src%%,dst=*} ;; esac
				done
				inventory=$(jq -r .inventory_sha256 "$ledger_src")
				objects=$(jq '.objects | length' "$ledger_src")
				source=$(jq -r .source "$ledger_src")
				if [ "$source" = SYNTHETIC_BOOTSTRAP ]; then synthetic=1; else synthetic=0; fi
				schema_versions=$(printf '%s\n' 202609100008_restore_replay 202609100009_privacy_provider_execution 202609100010_privacy_retention_completion 202609100011_privacy_completion_control 202609100012_privacy_restore_replay_hardening 202609100013_privacy_worker_release_guard 202609100014_privacy_activation_broker 202609100015_privacy_membership_postcondition reset-baseline-v1)
				schema_digest=$(printf '%s' "$schema_versions" | sha256sum | awk '{print $1}')
				jq -n --arg source "$source" --arg inventory "$inventory" --arg schema "$schema_digest" --argjson objects "$objects" --argjson synthetic "$synthetic" '{contract:"mycfc/privacy-restore-replay-result/v2",result:"SUCCEEDED",input_source:$source,policy_version:"privacy-policy-v1",executor_version:"privacy-erasure-executor/v2",plan_schema_version:"privacy-erasure-plan/v2",image_digest:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",schema_migration_digest:$schema,inventory_sha256:$inventory,object_count:$objects,imported_count:$objects,replayed_count:1,already_applied_count:0,non_replayable_v1_count:0,absence_verified_count:1,synthetic_replayed_count:$synthetic,closure_v4_count:1,intent_only_count:0,legacy_closure_v2_count:0,erasure_effective_at_verified_count:1,membership_postcondition_contract:"mycfc/membership-history-postcondition/v1",membership_postcondition_sha256:("b"*64),membership_postcondition_verified_count:1,membership_count:1,variation_count:1,failed_count:0}' >"$output_src/replay.json"
				;;
			*) exit 0 ;;
		esac
		;;
	*) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
chmod +x "$work_dir/bin"/*
REAL_OPENSSL=$(command -v openssl)
export REAL_OPENSSL

image='registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
if ! output=$(env PATH="$work_dir/bin:$PATH" \
	MYCFC_ENV_FILE="$work_dir/mycfc.env" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/backup-credentials" \
	MYCFC_RESTORE_LEDGER_CREDENTIALS_FILE="$work_dir/ledger-credentials" \
	MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE="$work_dir/attestation.key" \
	MYCFC_RESTORE_REPLAY_PRIVATE_KEY_FILE="$work_dir/replay.key" \
	MYCFC_RESTORE_ATTESTATION_DIR="$work_dir/attestations" \
	MYCFC_RUNTIME_DIR="$work_dir/runtime" \
	TEST_FIXTURES="$work_dir/fixtures" \
	TEST_BACKUP_CREDENTIALS="$work_dir/backup-credentials" \
	TEST_LEDGER_CREDENTIALS="$work_dir/ledger-credentials" \
	TEST_OPERATIONS_LOG="$work_dir/operations.log" \
	TEST_LEDGER_SIZE="$ledger_size" \
	TEST_UPLOADED_EVIDENCE="$work_dir/uploaded-evidence.json" \
	TEST_UPLOADED_ATTESTATION="$work_dir/uploaded-attestation.json" \
	sh -x "$deployment_dir/postgres-restore-drill.sh" "$image" 2>"$work_dir/trace.log"); then
	cat "$work_dir/trace.log" >&2
	exit 1
fi

printf '%s\n' "$output" | grep -q '^privacy_restore_drill_started$'
printf '%s\n' "$output" | grep -q '^privacy_restore_backup_selected$'
printf '%s\n' "$output" | grep -Eq '^privacy_restore_ledger_prefetched object_count=1 inventory_sha256=[0-9a-f]{64}$'
printf '%s\n' "$output" | grep -Eq '^privacy_restore_drill_succeeded attestation_sha256=[0-9a-f]{64} backup_age_seconds=[0-9]+ ledger_object_count=1 replayed_count=1 absence_verified_count=1$'
jq -e '.contract == "mycfc/privacy-restore-drill-attestation/v2" and .contracts.closure == "restore-tombstone-closure/v4" and .backup.manifest.ref == "s3://test-backups/daily/2026-09-02T02-15-00Z.json?versionId=manifest-version" and .backup.dump.ref == "s3://test-backups/daily/2026-09-02T02-15-00Z.dump.enc?versionId=dump-version" and .ledger.input_source == "LIVE_LEDGER" and .ledger.object_count == 1 and .candidate.closure_v4_count == 1 and .observer.verified_run_count == 1 and .evidence.ref == "s3://test-backups/" + (.evidence.ref | ltrimstr("s3://test-backups/")) and .result == "SUCCEEDED"' "$work_dir/attestations/latest.json" >/dev/null
cmp "$work_dir/attestations/latest.json" "$work_dir/uploaded-attestation.json"
test -s "$work_dir/uploaded-evidence.json"
grep -q 'aws s3api put-object .*--key restore-evidence/.*--if-none-match \*' "$work_dir/operations.log"
grep -q 'aws s3api put-object .*--key restore-attestations/.*--if-none-match \*' "$work_dir/operations.log"

ledger_get_line=$(grep -n 'aws s3api get-object.*tombstones/closure' "$work_dir/operations.log" | cut -d: -f1)
network_line=$(grep -n 'docker network create --internal' "$work_dir/operations.log" | cut -d: -f1)
test "$ledger_get_line" -lt "$network_line"
grep -q 'docker network rm mycfc-restore-' "$work_dir/operations.log"
grep -q 'docker rm -f mycfc-restore-db-' "$work_dir/operations.log"
grep -q 'chown 65532:65532 /var/tmp/mycfc-restore\..*/output' "$work_dir/operations.log"
grep -q 'chown 65532:65532 /var/tmp/mycfc-restore\..*/tombstone-replay.key' "$work_dir/operations.log"
if grep -Eq 'application-secret|must-not-reach-restore|postgres://|POSTGRES_PASSWORD=' "$work_dir/operations.log" "$work_dir/attestations/latest.json"; then
	printf '%s\n' 'A runtime/provider/database secret reached restore evidence or a container command.' >&2
	exit 1
fi
if grep -Fq -- '-K ' "$work_dir/operations.log" || grep -Fq "$data_key_hex" "$work_dir/operations.log" "$work_dir/trace.log"; then
	printf '%s\n' 'KMS plaintext data key reached restore argv or shell tracing' >&2
	exit 1
fi
grep -Eq 'openssl enc -d -aes-256-cbc .* -pass file:/var/tmp/mycfc-restore\.' "$work_dir/operations.log"

# An empty live ledger must take the explicit isolated synthetic bootstrap path,
# then run the ordinary replay and independent observer against that fixture.
: >"$work_dir/synthetic-operations.log"
if ! synthetic_output=$(env PATH="$work_dir/bin:$PATH" \
	MYCFC_ENV_FILE="$work_dir/mycfc.env" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/backup-credentials" \
	MYCFC_RESTORE_LEDGER_CREDENTIALS_FILE="$work_dir/ledger-credentials" \
	MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE="$work_dir/attestation.key" \
	MYCFC_RESTORE_REPLAY_PRIVATE_KEY_FILE="$work_dir/replay.key" \
	MYCFC_RESTORE_ATTESTATION_DIR="$work_dir/attestations" \
	MYCFC_RUNTIME_DIR="$work_dir/runtime" \
	TEST_FIXTURES="$work_dir/fixtures" \
	TEST_BACKUP_CREDENTIALS="$work_dir/backup-credentials" \
	TEST_LEDGER_CREDENTIALS="$work_dir/ledger-credentials" \
	TEST_OPERATIONS_LOG="$work_dir/synthetic-operations.log" \
	TEST_LEDGER_SIZE="$ledger_size" \
	TEST_UPLOADED_EVIDENCE="$work_dir/uploaded-evidence.json" \
	TEST_UPLOADED_ATTESTATION="$work_dir/uploaded-attestation.json" \
	TEST_EMPTY_LEDGER=true \
	sh "$deployment_dir/postgres-restore-drill.sh" "$image" 2>"$work_dir/synthetic-trace.log"); then
	cat "$work_dir/synthetic-trace.log" >&2
	exit 1
fi
printf '%s\n' "$synthetic_output" | grep -Eq '^privacy_restore_ledger_prefetched object_count=0 inventory_sha256=[0-9a-f]{64}$'
grep -q -- '--bootstrap-synthetic-fixture .*--synthetic-ledger-output /output/ledger.json' "$work_dir/synthetic-operations.log"
grep -q -- '--ledger-input /input/ledger.json .*--attestation-output /output/replay.json' "$work_dir/synthetic-operations.log"
if grep -Eq 'postgres://|PRIVACY_RESTORE_OBSERVER_DB_PASSWORD=' "$work_dir/synthetic-operations.log"; then
	printf '%s\n' 'observer credentials reached a container command argument' >&2
	exit 1
fi

service="$deployment_dir/mycfc-postgres-restore-drill.service"
timer="$deployment_dir/mycfc-postgres-restore-drill.timer"
grep -qx 'ProtectSystem=strict' "$service"
grep -qx 'NoNewPrivileges=true' "$service"
grep -qx 'OnCalendar=\*-01-15 04:15:00 UTC' "$timer"
grep -qx 'Persistent=true' "$timer"
grep -qx 'Unit=mycfc-postgres-restore-drill.service' "$timer"

printf '%s\n' 'postgres privacy restore drill tests passed'
