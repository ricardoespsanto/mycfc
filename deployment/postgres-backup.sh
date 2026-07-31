#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
compose_file=/opt/mycfc/deployment/compose.yaml
work_dir=$(mktemp -d /var/tmp/mycfc-backup.XXXXXX)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

set -a
. "$env_file"
set +a
: "${BACKUP_S3_BUCKET:?}"
: "${BACKUP_KMS_KEY_ID:?}"

export AWS_SHARED_CREDENTIALS_FILE=/etc/mycfc/backup-aws/credentials
export AWS_PROFILE=mycfc-backup
export AWS_REGION=${AWS_REGION:-eu-west-1}
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN

stamp=$(date -u +%Y-%m-%dT%H-%M-%SZ)
dump="$work_dir/$stamp.dump"
encrypted="$dump.enc"
key_json="$work_dir/key.json"

docker compose --env-file "$env_file" -f "$compose_file" exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc >"$dump"
aws kms generate-data-key --key-id "$BACKUP_KMS_KEY_ID" --key-spec AES_256 --output json >"$key_json"
key_hex=$(jq -r '.Plaintext' "$key_json" | base64 -d | od -An -tx1 | tr -d ' \n')
iv=$(openssl rand -hex 16)
openssl enc -aes-256-cbc -K "$key_hex" -iv "$iv" -nosalt -in "$dump" -out "$encrypted"
sha256=$(shasum -a 256 "$encrypted" | cut -d ' ' -f 1)

manifest="$work_dir/$stamp.json"
jq -n --arg ciphertext "$(jq -r '.CiphertextBlob' "$key_json")" --arg iv "$iv" --arg sha256 "$sha256" --arg database "$POSTGRES_DB" --arg created_at "$stamp" '{ciphertext:$ciphertext,iv:$iv,sha256:$sha256,database:$database,created_at:$created_at}' >"$manifest"

for prefix in daily; do
  aws s3 cp "$encrypted" "s3://$BACKUP_S3_BUCKET/$prefix/$stamp.dump.enc"
  aws s3 cp "$manifest" "s3://$BACKUP_S3_BUCKET/$prefix/$stamp.json"
done

if [ "$(date -u +%d)" = 01 ]; then
  aws s3 cp "$encrypted" "s3://$BACKUP_S3_BUCKET/monthly/$stamp.dump.enc"
  aws s3 cp "$manifest" "s3://$BACKUP_S3_BUCKET/monthly/$stamp.json"
fi
