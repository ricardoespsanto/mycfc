#!/bin/sh
set -eu

unit_dir=${MYCFC_SYSTEMD_UNIT_DIR:-/etc/systemd/system}
report_dir=${MYCFC_RETIREMENT_REPORT_DIR:-/var/lib/mycfc/privacy-automation-retirement}

[ "$(id -u)" -eq 0 ] || { printf '%s\n' 'Run as root.' >&2; exit 1; }

units='mycfc-privacy-worker.service
mycfc-privacy-retention.service
mycfc-privacy-retention.timer
mycfc-privacy-activation-collector.service
mycfc-privacy-activation-collector.timer
mycfc-privacy-production-operation.service
mycfc-privacy-production-operation.timer
mycfc-postgres-restore-drill.service
mycfc-postgres-restore-drill.timer'

printf '%s\n' "$units" | while IFS= read -r unit; do
	[ -n "$unit" ] || continue
	if [ -e "$unit_dir/$unit" ]; then
		systemctl disable --now "$unit" >/dev/null
	else
		systemctl disable --now "$unit" >/dev/null 2>&1 || true
	fi
	rm -f "$unit_dir/$unit"
done

for container in \
	mycfc-production-privacy-worker-1 \
	mycfc-production-privacy-retention-1 \
	mycfc-production-privacy-activation-1 \
	mycfc-production-privacy-activation-prepare-1 \
	mycfc-production-privacy-activation-approval-1 \
	mycfc-production-privacy-activation-exchange-1 \
	mycfc-production-privacy-activation-disable-1 \
	mycfc-production-privacy-activation-disable-bootstrap-1 \
	mycfc-production-privacy-acceptance-1 \
	mycfc-production-privacy-policy-import-1; do
	if docker inspect "$container" >/dev/null 2>&1; then
		docker rm -f "$container" >/dev/null
	fi
done

retired_services='privacy-worker privacy-retention privacy-activation privacy-activation-prepare privacy-activation-approval
privacy-activation-exchange privacy-activation-disable privacy-activation-disable-bootstrap privacy-acceptance privacy-policy-import'
for service in $retired_services; do
	container_ids=$(docker ps -aq \
		--filter label=com.docker.compose.project=mycfc-production \
		--filter "label=com.docker.compose.service=$service")
	for container_id in $container_ids; do
		docker rm -f "$container_id" >/dev/null
	done
	remaining=$(docker ps -aq \
		--filter label=com.docker.compose.project=mycfc-production \
		--filter "label=com.docker.compose.service=$service")
	if [ -n "$remaining" ]; then
		printf '%s\n' "retired privacy container remains for service: $service" >&2
		exit 1
	fi
done

systemctl daemon-reload
printf '%s\n' "$units" | while IFS= read -r unit; do
	[ -n "$unit" ] || continue
	if systemctl is-active --quiet "$unit" >/dev/null 2>&1; then
		printf '%s\n' "retired unit remains active: $unit" >&2
		exit 1
	fi
done
install -d -o root -g root -m 0700 "$report_dir"
temporary=$(mktemp "$report_dir/.report.XXXXXX")
{
	printf 'retired_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	printf 'credential_paths_for_separate_review=%s\n' '/etc/mycfc/privacy-worker.env,/etc/mycfc/privacy-activation.env,/etc/mycfc/privacy-retention.env,/etc/mycfc/privacy-activation-disable.env,/etc/mycfc/privacy-acceptance.env,/etc/mycfc/privacy-operation-receipt.env,/etc/mycfc/privacy-restore,/etc/mycfc/legacy-media-purge.env'
	printf 'state_paths_for_separate_review=%s\n' '/var/lib/mycfc/privacy-operations,/var/lib/mycfc/privacy-activation-exchange,/var/lib/mycfc/privacy-restore,/var/lib/mycfc/legacy-media-purge'
} >"$temporary"
chmod 0600 "$temporary"
mv "$temporary" "$report_dir/latest.env"
printf '%s\n' "privacy automation retired; review $report_dir/latest.env before separately approved credential or state deletion"
