#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
work_dir=$(mktemp -d /var/tmp/mycfc-restore.XXXXXX)
container=mycfc-postgres-restore-drill
trap 'docker rm -f "$container" >/dev/null 2>&1 || true; rm -rf "$work_dir"' EXIT HUP INT TERM

set -a
. "$env_file"
set +a
export AWS_SHARED_CREDENTIALS_FILE=/etc/mycfc/backup-aws/credentials
export AWS_PROFILE=mycfc-backup
export AWS_REGION="${AWS_REGION:-eu-west-1}"
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN

manifest_key=$(aws s3api list-objects-v2 --bucket "$BACKUP_S3_BUCKET" --prefix daily/ --query 'reverse(sort_by(Contents[?ends_with(Key, `.json`)], &LastModified))[0].Key' --output text)
test "$manifest_key" != None
aws s3 cp "s3://$BACKUP_S3_BUCKET/$manifest_key" "$work_dir/manifest.json" >/dev/null
dump_key=${manifest_key%.json}.dump.enc
aws s3 cp "s3://$BACKUP_S3_BUCKET/$dump_key" "$work_dir/dump.enc" >/dev/null
test "$(shasum -a 256 "$work_dir/dump.enc" | cut -d ' ' -f 1)" = "$(jq -r .sha256 "$work_dir/manifest.json")"
jq -r .ciphertext "$work_dir/manifest.json" | base64 -d >"$work_dir/key.enc"
aws kms decrypt --ciphertext-blob "fileb://$work_dir/key.enc" --output json | jq -r .Plaintext | base64 -d | od -An -tx1 | tr -d ' \n' >"$work_dir/key.hex"
openssl enc -d -aes-256-cbc -K "$(cat "$work_dir/key.hex")" -iv "$(jq -r .iv "$work_dir/manifest.json")" -nosalt -in "$work_dir/dump.enc" -out "$work_dir/dump"
docker run -d --name "$container" -e POSTGRES_PASSWORD=restore -e POSTGRES_DB="$POSTGRES_DB" postgres:16.9-alpine >/dev/null
until docker exec "$container" pg_isready -U postgres -d "$POSTGRES_DB" >/dev/null 2>&1; do sleep 1; done
sleep 2
docker cp "$work_dir/dump" "$container":/tmp/dump
docker exec "$container" pg_restore --no-owner -U postgres -d "$POSTGRES_DB" /tmp/dump
docker exec "$container" psql -U postgres -d "$POSTGRES_DB" -tAc "SELECT to_regclass('public.users') IS NOT NULL" | grep -qx t
