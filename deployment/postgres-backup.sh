#!/bin/sh
set -eu
umask 077

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
compose_file=${MYCFC_COMPOSE_FILE:-/opt/mycfc/deployment/compose.yaml}
credentials_file=${MYCFC_BACKUP_CREDENTIALS_FILE:-/etc/mycfc/backup-aws/credentials}
manifest_auth_key_file=${MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE:-/etc/mycfc/backup-auth/manifest.key}
work_dir=

cleanup() {
	status=$?
	[ -z "$work_dir" ] || rm -rf "$work_dir"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

work_dir=$(mktemp -d /var/tmp/mycfc-backup.XXXXXX)

set -a
. "$env_file"
set +a
: "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_KMS_KEY_ID:?}"

BACKUP_MANIFEST_AUTH_ENABLED=${BACKUP_MANIFEST_AUTH_ENABLED:-false}
case "$BACKUP_MANIFEST_AUTH_ENABLED" in
	true|false) ;;
	*) printf '%s\n' 'BACKUP_MANIFEST_AUTH_ENABLED must be true or false.' >&2; exit 1 ;;
esac

export AWS_SHARED_CREDENTIALS_FILE="$credentials_file"
export AWS_PROFILE="${AWS_PROFILE:-mycfc-backup}"
export AWS_REGION="${AWS_REGION:-eu-west-1}"
export AWS_PAGER=""
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN

if [ "$BACKUP_MANIFEST_AUTH_ENABLED" = true ]; then
	if [ ! -f "$manifest_auth_key_file" ] || [ "$(stat -c '%u:%a' "$manifest_auth_key_file")" != '0:600' ]; then
		printf '%s\n' 'Backup manifest authentication key is missing or insecure.' >&2
		exit 1
	fi
	if ! tr -d '\n' <"$manifest_auth_key_file" | grep -Eq '^[0-9A-Fa-f]{64}$'; then
		printf '%s\n' 'Backup manifest authentication key is invalid.' >&2
		exit 1
	fi
fi

hmac_sha256() {
	printf '%s' "$1" |
		python3 -c 'import hashlib,hmac,pathlib,sys; key=bytes.fromhex(pathlib.Path(sys.argv[1]).read_text().strip()); sys.stdout.write(hmac.new(key,sys.stdin.buffer.read(),hashlib.sha256).hexdigest())' "$manifest_auth_key_file"
}

stamp=$(date -u +%Y-%m-%dT%H-%M-%SZ)
created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
dump="$work_dir/$stamp.dump"
encrypted="$dump.enc"
key_json="$work_dir/key.json"
key_hex_file="$work_dir/key.hex"

docker compose --env-file "$env_file" -f "$compose_file" exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc >"$dump"
aws kms generate-data-key --key-id "$BACKUP_KMS_KEY_ID" --key-spec AES_256 --output json >"$key_json"
jq -r '.Plaintext' "$key_json" | base64 -d | od -An -v -tx1 | tr -d ' \n' >"$key_hex_file"
chmod 0600 "$key_hex_file"
# File-based passphrase input keeps the KMS plaintext data key out of argv and
# shell traces. The manifest authenticates the ciphertext digest separately.
openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 -salt -pass "file:$key_hex_file" -in "$dump" -out "$encrypted"
sha256=$(sha256sum "$encrypted" | awk '{print $1}')
checksum_base64=$(openssl dgst -sha256 -binary "$encrypted" | base64 | tr -d '\n')

upload_recovery_point() {
	prefix=$1
	dump_key="$prefix/$stamp.dump.enc"
	manifest_key="$prefix/$stamp.json"
	manifest="$work_dir/$prefix-manifest.json"

	dump_version=$(aws s3api put-object \
		--bucket "$BACKUP_S3_BUCKET" \
		--key "$dump_key" \
		--body "$encrypted" \
		--checksum-algorithm SHA256 \
		--checksum-sha256 "$checksum_base64" \
		--if-none-match '*' \
		--server-side-encryption aws:kms \
		--ssekms-key-id "$BACKUP_KMS_KEY_ID" \
		--output json | jq -er '.VersionId | strings | select(length > 0)')

	if [ "$BACKUP_MANIFEST_AUTH_ENABLED" = true ]; then
		jq -n \
			--arg contract 'mycfc/postgres-backup/v3' \
			--arg ciphertext "$(jq -r '.CiphertextBlob' "$key_json")" \
			--arg cipher 'AES-256-CBC' \
			--arg kdf 'PBKDF2-HMAC-SHA256' \
			--argjson kdf_iterations 600000 \
			--arg sha256 "$sha256" \
			--arg database "$POSTGRES_DB" \
			--arg created_at "$created_at" \
			--arg dump_key "$dump_key" \
			--arg dump_version "$dump_version" \
			'{contract:$contract,cipher:$cipher,kdf:$kdf,kdf_iterations:$kdf_iterations,ciphertext:$ciphertext,sha256:$sha256,database:$database,created_at:$created_at,dump_key:$dump_key,dump_version:$dump_version}' \
			>"$manifest"
		canonical=$(jq -Sc . "$manifest")
		auth_hmac_sha256=$(hmac_sha256 "$canonical")
		jq --arg auth_hmac_sha256 "$auth_hmac_sha256" '. + {auth_hmac_sha256:$auth_hmac_sha256}' "$manifest" >"$manifest.signed"
		mv "$manifest.signed" "$manifest"
	else
		jq -n --arg ciphertext "$(jq -r '.CiphertextBlob' "$key_json")" --arg cipher 'AES-256-CBC' --arg kdf 'PBKDF2-HMAC-SHA256' --argjson kdf_iterations 600000 \
			--arg sha256 "$sha256" --arg database "$POSTGRES_DB" --arg created_at "$created_at" \
			'{cipher:$cipher,kdf:$kdf,kdf_iterations:$kdf_iterations,ciphertext:$ciphertext,sha256:$sha256,database:$database,created_at:$created_at}' >"$manifest"
	fi

	manifest_checksum_base64=$(openssl dgst -sha256 -binary "$manifest" | base64 | tr -d '\n')
	aws s3api put-object \
		--bucket "$BACKUP_S3_BUCKET" \
		--key "$manifest_key" \
		--body "$manifest" \
		--content-type application/json \
		--checksum-algorithm SHA256 \
		--checksum-sha256 "$manifest_checksum_base64" \
		--if-none-match '*' \
		--server-side-encryption aws:kms \
		--ssekms-key-id "$BACKUP_KMS_KEY_ID" \
		--output json >/dev/null
}

upload_recovery_point daily
if [ "$(date -u +%d)" = 01 ]; then
	upload_recovery_point monthly
fi
