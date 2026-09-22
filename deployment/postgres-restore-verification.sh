#!/bin/sh
set -eu
umask 077

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
credentials_file=${MYCFC_BACKUP_CREDENTIALS_FILE:-/etc/mycfc/backup-aws/credentials}
manifest_key_file=${MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE:-/etc/mycfc/backup-auth/manifest.key}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
work_dir=
network=
database_container=

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	[ -z "$database_container" ] || docker rm -f "$database_container" >/dev/null 2>&1 || true
	[ -z "$network" ] || docker network rm "$network" >/dev/null 2>&1 || true
	[ -z "$work_dir" ] || rm -rf "$work_dir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run as root.' >&2; exit 1; }
[ ! -L "$env_file" ] && [ -f "$env_file" ] && [ "$(stat -c '%u:%a' "$env_file")" = '0:600' ] || {
	printf '%s\n' 'The protected environment file is missing or insecure.' >&2; exit 1;
}
set -a
. "$env_file"
set +a
: "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_KMS_KEY_ID:?}"
: "${POSTGRES_DB:?}"
[ "${POSTGRES_RESTORE_VERIFICATION_ENABLED:-false}" = true ] || {
	printf '%s\n' 'postgres_restore_verification_disabled' >&2; exit 1;
}
[ "${BACKUP_MANIFEST_AUTH_ENABLED:-false}" = true ] || {
	printf '%s\n' 'postgres_restore_verification_requires_authenticated_backups' >&2; exit 1;
}
candidate_image=${1:-${MYCFC_RESTORE_VERIFICATION_IMAGE:-${MYCFC_IMAGE:-}}}
printf '%s' "$candidate_image" | grep -Eq '@sha256:[0-9a-f]{64}$' || {
	printf '%s\n' 'Restore verification requires an immutable image digest.' >&2; exit 1;
}
for file in "$credentials_file" "$manifest_key_file"; do
	[ ! -L "$file" ] && [ -f "$file" ] && [ "$(stat -c '%u:%a' "$file")" = '0:600' ] || {
		printf '%s\n' 'A protected restore input is missing or insecure.' >&2; exit 1;
	}
	done
tr -d '\n' <"$manifest_key_file" | grep -Eq '^[0-9A-Fa-f]{64}$' || {
	printf '%s\n' 'The backup manifest authentication key is invalid.' >&2; exit 1;
}

exec 9>"$runtime_dir/mycfc-postgres-restore-verification.lock"
flock -n 9 || { printf '%s\n' 'Restore verification is already running.' >&2; exit 1; }
work_dir=$(mktemp -d /var/tmp/mycfc-restore-verification.XXXXXX)
suffix=$(basename "$work_dir" | tr -cd 'A-Za-z0-9')
network="mycfc-restore-verification-$suffix"
database_container="mycfc-restore-verification-db-$suffix"
export AWS_REGION="${AWS_REGION:-eu-west-1}" AWS_PAGER=''
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN

backup_aws() {
	AWS_SHARED_CREDENTIALS_FILE="$credentials_file" AWS_PROFILE="${MYCFC_BACKUP_AWS_PROFILE:-mycfc-backup}" aws "$@"
}

for prefix in daily monthly; do
	backup_aws s3api list-object-versions --bucket "$BACKUP_S3_BUCKET" --prefix "$prefix/" --output json >"$work_dir/$prefix.json"
done
jq -r '(.Versions // [])[] | select(.IsLatest and (.Key | test("^(daily|monthly)/.*\\.json$"))) | [.LastModified,.Key,.VersionId] | @tsv' \
	"$work_dir/daily.json" "$work_dir/monthly.json" | sort -r >"$work_dir/candidates.tsv"

selected=false
while IFS="$(printf '\t')" read -r modified manifest_key manifest_version; do
	[ -n "$modified" ] || continue
	if backup_aws s3api get-object --bucket "$BACKUP_S3_BUCKET" --key "$manifest_key" --version-id "$manifest_version" "$work_dir/manifest.json" >/dev/null 2>&1 &&
		jq -e --arg database "$POSTGRES_DB" '.contract=="mycfc/postgres-backup/v3" and .database==$database and (.dump_key|type=="string") and (.dump_version|type=="string") and (.sha256|test("^[0-9a-f]{64}$")) and (.auth_hmac_sha256|test("^[0-9a-f]{64}$"))' "$work_dir/manifest.json" >/dev/null &&
		python3 - "$manifest_key_file" "$work_dir/manifest.json" <<'PY'
