#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
state_dir=/etc/mycfc/deployment
release_credentials_file=/etc/mycfc/release-aws/credentials

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

case "${PRIVACY_RESTORE_DRILL_ENABLED:-false}" in
	true)
		if [ "${BACKUP_MANIFEST_AUTH_ENABLED:-false}" != true ]; then
			printf '%s\n' 'PRIVACY_RESTORE_DRILL_ENABLED requires BACKUP_MANIFEST_AUTH_ENABLED=true.' >&2
			exit 1
		fi
		: "${PRIVACY_RESTORE_LEDGER_BUCKET:?set PRIVACY_RESTORE_LEDGER_BUCKET in /etc/mycfc/mycfc.env}"
		: "${PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN:?set PRIVACY_RESTORE_LEDGER_KMS_KEY_ARN in /etc/mycfc/mycfc.env}"
		for protected_file in /etc/mycfc/privacy-restore/credentials /etc/mycfc/privacy-restore/attestation.key /etc/mycfc/privacy-restore/tombstone-replay.key; do
			if [ ! -f "$protected_file" ] || [ "$(stat -c '%u:%a' "$protected_file")" != '0:600' ]; then
				printf '%s\n' 'A privacy restore input is missing or not root-owned mode 0600.' >&2
				exit 1
			fi
		done
		if ! tr -d '\n' </etc/mycfc/privacy-restore/attestation.key | grep -Eq '^[0-9A-Fa-f]{64}$'; then
			printf '%s\n' 'The restore attestation authentication key must contain exactly 32 bytes encoded as hexadecimal.' >&2
			exit 1
		fi
		install -d -m 0700 /etc/mycfc/privacy-restore/attestations
		;;
	false) ;;
	*) printf '%s\n' 'PRIVACY_RESTORE_DRILL_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${PRIVACY_RESTORE_PROMOTION_GATE_ENABLED:-false}" in
	true)
		if [ "${PRIVACY_RESTORE_DRILL_ENABLED:-false}" != true ]; then
			printf '%s\n' 'PRIVACY_RESTORE_PROMOTION_GATE_ENABLED requires PRIVACY_RESTORE_DRILL_ENABLED=true.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'PRIVACY_RESTORE_PROMOTION_GATE_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${PRIVACY_RETENTION_ENABLED:-false}" in
	true)
		if [ ! -f /etc/mycfc/privacy-retention.env ] || [ "$(stat -c '%u:%a' /etc/mycfc/privacy-retention.env)" != '0:600' ]; then
			printf '%s\n' '/etc/mycfc/privacy-retention.env must be owned by root and have mode 0600.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'PRIVACY_RETENTION_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${PRIVACY_COMPLETION_ENABLED:-false}" in
	true | false) ;;
	*) printf '%s\n' 'PRIVACY_COMPLETION_ENABLED must be true or false.' >&2; exit 1 ;;
esac

case "${PRIVACY_WORKER_ENABLED:-false}" in
	true)
		if [ "${PRIVACY_REQUESTS_ENABLED:-false}" != true ]; then
			printf '%s\n' 'PRIVACY_WORKER_ENABLED requires PRIVACY_REQUESTS_ENABLED=true.' >&2
			exit 1
		fi
		if [ "${PRIVACY_COMPLETION_ENABLED:-false}" != true ]; then
			printf '%s\n' 'PRIVACY_WORKER_ENABLED requires PRIVACY_COMPLETION_ENABLED=true.' >&2
			exit 1
		fi
		if [ ! -f /etc/mycfc/privacy-worker.env ] || [ "$(stat -c '%u:%a' /etc/mycfc/privacy-worker.env)" != '0:600' ]; then
			printf '%s\n' '/etc/mycfc/privacy-worker.env must be owned by root and have mode 0600.' >&2
			exit 1
		fi
		if [ ! -d /etc/mycfc/privacy-worker/keys ] || [ "$(stat -c '%u:%g:%a' /etc/mycfc/privacy-worker/keys)" != '0:65532:750' ]; then
			printf '%s\n' '/etc/mycfc/privacy-worker/keys must be root-owned, group 65532, and mode 0750.' >&2
			exit 1
		fi
		for protected_file in aws-credentials upload-private.key upload-evidence.key object-target-private.key object-evidence.key tombstone-public.key tombstone-locator.key provider-target-private.key provider-evidence.key provider-credential-digest-keys.json completion-delivery.key; do
			path=/etc/mycfc/privacy-worker/keys/$protected_file
			if [ ! -f "$path" ] || [ "$(stat -c '%u:%g:%a' "$path")" != '0:65532:440' ]; then
				printf '%s\n' 'A privacy worker key input is missing or does not have root:65532 mode 0440.' >&2
				exit 1
			fi
		done
		for name in PRIVACY_EXECUTOR_DB_USER PRIVACY_EXECUTOR_DB_PASSWORD PRIVACY_ACTIVATION_BROKER_DB_USER PRIVACY_ACTIVATION_BROKER_DB_PASSWORD PRIVACY_ACTIVATION_DISABLE_DB_USER PRIVACY_ACTIVATION_DISABLE_DB_PASSWORD; do
			case "$name" in
				PRIVACY_EXECUTOR_DB_USER) value=${PRIVACY_EXECUTOR_DB_USER:-} ;;
				PRIVACY_EXECUTOR_DB_PASSWORD) value=${PRIVACY_EXECUTOR_DB_PASSWORD:-} ;;
				PRIVACY_ACTIVATION_BROKER_DB_USER) value=${PRIVACY_ACTIVATION_BROKER_DB_USER:-} ;;
				PRIVACY_ACTIVATION_BROKER_DB_PASSWORD) value=${PRIVACY_ACTIVATION_BROKER_DB_PASSWORD:-} ;;
				PRIVACY_ACTIVATION_DISABLE_DB_USER) value=${PRIVACY_ACTIVATION_DISABLE_DB_USER:-} ;;
				PRIVACY_ACTIVATION_DISABLE_DB_PASSWORD) value=${PRIVACY_ACTIVATION_DISABLE_DB_PASSWORD:-} ;;
			esac
			if [ -z "$value" ]; then
				printf '%s\n' 'The privacy executor, activation broker, and disable database role bootstrap inputs are incomplete.' >&2
				exit 1
			fi
		done
		if [ "$PRIVACY_EXECUTOR_DB_USER" != mycfc_privacy_executor ] || [ "$PRIVACY_ACTIVATION_BROKER_DB_USER" != mycfc_privacy_activation_broker ] || [ "$PRIVACY_ACTIVATION_DISABLE_DB_USER" != mycfc_privacy_activation_disable ]; then
			printf '%s\n' 'The privacy database role identifiers must match the reviewed fixed identities.' >&2
			exit 1
		fi
		;;
	false) ;;
	*) printf '%s\n' 'PRIVACY_WORKER_ENABLED must be true or false.' >&2; exit 1 ;;
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

