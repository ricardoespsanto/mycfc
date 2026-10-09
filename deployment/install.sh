#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
state_dir=/etc/mycfc/deployment
release_credentials_file=/etc/mycfc/release-aws/credentials
guardian_release_bind_env_file=/etc/mycfc/guardian-release-bind.env
database_maintenance_env_file=/etc/mycfc/database-maintenance.env
database_control_env_file=/etc/mycfc/database-control.env

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

if [ ! -f "$release_credentials_file" ] || [ "$(stat -c '%u:%a' "$release_credentials_file")" != '0:600' ]; then
	printf '%s\n' "$release_credentials_file must exist, be owned by root, and have mode 0600." >&2
	exit 1
fi

# The first rollout must stage this restricted login against the pre-005
# database before installation starts the ordinary release service. The
# installer validates custody but deliberately never provisions or rotates it.
if [ ! -f "$guardian_release_bind_env_file" ] || [ -L "$guardian_release_bind_env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$guardian_release_bind_env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	printf '%s\n' "$guardian_release_bind_env_file must exist, be a root-owned regular file, and have mode 0600; stage the release-bind login before running install.sh." >&2
	exit 1
fi
for required in GUARDIAN_RELEASE_BIND_DATABASE_URL GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE; do
	if [ "$(grep -c "^$required=" "$guardian_release_bind_env_file" 2>/dev/null || true)" -ne 1 ]; then
		printf '%s\n' "$guardian_release_bind_env_file is missing the exact release-bind credential contract." >&2
		exit 1
	fi
done
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
		''|\#*) ;;
		GUARDIAN_RELEASE_BIND_DATABASE_URL=*|GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=*) ;;
		*) printf '%s\n' "$guardian_release_bind_env_file contains an unsupported key." >&2; exit 1 ;;
	esac
done <"$guardian_release_bind_env_file"

if [ ! -f "$database_maintenance_env_file" ] || [ -L "$database_maintenance_env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$database_maintenance_env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	printf '%s\n' "$database_maintenance_env_file must be a root-owned regular file with mode 0600." >&2
	exit 1
fi
for required in MEDIA_CLEANUP_DB_USER MEDIA_CLEANUP_DB_PASSWORD DATA_RETENTION_DB_USER DATA_RETENTION_DB_PASSWORD; do
	if [ "$(grep -c "^$required=" "$database_maintenance_env_file" 2>/dev/null || true)" -ne 1 ]; then
		printf '%s\n' "$database_maintenance_env_file is missing the exact maintenance credential contract." >&2
		exit 1
	fi
done
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
		''|\#*) ;;
		MEDIA_CLEANUP_DB_USER=*|MEDIA_CLEANUP_DB_PASSWORD=*|DATA_RETENTION_DB_USER=*|DATA_RETENTION_DB_PASSWORD=*) ;;
		*) printf '%s\n' "$database_maintenance_env_file contains an unsupported key." >&2; exit 1 ;;
	esac
done <"$database_maintenance_env_file"

if [ ! -f "$database_control_env_file" ] || [ -L "$database_control_env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$database_control_env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	printf '%s\n' "$database_control_env_file must be a root-owned regular file with mode 0600." >&2
	exit 1
fi
for required in POSTGRES_PASSWORD MIGRATION_DB_PASSWORD; do
	if [ "$(grep -c "^$required=" "$database_control_env_file" 2>/dev/null || true)" -ne 1 ]; then
		printf '%s\n' "$database_control_env_file is missing the exact privileged database credential contract." >&2
		exit 1
	fi
done
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
		''|\#*) ;;
		POSTGRES_PASSWORD=*|MIGRATION_DB_PASSWORD=*) ;;
		*) printf '%s\n' "$database_control_env_file contains an unsupported key." >&2; exit 1 ;;
	esac
done <"$database_control_env_file"

if ! command -v aws >/dev/null 2>&1; then
	printf '%s\n' 'Missing required command: aws' >&2
	exit 1
fi

install -d -m 0755 "$state_dir"
if [ ! -f "$state_dir/active-slot" ]; then
	printf '%s\n' legacy >"$state_dir/active-slot"
	chmod 0644 "$state_dir/active-slot"
