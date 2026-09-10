#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin" "$work_dir/fixtures" "$work_dir/attestations" "$work_dir/runtime"

manifest_key=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
attestation_key=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
data_key_hex=3030303030303030303030303030303030303030303030303030303030303030
iv=11111111111111111111111111111111
printf '%s' 'synthetic-pg-dump' >"$work_dir/fixtures/dump"
openssl enc -aes-256-cbc -K "$data_key_hex" -iv "$iv" -nosalt -in "$work_dir/fixtures/dump" -out "$work_dir/fixtures/dump.enc"
dump_sha256=$(sha256sum "$work_dir/fixtures/dump.enc" | awk '{print $1}')

printf '%s\n' '{"legacy":"unsigned"}' >"$work_dir/fixtures/invalid-manifest.json"
jq -n \
	--arg contract 'mycfc/postgres-backup/v2' \
	--arg ciphertext "$(printf 'encrypted-data-key' | base64 | tr -d '\n')" \
	--arg iv "$iv" --arg sha256 "$dump_sha256" --arg database mycfc \
	--arg created_at '2026-09-02T02:15:00Z' \
	--arg dump_key 'daily/2026-09-02T02-15-00Z.dump.enc' \
	--arg dump_version dump-version \
	'{contract:$contract,ciphertext:$ciphertext,iv:$iv,sha256:$sha256,database:$database,created_at:$created_at,dump_key:$dump_key,dump_version:$dump_version}' \
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
				printf '{"Versions":[{"Key":"tombstones/closure/%064d.json","VersionId":"ledger-version","IsLatest":true,"LastModified":"2026-09-02T02:15:01Z","Size":%s}],"DeleteMarkers":[]}\n' 0 "$TEST_LEDGER_SIZE"
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
		cp "$body" "$TEST_UPLOADED_ATTESTATION"
		printf '%s\n' '{"VersionId":"attestation-version"}'
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
			*'SELECT version FROM mycfc_meta.schema_migrations'*) printf '%s\n' reset-baseline-v1 202609100008_restore_replay ;;
			*) printf 'unexpected docker exec: %s\n' "$*" >&2; exit 1 ;;
		esac
		;;
	run)
		case "$*" in
			*-d\ --name*) exit 0 ;;
			*--entrypoint\ /app/privacy-restore-replay*)
				ledger_src=
				output_src=
				previous=
				for argument in "$@"; do
					case "$argument" in
						type=bind,src=*,dst=/input/ledger.json,readonly) ledger_src=${argument#type=bind,src=}; ledger_src=${ledger_src%%,dst=*} ;;
						type=bind,src=*,dst=/output) output_src=${argument#type=bind,src=}; output_src=${output_src%%,dst=*} ;;
					esac
					previous=$argument
				done
				inventory=$(jq -r .inventory_sha256 "$ledger_src")
				objects=$(jq '.objects | length' "$ledger_src")
				schema_versions=$(printf '%s\n' reset-baseline-v1 202609100008_restore_replay)
				schema_digest=$(printf '%s' "$schema_versions" | sha256sum | awk '{print $1}')
				jq -n --arg inventory "$inventory" --arg schema "$schema_digest" --argjson objects "$objects" '{contract:"mycfc/privacy-restore-replay-result/v1",result:"SUCCEEDED",inventory_sha256:$inventory,object_count:$objects,imported_count:$objects,replayed_count:1,already_applied_count:0,non_replayable_v1_count:0,absence_verified_count:1,failed_count:0,schema_migration_digest:$schema}' >"$output_src/replay.json"
				;;
			*) exit 0 ;;
		esac
		;;
	*) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
chmod +x "$work_dir/bin"/*

image='registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
output=$(env PATH="$work_dir/bin:$PATH" \
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
	TEST_UPLOADED_ATTESTATION="$work_dir/uploaded-attestation.json" \
	sh "$deployment_dir/postgres-restore-drill.sh" "$image")

printf '%s\n' "$output" | grep -q '^privacy_restore_drill_started$'
printf '%s\n' "$output" | grep -q '^privacy_restore_backup_selected$'
printf '%s\n' "$output" | grep -Eq '^privacy_restore_ledger_prefetched object_count=1 inventory_sha256=[0-9a-f]{64}$'
printf '%s\n' "$output" | grep -Eq '^privacy_restore_drill_succeeded attestation_sha256=[0-9a-f]{64} backup_age_seconds=[0-9]+ ledger_object_count=1 replayed_count=1 absence_verified_count=1$'
jq -e '.backup.manifest_version == "manifest-version" and .backup.dump_version == "dump-version" and .ledger.object_count == 1 and .result == "SUCCEEDED"' "$work_dir/attestations/latest.json" >/dev/null
cmp "$work_dir/attestations/latest.json" "$work_dir/uploaded-attestation.json"
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

service="$deployment_dir/mycfc-postgres-restore-drill.service"
timer="$deployment_dir/mycfc-postgres-restore-drill.timer"
grep -qx 'ProtectSystem=strict' "$service"
grep -qx 'NoNewPrivileges=true' "$service"
grep -qx 'OnCalendar=\*-01-15 04:15:00 UTC' "$timer"
grep -qx 'Persistent=true' "$timer"
grep -qx 'Unit=mycfc-postgres-restore-drill.service' "$timer"

printf '%s\n' 'postgres privacy restore drill tests passed'