import hashlib, hmac, json, pathlib, sys
key = bytes.fromhex(pathlib.Path(sys.argv[1]).read_text().strip())
document = json.loads(pathlib.Path(sys.argv[2]).read_text())
expected = document.pop("auth_hmac_sha256")
canonical = json.dumps(document, separators=(",", ":"), sort_keys=True).encode()
raise SystemExit(0 if hmac.compare_digest(hmac.new(key, canonical, hashlib.sha256).hexdigest(), expected) else 1)
PY
	then
		dump_key=$(jq -r .dump_key "$work_dir/manifest.json")
		dump_version=$(jq -r .dump_version "$work_dir/manifest.json")
		if backup_aws s3api get-object --bucket "$BACKUP_S3_BUCKET" --key "$dump_key" --version-id "$dump_version" "$work_dir/dump.enc" >/dev/null 2>&1 &&
			[ "$(sha256sum "$work_dir/dump.enc" | awk '{print $1}')" = "$(jq -r .sha256 "$work_dir/manifest.json")" ]; then
			selected=true
			break
		fi
	fi
done <"$work_dir/candidates.tsv"
[ "$selected" = true ] || { printf '%s\n' 'No authenticated retained backup passed verification.' >&2; exit 1; }

jq -r .ciphertext "$work_dir/manifest.json" | base64 -d >"$work_dir/key.enc"
backup_aws kms decrypt --ciphertext-blob "fileb://$work_dir/key.enc" --output json | jq -r .Plaintext | base64 -d | od -An -v -tx1 | tr -d ' \n' >"$work_dir/key.hex"
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 -pass "file:$work_dir/key.hex" -in "$work_dir/dump.enc" -out "$work_dir/dump"

docker network create --internal "$network" >/dev/null
restore_password=$(openssl rand -hex 32)
POSTGRES_PASSWORD="$restore_password" docker run -d --name "$database_container" --network "$network" -e POSTGRES_PASSWORD -e POSTGRES_DB=mycfc_restore postgres:16.9-alpine3.21 >/dev/null
ready=false
for _ in $(seq 1 60); do
	if docker exec "$database_container" pg_isready -U postgres -d mycfc_restore >/dev/null 2>&1; then ready=true; break; fi
	sleep 1
done
[ "$ready" = true ] || { printf '%s\n' 'Isolated PostgreSQL did not become ready.' >&2; exit 1; }
docker cp "$work_dir/dump" "$database_container":/tmp/dump >/dev/null
docker exec "$database_container" pg_restore --exit-on-error --no-owner --no-acl -U postgres -d mycfc_restore /tmp/dump
docker exec "$database_container" psql -v ON_ERROR_STOP=1 -U postgres -d mycfc_restore -c 'CREATE ROLE mycfc_restore_app NOLOGIN' >/dev/null
database_url="postgres://postgres:$restore_password@$database_container:5432/mycfc_restore?sslmode=disable"
DATABASE_URL="$database_url" docker run --rm --network "$network" -e DATABASE_URL -e APP_DB_USER=mycfc_restore_app -e MIGRATION_DB_USER=postgres "$candidate_image" migrate
docker exec "$database_container" psql -v ON_ERROR_STOP=1 -U postgres -d mycfc_restore -Atc "
SELECT CASE WHEN to_regclass('public.users') IS NULL THEN 1/0 ELSE 1 END;
SELECT CASE WHEN EXISTS(SELECT 1 FROM mycfc_meta.schema_migrations WHERE version='202609220001_privacy_automation_retirement') THEN 1 ELSE 1/0 END;
SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM privacy_request_activation WHERE enabled OR fulfilment_ready) THEN 1 ELSE 1/0 END;
SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM email_outbox WHERE message_type LIKE 'PRIVACY_%' AND status IN ('PENDING','SENDING')) THEN 1 ELSE 1/0 END;
" >/dev/null
printf '%s\n' 'postgres_restore_verification_succeeded'
