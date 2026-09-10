#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
backup_credentials_file=${MYCFC_BACKUP_CREDENTIALS_FILE:-/etc/mycfc/backup-aws/credentials}
ledger_credentials_file=${MYCFC_RESTORE_LEDGER_CREDENTIALS_FILE:-/etc/mycfc/privacy-restore/credentials}
manifest_auth_key_file=${MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE:-/etc/mycfc/backup-auth/manifest.key}
attestation_auth_key_file=${MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE:-/etc/mycfc/privacy-restore/attestation.key}
replay_private_key_file=${MYCFC_RESTORE_REPLAY_PRIVATE_KEY_FILE:-/etc/mycfc/privacy-restore/tombstone-replay.key}
attestation_dir=${MYCFC_RESTORE_ATTESTATION_DIR:-/etc/mycfc/privacy-restore/attestations}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
lock_file="$runtime_dir/mycfc-postgres-restore-drill.lock"
work_dir=
network=
database_container=

log_event() {
	printf '%s\n' "$1"
	logger -t mycfc-restore-drill -- "$1" 2>/dev/null || true
}

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	[ -z "$database_container" ] || docker rm -f "$database_container" >/dev/null 2>&1 || true
	[ -z "$network" ] || docker network rm "$network" >/dev/null 2>&1 || true
	[ -z "$work_dir" ] || rm -rf "$work_dir"
	if [ "$status" -ne 0 ]; then
		log_event 'privacy_restore_drill_failed'
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if [ "$(id -u)" -ne 0 ]; then
	printf '%s\n' 'The privacy restore drill must run as root.' >&2
	exit 1