fi
if [ ! -f "$state_dir/caddy-upstream.caddy" ]; then
	cat >"$state_dir/caddy-upstream.caddy" <<'EOF'
reverse_proxy app:8080 {
	health_uri /health/live
	health_interval 10s
	health_timeout 2s
}
EOF
	chmod 0644 "$state_dir/caddy-upstream.caddy"
fi

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

case "${BACKUP_MANIFEST_AUTH_ENABLED:-false}" in
	true)
		if [ ! -f /etc/mycfc/backup-auth/manifest.key ] || [ "$(stat -c '%u:%a' /etc/mycfc/backup-auth/manifest.key)" != '0:600' ]; then
			printf '%s\n' '/etc/mycfc/backup-auth/manifest.key must be owned by root and have mode 0600.' >&2
			exit 1
		fi
		if ! tr -d '\n' </etc/mycfc/backup-auth/manifest.key | grep -Eq '^[0-9A-Fa-f]{64}$'; then
			printf '%s\n' 'The backup manifest authentication key must contain exactly 32 bytes encoded as hexadecimal.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'BACKUP_MANIFEST_AUTH_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${POSTGRES_RESTORE_VERIFICATION_ENABLED:-false}" in
	true)
		if [ "${BACKUP_MANIFEST_AUTH_ENABLED:-false}" != true ]; then
			printf '%s\n' 'POSTGRES_RESTORE_VERIFICATION_ENABLED=true requires BACKUP_MANIFEST_AUTH_ENABLED=true.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'POSTGRES_RESTORE_VERIFICATION_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}" in
	true)
		if ! printf '%s' "${BACKUP_CLEANUP_ROLE_ARN:-}" | grep -Eq '^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$'; then
			printf '%s\n' 'BACKUP_CLEANUP_ROLE_ARN must identify the exact cleanup role.' >&2
			exit 1
		fi
		if [ ! -f /etc/mycfc/backup-cleanup-aws/credentials ] || [ -L /etc/mycfc/backup-cleanup-aws/credentials ] || [ "$(stat -c '%u:%g:%a' /etc/mycfc/backup-cleanup-aws/credentials)" != '0:0:600' ]; then
			printf '%s\n' '/etc/mycfc/backup-cleanup-aws/credentials must be owned by root and have mode 0600.' >&2
			exit 1
		fi
		if ! awk '
			/^[[:space:]]*(#|;|$)/ { next }
			/^\[mycfc-backup-cleanup\][[:space:]]*$/ { section = "cleanup"; next }
			/^\[/ { invalid = 1; next }
			section != "cleanup" { invalid = 1; next }
			/^[[:space:]]*aws_access_key_id[[:space:]]*=[[:space:]]*ASIA[A-Z0-9]+[[:space:]]*$/ { access = 1; next }
			/^[[:space:]]*aws_secret_access_key[[:space:]]*=[[:space:]]*[^[:space:]]+[[:space:]]*$/ { secret = 1; next }
			/^[[:space:]]*aws_session_token[[:space:]]*=[[:space:]]*[^[:space:]]+[[:space:]]*$/ { token = 1; next }
			{ invalid = 1 }
			END { exit !(access && secret && token && !invalid) }
		' /etc/mycfc/backup-cleanup-aws/credentials; then
			printf '%s\n' 'The cleanup profile must contain only renewable STS session credentials.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'BACKUP_NONCURRENT_CLEANER_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}" in
	true | false) ;;
	*) printf '%s\n' 'BACKUP_NONCURRENT_CLEANER_DRY_RUN must be true or false.' >&2; exit 1 ;;
esac

AWS_REGION=${AWS_REGION:-eu-west-1}
BACKUP_NONCURRENT_DELETE_AGE_SECONDS=${BACKUP_NONCURRENT_DELETE_AGE_SECONDS:-82800}
if ! printf '%s' "$BACKUP_S3_BUCKET" | grep -Eq '^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$' || ! printf '%s' "$AWS_REGION" | grep -Eq '^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$'; then
	printf '%s\n' 'The backup cleanup bucket or AWS region is invalid.' >&2
	exit 1