for command in aws awk base64 cmp curl date docker flock hostname jq logger od openssl python3 sed sha256sum; do
	if ! command -v "$command" >/dev/null 2>&1; then
		printf '%s\n' "Missing required command: $command" >&2
		exit 1
	fi
done

chmod 0755 "$deployment_dir/run-with-cloudwatch-logs.sh"
chmod 0755 "$deployment_dir/release-status.sh"
chmod 0755 "$deployment_dir/postgres-backup-version-cleanup.sh"
chmod 0755 "$deployment_dir/hetzner-backup-posture.sh"
chmod 0755 "$deployment_dir/postgres-restore-drill.sh"
chmod 0755 "$deployment_dir/privacy-restore-observer.sh"
chmod 0755 "$deployment_dir/verify-privacy-restore-attestation.sh"
chmod 0755 "$deployment_dir/privacy-retention.sh"
chmod 0755 "$deployment_dir/privacy-worker.sh"
chmod 0755 "$deployment_dir/privacy-activation.sh"
install -m 0644 "$deployment_dir/mycfc-pull-release.service" /etc/systemd/system/mycfc-pull-release.service
install -m 0644 "$deployment_dir/mycfc-pull-release.timer" /etc/systemd/system/mycfc-pull-release.timer
install -m 0644 "$deployment_dir/mycfc-postgres-backup.service" /etc/systemd/system/mycfc-postgres-backup.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup.timer" /etc/systemd/system/mycfc-postgres-backup.timer
install -m 0644 "$deployment_dir/mycfc-postgres-backup-version-cleanup.service" /etc/systemd/system/mycfc-postgres-backup-version-cleanup.service
install -m 0644 "$deployment_dir/mycfc-postgres-backup-version-cleanup.timer" /etc/systemd/system/mycfc-postgres-backup-version-cleanup.timer
install -m 0644 "$deployment_dir/mycfc-hetzner-backup-posture.service" /etc/systemd/system/mycfc-hetzner-backup-posture.service
install -m 0644 "$deployment_dir/mycfc-hetzner-backup-posture.timer" /etc/systemd/system/mycfc-hetzner-backup-posture.timer
install -m 0644 "$deployment_dir/mycfc-postgres-restore-drill.service" /etc/systemd/system/mycfc-postgres-restore-drill.service
install -m 0644 "$deployment_dir/mycfc-postgres-restore-drill.timer" /etc/systemd/system/mycfc-postgres-restore-drill.timer
install -m 0644 "$deployment_dir/mycfc-privacy-retention.service" /etc/systemd/system/mycfc-privacy-retention.service
install -m 0644 "$deployment_dir/mycfc-privacy-retention.timer" /etc/systemd/system/mycfc-privacy-retention.timer
install -m 0644 "$deployment_dir/mycfc-privacy-worker.service" /etc/systemd/system/mycfc-privacy-worker.service
systemctl daemon-reload
systemctl enable mycfc-pull-release.timer
systemctl enable --now mycfc-postgres-backup.timer
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
if [ "${PRIVACY_RESTORE_DRILL_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-postgres-restore-drill.timer
else
	systemctl disable --now mycfc-postgres-restore-drill.timer >/dev/null 2>&1 || true
fi
if [ "${PRIVACY_RETENTION_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-privacy-retention.timer
else
	systemctl disable --now mycfc-privacy-retention.timer >/dev/null 2>&1 || true
fi
if [ "${PRIVACY_WORKER_ENABLED:-false}" = true ]; then
	systemctl enable --now mycfc-privacy-worker.service
else
	systemctl disable --now mycfc-privacy-worker.service >/dev/null 2>&1 || true
fi
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" build caddy
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" up -d --no-deps --force-recreate caddy
systemctl start mycfc-pull-release.service
docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" up -d --no-deps cloudflared
systemctl start mycfc-pull-release.timer