fi
if [ ! -f "$env_file" ] || [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	printf '%s\n' 'The protected environment file is missing or insecure.' >&2
	exit 1
fi

set -a
. "$env_file"
set +a

: "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_KMS_KEY_ID:?}"
: "${PRIVACY_RESTORE_LEDGER_BUCKET:?}"
: "${PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN:?}"
: "${POSTGRES_DB:?}"

case "$BACKUP_KMS_KEY_ID" in
	arn:*:kms:*:*:key/*) ;;
	*) printf '%s\n' 'BACKUP_KMS_KEY_ID must be the exact KMS key ARN for restore evidence.' >&2; exit 1 ;;
esac
case "$PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN" in
	arn:*:kms:*:*:key/*) ;;
	*) printf '%s\n' 'PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN must be an exact KMS key ARN.' >&2; exit 1 ;;
esac

PRIVACY_RESTORE_DRILL_ENABLED=${PRIVACY_RESTORE_DRILL_ENABLED:-false}
BACKUP_MANIFEST_AUTH_ENABLED=${BACKUP_MANIFEST_AUTH_ENABLED:-false}
if [ "$PRIVACY_RESTORE_DRILL_ENABLED" != true ]; then
	printf '%s\n' 'privacy_restore_drill_disabled' >&2
	exit 1
fi
if [ "$BACKUP_MANIFEST_AUTH_ENABLED" != true ]; then
	printf '%s\n' 'privacy_restore_drill_requires_authenticated_backups' >&2
	exit 1
fi

candidate_image=${1:-${MYCFC_RESTORE_DRILL_IMAGE:-${MYCFC_IMAGE:-}}}
if ! printf '%s' "$candidate_image" | grep -Eq '@sha256:[0-9a-f]{64}$'; then
	printf '%s\n' 'The restore drill requires an immutable image digest.' >&2
	exit 1
fi

check_secret_file() {
	file=$1
	if [ ! -f "$file" ] || [ "$(stat -c '%u:%a' "$file")" != '0:600' ]; then
		printf '%s\n' 'A protected restore input is missing or insecure.' >&2
		exit 1
	fi
}
for file in "$backup_credentials_file" "$ledger_credentials_file" "$manifest_auth_key_file" "$attestation_auth_key_file" "$replay_private_key_file"; do
	check_secret_file "$file"
done

for key_file in "$manifest_auth_key_file" "$attestation_auth_key_file"; do
	if ! tr -d '\n' <"$key_file" | grep -Eq '^[0-9A-Fa-f]{64}$'; then
		printf '%s\n' 'A restore authentication key is invalid.' >&2
		exit 1
	fi
done

exec 9>"$lock_file"
if ! flock -n 9; then
	printf '%s\n' 'A privacy restore drill is already running.' >&2
	exit 1
fi

work_dir=$(mktemp -d /var/tmp/mycfc-restore.XXXXXX)
suffix=$(basename "$work_dir" | tr -cd 'A-Za-z0-9')
network="mycfc-restore-$suffix"
database_container="mycfc-restore-db-$suffix"
output_dir="$work_dir/output"
mkdir -m 0700 "$output_dir"
# The immutable application image runs as the distroless nonroot identity.
# Give only that identity access to the replay result directory.
chown 65532:65532 "$output_dir"

unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_REGION="${AWS_REGION:-eu-west-1}"
export AWS_PAGER=""

backup_aws() {
	AWS_SHARED_CREDENTIALS_FILE="$backup_credentials_file" AWS_PROFILE="${MYCFC_BACKUP_AWS_PROFILE:-mycfc-backup}" aws "$@"
}

ledger_aws() {
	AWS_SHARED_CREDENTIALS_FILE="$ledger_credentials_file" AWS_PROFILE="${MYCFC_RESTORE_LEDGER_AWS_PROFILE:-mycfc-privacy-restore}" aws "$@"
}

hmac_sha256() {
	input=$1
	printf '%s' "$input" |
		python3 -c 'import hashlib,hmac,pathlib,sys; key=bytes.fromhex(pathlib.Path(sys.argv[1]).read_text().strip()); sys.stdout.write(hmac.new(key,sys.stdin.buffer.read(),hashlib.sha256).hexdigest())' "$2"
}

normalize_rfc3339() {
	normalized=$(date -u -d "$1" +%Y-%m-%dT%H:%M:%S.%NZ)
	printf '%s\n' "$normalized" | sed -E 's/\.([0-9]*[1-9])0+Z$/.\1Z/; s/\.0+Z$/Z/'
}

canonical_versions() {
	jq -Sc '[
		((.Versions // [])[] | {kind:"VERSION",key:.Key,version:.VersionId,is_latest:.IsLatest,last_modified:.LastModified,size:.Size}),
		((.DeleteMarkers // [])[] | {kind:"DELETE_MARKER",key:.Key,version:.VersionId,is_latest:.IsLatest,last_modified:.LastModified,size:0})
	] | sort_by(.key,.version,.kind)'
}

log_event 'privacy_restore_drill_started'

# Require a stable complete view before choosing the oldest authenticated
# recovery point. Unsigned legacy manifests are deliberately not eligible.
for pass in first second; do
	for prefix in daily monthly; do
		backup_aws s3api list-object-versions --bucket "$BACKUP_S3_BUCKET" --prefix "$prefix/" --output json >"$work_dir/backups-$pass-$prefix.json"
		canonical_versions <"$work_dir/backups-$pass-$prefix.json" >"$work_dir/backups-$pass-$prefix.canonical"
	done
	cat "$work_dir/backups-$pass-daily.canonical" "$work_dir/backups-$pass-monthly.canonical" >"$work_dir/backups-$pass.inventory"
done
if ! cmp -s "$work_dir/backups-first.inventory" "$work_dir/backups-second.inventory"; then
	printf '%s\n' 'Backup inventory changed during selection.' >&2
	exit 1
fi

jq -r '
	(.Versions // [])[]
	| select(.IsLatest == true and (.Key | test("^(daily|monthly)/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}Z\\.json$")))
	| [.LastModified,.Key,.VersionId] | @tsv
' "$work_dir/backups-first-daily.json" "$work_dir/backups-first-monthly.json" | sort >"$work_dir/manifest-candidates.tsv"

selected_manifest=
selected_manifest_key=
selected_manifest_version=
selected_dump_key=
selected_dump_version=

validate_backup_candidate() {
	manifest_key=$1
	manifest_version=$2
	manifest_file="$work_dir/candidate-manifest.json"
	manifest_head="$work_dir/candidate-manifest-head.json"
	dump_file="$work_dir/candidate-dump.enc"
	dump_head="$work_dir/candidate-dump-head.json"

	backup_aws s3api head-object --bucket "$BACKUP_S3_BUCKET" --key "$manifest_key" --version-id "$manifest_version" --checksum-mode ENABLED --output json >"$manifest_head" || return 1
	jq -e --arg version "$manifest_version" --arg kms "$BACKUP_KMS_KEY_ID" '
		.VersionId == $version and .ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
		and (.ContentLength | type == "number" and . > 0 and . <= 65536)
		and (.ChecksumSHA256 | type == "string" and length > 0)
	' "$manifest_head" >/dev/null || return 1
	backup_aws s3api get-object --bucket "$BACKUP_S3_BUCKET" --key "$manifest_key" --version-id "$manifest_version" --checksum-mode ENABLED "$manifest_file" >/dev/null || return 1
	manifest_checksum=$(openssl dgst -sha256 -binary "$manifest_file" | base64 | tr -d '\n')
	[ "$manifest_checksum" = "$(jq -r .ChecksumSHA256 "$manifest_head")" ] || return 1

	jq -e --arg database "$POSTGRES_DB" --arg manifest_key "$manifest_key" '
		(type == "object") and (keys | sort == ["auth_hmac_sha256","ciphertext","contract","created_at","database","dump_key","dump_version","iv","sha256"])
		and .contract == "mycfc/postgres-backup/v2"
		and .database == $database
		and (.created_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"))
		and (.ciphertext | type == "string" and length > 0)
		and (.iv | type == "string" and test("^[0-9a-f]{32}$"))
		and (.sha256 | type == "string" and test("^[0-9a-f]{64}$"))
		and (.auth_hmac_sha256 | type == "string" and test("^[0-9a-f]{64}$"))
		and (.dump_version | type == "string" and length > 0 and length <= 1024)
		and (.dump_key == ($manifest_key | sub("\\.json$"; ".dump.enc")))
	' "$manifest_file" >/dev/null || return 1
	canonical=$(jq -Sc 'del(.auth_hmac_sha256)' "$manifest_file")
	[ "$(hmac_sha256 "$canonical" "$manifest_auth_key_file")" = "$(jq -r .auth_hmac_sha256 "$manifest_file")" ] || return 1
	candidate_created_epoch=$(date -u -d "$(jq -r .created_at "$manifest_file")" +%s) || return 1
	candidate_age_seconds=$(($(date -u +%s) - candidate_created_epoch))
	[ "$candidate_age_seconds" -ge 0 ] || return 1
	case "$manifest_key" in
		daily/*) [ "$candidate_age_seconds" -le 2678400 ] || return 1 ;;
		monthly/*) [ "$candidate_age_seconds" -le 31622400 ] || return 1 ;;
		*) return 1 ;;
	esac

	dump_key=$(jq -r .dump_key "$manifest_file")
	dump_version=$(jq -r .dump_version "$manifest_file")
	backup_aws s3api head-object --bucket "$BACKUP_S3_BUCKET" --key "$dump_key" --version-id "$dump_version" --checksum-mode ENABLED --output json >"$dump_head" || return 1
	jq -e --arg version "$dump_version" --arg kms "$BACKUP_KMS_KEY_ID" '
		.VersionId == $version and .ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
		and (.ContentLength | type == "number" and . > 0)
		and (.ChecksumSHA256 | type == "string" and length > 0)
	' "$dump_head" >/dev/null || return 1
	backup_aws s3api get-object --bucket "$BACKUP_S3_BUCKET" --key "$dump_key" --version-id "$dump_version" --checksum-mode ENABLED "$dump_file" >/dev/null || return 1
	[ "$(sha256sum "$dump_file" | awk '{print $1}')" = "$(jq -r .sha256 "$manifest_file")" ] || return 1
	[ "$(openssl dgst -sha256 -binary "$dump_file" | base64 | tr -d '\n')" = "$(jq -r .ChecksumSHA256 "$dump_head")" ] || return 1
	return 0
}

while IFS="$(printf '\t')" read -r candidate_modified candidate_key candidate_version; do
	[ -n "$candidate_modified" ] || continue
	if validate_backup_candidate "$candidate_key" "$candidate_version"; then
		selected_manifest="$work_dir/manifest.json"
		cp "$work_dir/candidate-manifest.json" "$selected_manifest"
		cp "$work_dir/candidate-dump.enc" "$work_dir/dump.enc"
		selected_manifest_key=$candidate_key
		selected_manifest_version=$candidate_version
		selected_dump_key=$(jq -r .dump_key "$selected_manifest")
		selected_dump_version=$(jq -r .dump_version "$selected_manifest")
		break
	fi
done <"$work_dir/manifest-candidates.tsv"
if [ -z "$selected_manifest" ]; then
	printf '%s\n' 'No authenticated retained backup passed checksum verification.' >&2
	exit 1
fi
log_event 'privacy_restore_backup_selected'

# Fetch and verify the complete immutable ledger before creating the isolated
# network. No AWS identity or network route is available to replay containers.
for pass in first second; do
	ledger_aws s3api list-object-versions --bucket "$PRIVACY_RESTORE_LEDGER_BUCKET" --prefix tombstones/ --output json >"$work_dir/ledger-$pass.json"
	canonical_versions <"$work_dir/ledger-$pass.json" >"$work_dir/ledger-$pass.canonical"
done
if ! cmp -s "$work_dir/ledger-first.canonical" "$work_dir/ledger-second.canonical"; then
	printf '%s\n' 'Privacy ledger changed during prefetch.' >&2
	exit 1
fi
if jq -e '((.DeleteMarkers // []) | length) > 0 or any((.Versions // [])[]; .IsLatest != true)' "$work_dir/ledger-first.json" >/dev/null; then
	printf '%s\n' 'Privacy ledger contains unexpected version history.' >&2
	exit 1
fi
if jq -e 'any((.Versions // [])[]; (.Key | test("^tombstones/(intent|closure)/[0-9a-f]{64}\\.json$") | not))' "$work_dir/ledger-first.json" >/dev/null; then
	printf '%s\n' 'Privacy ledger contains an invalid object name.' >&2
	exit 1
fi

jq -r '
	(.Versions // [])[]
	| select(.IsLatest == true and (.Key | test("^tombstones/(intent|closure)/[0-9a-f]{64}\\.json$")))
	| [.Key,.VersionId,.Size] | @tsv
' "$work_dir/ledger-first.json" | sort >"$work_dir/ledger-objects.tsv"
ledger_object_count=$(wc -l <"$work_dir/ledger-objects.tsv" | tr -d ' ')
ledger_total_bytes=$(awk -F '\t' '{total += $3} END {print total + 0}' "$work_dir/ledger-objects.tsv")
ledger_max_objects=${PRIVACY_RESTORE_LEDGER_MAX_OBJECTS:-1024}
ledger_max_bytes=${PRIVACY_RESTORE_LEDGER_MAX_BYTES:-22020096}
case "$ledger_max_objects:$ledger_max_bytes" in
	*[!0-9:]* | :* | *:) printf '%s\n' 'Privacy ledger replay bounds must be positive integers.' >&2; exit 1 ;;
esac
if [ "$ledger_max_objects" -lt 1 ] || [ "$ledger_max_objects" -gt 1024 ] ||
	[ "$ledger_max_bytes" -lt 1 ] || [ "$ledger_max_bytes" -gt 22020096 ]; then
	printf '%s\n' 'Privacy ledger replay bounds exceed the offline replay contract.' >&2
	exit 1
fi
if [ "$ledger_object_count" -gt "$ledger_max_objects" ] || [ "$ledger_total_bytes" -gt "$ledger_max_bytes" ]; then
	printf '%s\n' 'Privacy ledger exceeds the bounded offline replay input.' >&2
	exit 1
fi

printf '%s\n' '{"contract":"mycfc/privacy-restore-ledger-input/v1","objects":[]}' >"$work_dir/ledger-building.json"
ledger_index=0
while IFS="$(printf '\t')" read -r ledger_key ledger_version listed_size; do
	[ -n "$ledger_key" ] || continue
	ledger_index=$((ledger_index + 1))
	object_file="$work_dir/ledger-object-$ledger_index.json"
	head_file="$work_dir/ledger-head-$ledger_index.json"
	ledger_aws s3api head-object --bucket "$PRIVACY_RESTORE_LEDGER_BUCKET" --key "$ledger_key" --version-id "$ledger_version" --checksum-mode ENABLED --output json >"$head_file"
	jq -e --arg version "$ledger_version" --arg kms "$PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN" --argjson listed_size "$listed_size" '
		.VersionId == $version and .ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
		and .ContentLength == $listed_size and .ContentLength > 0 and .ContentLength <= 1048576
		and (.ChecksumSHA256 | type == "string" and length > 0)
		and (.LastModified | type == "string" and length > 0)
		and (.Metadata["locator-key-id"] | type == "string" and length > 0)
	' "$head_file" >/dev/null
	case "$ledger_key" in
		tombstones/closure/*)
			jq -e '.ObjectLockMode == "COMPLIANCE" and (.ObjectLockRetainUntilDate | type == "string" and length > 0)' "$head_file" >/dev/null
			retain_until=$(normalize_rfc3339 "$(jq -r .ObjectLockRetainUntilDate "$head_file")")
			;;
		*) retain_until=null ;;
	esac
	ledger_aws s3api get-object --bucket "$PRIVACY_RESTORE_LEDGER_BUCKET" --key "$ledger_key" --version-id "$ledger_version" --checksum-mode ENABLED "$object_file" >/dev/null
	ciphertext_sha256=$(sha256sum "$object_file" | awk '{print $1}')
	expected_checksum=$(jq -r .ChecksumSHA256 "$head_file")
	[ "$(openssl dgst -sha256 -binary "$object_file" | base64 | tr -d '\n')" = "$expected_checksum" ]
	key_sha256=$(printf '%s' "$ledger_key" | sha256sum | awk '{print $1}')
	written_at=$(normalize_rfc3339 "$(jq -r .LastModified "$head_file")")
	verified_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	payload_file="$work_dir/ledger-payload-$ledger_index.b64"
	base64 <"$object_file" | tr -d '\n' >"$payload_file"
	jq \
		--arg key_sha256 "$key_sha256" \
		--arg object_version "$ledger_version" \
		--arg ciphertext_sha256 "$ciphertext_sha256" \
		--argjson size_bytes "$listed_size" \
		--arg written_at "$written_at" \
		--arg verified_at "$verified_at" \
		--arg retain_until "$retain_until" \
		--rawfile payload "$payload_file" \
		'.objects += [{key_sha256:$key_sha256,object_version:$object_version,ciphertext_sha256:$ciphertext_sha256,size_bytes:$size_bytes,written_at:$written_at,verified_at:$verified_at,retain_until:(if $retain_until == "null" then null else $retain_until end),payload:$payload}]' \
		"$work_dir/ledger-building.json" >"$work_dir/ledger-next.json"
	mv "$work_dir/ledger-next.json" "$work_dir/ledger-building.json"
done <"$work_dir/ledger-objects.tsv"

jq '.objects |= sort_by(.key_sha256,.object_version)' "$work_dir/ledger-building.json" >"$work_dir/ledger-sorted.json"
ledger_metadata=$(jq -Sc '[.objects[] | del(.payload)]' "$work_dir/ledger-sorted.json")
ledger_inventory_sha256=$(printf '%s' "$ledger_metadata" | sha256sum | awk '{print $1}')
jq --arg inventory_sha256 "$ledger_inventory_sha256" '. + {inventory_sha256:$inventory_sha256}' "$work_dir/ledger-sorted.json" >"$work_dir/ledger.json"
if [ "$(wc -c <"$work_dir/ledger.json")" -gt 33554432 ]; then
	printf '%s\n' 'Privacy ledger exceeds the offline replay input contract.' >&2
	exit 1
fi
log_event "privacy_restore_ledger_prefetched object_count=$ledger_object_count inventory_sha256=$ledger_inventory_sha256"

# Only the temporary PostgreSQL and candidate replay containers join this
# internal Docker network. They receive no SMTP/provider/runtime AWS settings.
docker network create --internal "$network" >/dev/null
restore_password=$(openssl rand -hex 32)
POSTGRES_PASSWORD="$restore_password" docker run -d --name "$database_container" --network "$network" \
	-e POSTGRES_PASSWORD -e POSTGRES_DB=mycfc_restore postgres:16.9-alpine3.21 >/dev/null
ready=false
for _ in $(seq 1 60); do
	if docker exec "$database_container" pg_isready -U postgres -d mycfc_restore >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 1
done
if [ "$ready" != true ]; then
	printf '%s\n' 'Isolated PostgreSQL did not become ready.' >&2
	exit 1
fi

jq -r .ciphertext "$selected_manifest" | base64 -d >"$work_dir/key.enc"
backup_aws kms decrypt --ciphertext-blob "fileb://$work_dir/key.enc" --output json | jq -r .Plaintext | base64 -d | od -An -v -tx1 | tr -d ' \n' >"$work_dir/key.hex"
openssl enc -d -aes-256-cbc -K "$(cat "$work_dir/key.hex")" -iv "$(jq -r .iv "$selected_manifest")" -nosalt -in "$work_dir/dump.enc" -out "$work_dir/dump"
docker cp "$work_dir/dump" "$database_container":/tmp/dump >/dev/null
docker exec "$database_container" pg_restore --exit-on-error --no-owner --no-acl -U postgres -d mycfc_restore /tmp/dump
docker exec "$database_container" psql -v ON_ERROR_STOP=1 -U postgres -d mycfc_restore -c 'CREATE ROLE mycfc_restore_app NOLOGIN' >/dev/null

database_url="postgres://postgres:$restore_password@$database_container:5432/mycfc_restore?sslmode=disable"
DATABASE_URL="$database_url" docker run --rm --network "$network" \
	-e DATABASE_URL \
	-e APP_DB_USER=mycfc_restore_app \
	-e MIGRATION_DB_USER=postgres \
	"$candidate_image" migrate

cp "$replay_private_key_file" "$work_dir/tombstone-replay.key"
chmod 0444 "$work_dir/ledger.json"
chown 65532:65532 "$work_dir/tombstone-replay.key"
chmod 0400 "$work_dir/tombstone-replay.key"
DATABASE_URL="$database_url" docker run --rm --network "$network" --entrypoint /app/privacy-restore-replay \
	-e DATABASE_URL \
	--mount "type=bind,src=$work_dir/ledger.json,dst=/input/ledger.json,readonly" \
	--mount "type=bind,src=$work_dir/tombstone-replay.key,dst=/run/secrets/tombstone-replay-key,readonly" \
	--mount "type=bind,src=$output_dir,dst=/output" \
	"$candidate_image" \
	--isolated-restore \
	--ledger-input /input/ledger.json \
	--private-key-file /run/secrets/tombstone-replay-key \
	--attestation-output /output/replay.json

replay_result="$output_dir/replay.json"
if [ ! -f "$replay_result" ]; then
	printf '%s\n' 'Offline replay did not produce a result.' >&2
	exit 1
fi
jq -e --arg inventory "$ledger_inventory_sha256" --argjson objects "$ledger_object_count" '
		(keys | sort == ["absence_verified_count","already_applied_count","contract","failed_count","imported_count","inventory_sha256","non_replayable_v1_count","object_count","replayed_count","result","schema_migration_digest"])
		and .contract == "mycfc/privacy-restore-replay-result/v1"
		and .result == "SUCCEEDED"
		and .inventory_sha256 == $inventory
		and .object_count == $objects
		and .non_replayable_v1_count == 0
		and .failed_count == 0
		and (.schema_migration_digest | type == "string" and test("^[0-9a-f]{64}$"))
		and (.imported_count | type == "number" and . >= 0 and floor == .)
		and (.replayed_count | type == "number" and . >= 0 and floor == .)
		and (.already_applied_count | type == "number" and . >= 0 and floor == .)
		and (.absence_verified_count | type == "number" and . >= 0 and floor == .)
		and .replayed_count > 0
		and .object_count >= .replayed_count
		and .replayed_count == (.imported_count + .already_applied_count)
		and .absence_verified_count == .replayed_count
	' "$replay_result" >/dev/null

schema_versions=$(docker exec "$database_container" psql -v ON_ERROR_STOP=1 -U postgres -d mycfc_restore -Atc "SELECT version FROM mycfc_meta.schema_migrations ORDER BY version")
schema_migration_digest=$(printf '%s' "$schema_versions" | sha256sum | awk '{print $1}')
if [ "$schema_migration_digest" != "$(jq -r .schema_migration_digest "$replay_result")" ]; then
	printf '%s\n' 'Replay schema digest does not match the restored database.' >&2
	exit 1
fi

completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
completed_epoch=$(date -u -d "$completed_at" +%s)
valid_until=$(date -u -d "@$((completed_epoch + 7776000))" +%Y-%m-%dT%H:%M:%SZ)
manifest_sha256=$(sha256sum "$selected_manifest" | awk '{print $1}')
manifest_key_sha256=$(printf '%s' "$selected_manifest_key" | sha256sum | awk '{print $1}')
dump_key_sha256=$(printf '%s' "$selected_dump_key" | sha256sum | awk '{print $1}')
backup_created_at=$(jq -r .created_at "$selected_manifest")
image_digest=${candidate_image##*@}

jq -n \
	--arg contract 'mycfc/privacy-restore-drill-attestation/v1' \
	--arg result SUCCEEDED \
	--arg completed_at "$completed_at" \
	--arg valid_until "$valid_until" \
	--arg image_digest "$image_digest" \
	--arg backup_created_at "$backup_created_at" \
	--arg manifest_key_sha256 "$manifest_key_sha256" \
	--arg manifest_version "$selected_manifest_version" \
	--arg manifest_sha256 "$manifest_sha256" \
	--arg dump_key_sha256 "$dump_key_sha256" \
	--arg dump_version "$selected_dump_version" \
	--arg dump_sha256 "$(jq -r .sha256 "$selected_manifest")" \
	--arg schema_migration_digest "$schema_migration_digest" \
	--arg ledger_inventory_sha256 "$ledger_inventory_sha256" \
	--argjson ledger_object_count "$ledger_object_count" \
	--argjson imported_count "$(jq -r .imported_count "$replay_result")" \
	--argjson replayed_count "$(jq -r .replayed_count "$replay_result")" \
	--argjson already_applied_count "$(jq -r .already_applied_count "$replay_result")" \
	--argjson absence_verified_count "$(jq -r .absence_verified_count "$replay_result")" \
	'{contract:$contract,result:$result,completed_at:$completed_at,valid_until:$valid_until,image_digest:$image_digest,backup:{created_at:$backup_created_at,manifest_key_sha256:$manifest_key_sha256,manifest_version:$manifest_version,manifest_sha256:$manifest_sha256,dump_key_sha256:$dump_key_sha256,dump_version:$dump_version,dump_sha256:$dump_sha256},schema_migration_digest:$schema_migration_digest,ledger:{inventory_sha256:$ledger_inventory_sha256,object_count:$ledger_object_count},replay:{imported_count:$imported_count,replayed_count:$replayed_count,already_applied_count:$already_applied_count,absence_verified_count:$absence_verified_count}}' \
	>"$work_dir/attestation-payload.json"
attestation_canonical=$(jq -Sc . "$work_dir/attestation-payload.json")
attestation_hmac=$(hmac_sha256 "$attestation_canonical" "$attestation_auth_key_file")
jq --arg auth_hmac_sha256 "$attestation_hmac" '. + {auth_hmac_sha256:$auth_hmac_sha256}' "$work_dir/attestation-payload.json" >"$work_dir/attestation.json"
attestation_sha256=$(sha256sum "$work_dir/attestation.json" | awk '{print $1}')

attestation_key="restore-attestations/$(date -u -d "$completed_at" +%Y-%m-%dT%H-%M-%SZ)-$attestation_sha256.json"
attestation_checksum=$(openssl dgst -sha256 -binary "$work_dir/attestation.json" | base64 | tr -d '\n')
attestation_version=$(backup_aws s3api put-object \
	--bucket "$BACKUP_S3_BUCKET" --key "$attestation_key" --body "$work_dir/attestation.json" \
	--content-type application/json --checksum-algorithm SHA256 --checksum-sha256 "$attestation_checksum" \
	--if-none-match '*' \
	--server-side-encryption aws:kms --ssekms-key-id "$BACKUP_KMS_KEY_ID" --output json | jq -er '.VersionId | strings | select(length > 0)')
attestation_head="$work_dir/attestation-head.json"
backup_aws s3api head-object --bucket "$BACKUP_S3_BUCKET" --key "$attestation_key" --version-id "$attestation_version" --checksum-mode ENABLED --output json >"$attestation_head"
jq -e --arg version "$attestation_version" --arg checksum "$attestation_checksum" --arg kms "$BACKUP_KMS_KEY_ID" '
	.VersionId == $version and .ChecksumSHA256 == $checksum and .ServerSideEncryption == "aws:kms" and .SSEKMSKeyId == $kms
' "$attestation_head" >/dev/null

# The local promotion marker is committed only after the unique off-host copy
# has been written and verified. A failed upload cannot leave reusable success.
install -d -m 0700 "$attestation_dir"
attestation_target="$attestation_dir/latest.json"
temporary=$(mktemp "$attestation_dir/.latest.XXXXXX")
install -m 0600 "$work_dir/attestation.json" "$temporary"
mv "$temporary" "$attestation_target"

log_event "privacy_restore_drill_succeeded attestation_sha256=$attestation_sha256 backup_age_seconds=$((completed_epoch - $(date -u -d "$backup_created_at" +%s))) ledger_object_count=$ledger_object_count replayed_count=$(jq -r .replayed_count "$replay_result") absence_verified_count=$(jq -r .absence_verified_count "$replay_result")"
