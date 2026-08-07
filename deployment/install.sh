#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
	printf '%s\n' 'Run this script as root so it can verify the protected environment file.' >&2
	exit 1
fi

if [ ! -f "$env_file" ]; then
	printf '%s\n' "Missing $env_file. See $deployment_dir/README.md." >&2
	exit 1
fi

if [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	printf '%s\n' "$env_file must be owned by root and have mode 0600." >&2
	exit 1
fi

if ! command -v aws >/dev/null 2>&1; then
	printf '%s\n' 'Missing required command: aws' >&2
	exit 1
fi

"$deployment_dir/resolve-runtime-secrets.sh" "$env_file"
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" config -q

set -a
. "$env_file"
set +a
: "${BACKUP_S3_BUCKET:?set BACKUP_S3_BUCKET in /etc/mycfc/mycfc.env}"
: "${BACKUP_KMS_KEY_ID:?set BACKUP_KMS_KEY_ID in /etc/mycfc/mycfc.env}"

if [ ! -f /etc/mycfc/backup-aws/credentials ] || [ "$(stat -c '%u:%a' /etc/mycfc/backup-aws/credentials)" != '0:600' ]; then
	printf '%s\n' '/etc/mycfc/backup-aws/credentials must be owned by root and have mode 0600.' >&2
	exit 1
fi

for command in aws base64 curl docker flock jq logger od openssl shasum; do
	if ! command -v "$command" >/dev/null 2>&1; then
		printf '%s\n' "Missing required command: $command" >&2
		exit 1
	fi
done

install -m 0644 "$deployment_dir/mycfc-pull-release.service" /etc/systemd/system/mycfc-pull-release.service
install -m 0644 "$deployment_dir/mycfc-pull-release.timer" /etc/systemd/system/mycfc-pull-release.timer
install -m 0644 "$deployment_dir/mycfc-postgres-backup.service" /etc/systemd/system/mycfc-postgres-backup.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup.timer" /etc/systemd/system/mycfc-postgres-backup.timer
systemctl daemon-reload
systemctl enable --now mycfc-pull-release.timer
systemctl enable --now mycfc-postgres-backup.timer
systemctl start mycfc-pull-release.service