fi
case "$BACKUP_NONCURRENT_DELETE_AGE_SECONDS" in
	'' | *[!0-9]*) printf '%s\n' 'BACKUP_NONCURRENT_DELETE_AGE_SECONDS must be a positive integer.' >&2; exit 1 ;;
esac
if [ "$BACKUP_NONCURRENT_DELETE_AGE_SECONDS" -eq 0 ] || [ "$BACKUP_NONCURRENT_DELETE_AGE_SECONDS" -gt 82800 ]; then
	printf '%s\n' 'BACKUP_NONCURRENT_DELETE_AGE_SECONDS must preserve the 23-hour verification boundary.' >&2
	exit 1
fi
cleanup_env_tmp=$(mktemp /etc/mycfc/.backup-cleanup.env.XXXXXX)
chmod 0600 "$cleanup_env_tmp"
{
	printf "BACKUP_S3_BUCKET='%s'\n" "$BACKUP_S3_BUCKET"
	printf "AWS_REGION='%s'\n" "$AWS_REGION"
	printf "BACKUP_CLEANUP_ROLE_ARN='%s'\n" "${BACKUP_CLEANUP_ROLE_ARN:-}"
	printf "BACKUP_NONCURRENT_CLEANER_ENABLED='%s'\n" "${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}"
	printf "BACKUP_NONCURRENT_CLEANER_DRY_RUN='%s'\n" "${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}"
	printf "BACKUP_NONCURRENT_DELETE_AGE_SECONDS='%s'\n" "$BACKUP_NONCURRENT_DELETE_AGE_SECONDS"
} >"$cleanup_env_tmp"
chown root:root "$cleanup_env_tmp"
mv -f "$cleanup_env_tmp" /etc/mycfc/backup-cleanup.env

case "${MEDIA_CLEANUP_ENABLED:-false}" in
	true)
		[ ! -L /etc/mycfc/media-cleanup.env ] && [ -f /etc/mycfc/media-cleanup.env ] && [ "$(stat -c '%u:%g:%a' /etc/mycfc/media-cleanup.env)" = '0:0:600' ] || {
			printf '%s\n' '/etc/mycfc/media-cleanup.env must be a root-owned 0600 regular file.' >&2; exit 1;
		}
		[ ! -L /etc/mycfc/media-cleanup/keys ] && [ -d /etc/mycfc/media-cleanup/keys ] && [ "$(stat -c '%u:%g:%a' /etc/mycfc/media-cleanup/keys)" = '0:65532:750' ] || {
			printf '%s\n' '/etc/mycfc/media-cleanup/keys must be a root:65532 0750 directory.' >&2; exit 1;
		}
		for path in /etc/mycfc/media-cleanup/keys/media-upload-private.key /etc/mycfc/media-cleanup/keys/media-cleanup-evidence.key; do
			[ ! -L "$path" ] && [ -f "$path" ] && [ "$(stat -c '%u:%g:%a' "$path")" = '0:65532:440' ] || {
				printf '%s\n' "$path must be a root:65532 0440 regular file." >&2; exit 1;
			}
		done
		;;
	false) ;;
	*) printf "%s\n" "MEDIA_CLEANUP_ENABLED must be true or false." >&2; exit 1 ;;
esac

case "${HETZNER_BACKUP_POSTURE_ENABLED:-false}" in
	true)
		if [ ! -f /etc/mycfc/hetzner-read/token ] || [ "$(stat -c '%u:%a' /etc/mycfc/hetzner-read/token)" != '0:600' ]; then
			printf '%s\n' '/etc/mycfc/hetzner-read/token must be owned by root and have mode 0600.' >&2
			exit 1
		fi
		case "${HETZNER_SERVER_ID:-}" in
			''|*[!0-9]*) printf '%s\n' 'HETZNER_SERVER_ID must be a positive integer.' >&2; exit 1 ;;
		esac
		if [ "$HETZNER_SERVER_ID" -eq 0 ] || ! printf '%s' "${HETZNER_PROJECT_REF:-}" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$'; then
			printf '%s\n' 'Hetzner posture scope is invalid.' >&2
			exit 1
		fi
		;;
	false) ;;
	*)
		printf '%s\n' 'HETZNER_BACKUP_POSTURE_ENABLED must be true or false.' >&2
		exit 1
		;;
esac

for command in aws awk base64 cmp curl date docker flock gh hostname jq logger od openssl python3 sed seq sha256sum; do
	if ! command -v "$command" >/dev/null 2>&1; then
		printf '%s\n' "Missing required command: $command" >&2
		exit 1
	fi
done

chmod 0755 "$deployment_dir/run-with-cloudwatch-logs.sh"
chmod 0755 "$deployment_dir/release-status.sh"
chmod 0755 "$deployment_dir/postgres-backup-version-cleanup.sh"
chmod 0755 "$deployment_dir/postgres-backup-version-cleanup-cloudwatch.sh"
chmod 0755 "$deployment_dir/hetzner-backup-posture.sh"
chmod 0755 "$deployment_dir/guardian-activation.sh"
chmod 0755 "$deployment_dir/guardian-release-bind.sh"
chmod 0755 "$deployment_dir/postgres-restore-verification.sh"
install -m 0644 "$deployment_dir/mycfc-pull-release.service" /etc/systemd/system/mycfc-pull-release.service
install -m 0644 "$deployment_dir/mycfc-pull-release.timer" /etc/systemd/system/mycfc-pull-release.timer
install -m 0644 "$deployment_dir/mycfc-postgres-backup.service" /etc/systemd/system/mycfc-postgres-backup.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup.timer" /etc/systemd/system/mycfc-postgres-backup.timer
install -m 0644 "$deployment_dir/mycfc-postgres-backup-version-cleanup.service" /etc/systemd/system/mycfc-postgres-backup-version-cleanup.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup-version-cleanup-log.service" /etc/systemd/system/mycfc-postgres-backup-version-cleanup-log.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup-version-cleanup.timer" /etc/systemd/system/mycfc-postgres-backup-version-cleanup.timer
install -m 0644 "$deployment_dir/mycfc-hetzner-backup-posture.service" /etc/systemd/system/mycfc-hetzner-backup-posture.service
install -m 0644 "$deployment_dir/mycfc-hetzner-backup-posture.timer" /etc/systemd/system/mycfc-hetzner-backup-posture.timer
install -m 0644 "$deployment_dir/mycfc-media-cleanup.service" /etc/systemd/system/mycfc-media-cleanup.service
install -m 0644 "$deployment_dir/mycfc-postgres-restore-verification.service" /etc/systemd/system/mycfc-postgres-restore-verification.service
install -m 0644 "$deployment_dir/mycfc-postgres-restore-verification.timer" /etc/systemd/system/mycfc-postgres-restore-verification.timer
chmod 0755 "$deployment_dir/retire-privacy-automation.sh"
"$deployment_dir/retire-privacy-automation.sh"
systemctl daemon-reload
systemctl enable mycfc-pull-release.timer
systemctl enable --now mycfc-postgres-backup.timer
if [ "${POSTGRES_RESTORE_VERIFICATION_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-postgres-restore-verification.timer
else
	systemctl disable --now mycfc-postgres-restore-verification.timer >/dev/null 2>&1 || true
fi
if [ "${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}" = true ] && [ "${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}" = false ]; then
	systemctl enable --now mycfc-postgres-backup-version-cleanup.timer
else
	systemctl disable --now mycfc-postgres-backup-version-cleanup.timer >/dev/null 2>&1 || true
fi
if [ "${HETZNER_BACKUP_POSTURE_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-hetzner-backup-posture.timer
else
	systemctl disable --now mycfc-hetzner-backup-posture.timer >/dev/null 2>&1 || true
fi
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" build caddy
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" up -d --no-deps --force-recreate caddy
systemctl start mycfc-pull-release.service
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" up -d --no-deps cloudflared
systemctl start mycfc-pull-release.timer
if [ "${MEDIA_CLEANUP_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-media-cleanup.service
else
	systemctl disable --now mycfc-media-cleanup.service >/dev/null 2>&1 || true
fi
