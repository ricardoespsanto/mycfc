#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
state_dir=${MYCFC_STATE_DIR:-/etc/mycfc/deployment}
mode=${1:-}

fail() {
	printf '%s\n' 'privacy_production_config_failed' >&2
	exit 1
}

if [ "$(id -u 2>/dev/null || true)" != 0 ] || [ "$#" -ne 1 ] ||
	[ ! -f "$env_file" ] || [ -L "$env_file" ] ||
	[ "$(stat -c '%u:%g:%a' "$env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	fail
fi
case "$mode" in
	flags-enable | flags-disable | worker-enable | worker-disable | retention-enable | retention-disable) ;;
	*) fail ;;
esac

set_value() {
	key=$1
	value=$2
	count=$(grep -c "^${key}=" "$env_file" 2>/dev/null || true)
	[ "$count" -le 1 ] || fail
	temporary=$(mktemp "$(dirname -- "$env_file")/.mycfc.env.XXXXXX")
	trap 'rm -f "$temporary"' EXIT HUP INT TERM
	awk -v key="$key" -v value="$value" '
		BEGIN { found=0 }
		index($0,key "=")==1 { if (!found) print key "=" value; found=1; next }
		{ print }
		END { if (!found) print key "=" value }
	' "$env_file" >"$temporary"
	chown root:root "$temporary"
	chmod 0600 "$temporary"
	mv -f "$temporary" "$env_file"
	trap - EXIT HUP INT TERM
}

read_value() {
	key=$1
	awk -F= -v key="$key" '$1==key { print substr($0,length(key)+2); exit }' "$env_file"
}

active_slot=$(cat "$state_dir/active-slot" 2>/dev/null || true)
case "$active_slot" in blue | green) ;; *) fail ;; esac

recreate_application() {
	service=app-$active_slot
	container=mycfc-production-$service-1
	if ! docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile "$active_slot" \
		up -d --no-deps --force-recreate "$service" >/dev/null; then
		return 1
	fi
	ready=false
	for _ in $(seq 1 30); do
		address=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$container" 2>/dev/null || true)
		if [ -n "$address" ] && curl -fsS -o /dev/null "http://$address:8080/health/ready"; then
			ready=true
			break
		fi
		sleep 2
	done
	[ "$ready" = true ]
}

case "$mode" in
	flags-enable)
		[ "$(read_value PRIVACY_WORKER_ENABLED)" != true ] || fail
		if systemctl is-active --quiet mycfc-privacy-worker.service; then fail; fi
		set_value PRIVACY_REQUESTS_ENABLED true
		set_value PRIVACY_COMPLETION_ENABLED true
		set_value PRIVACY_WORKER_ENABLED true
		if ! "$deployment_dir/privacy-worker.sh" readiness >/dev/null; then
			set_value PRIVACY_WORKER_ENABLED false
			set_value PRIVACY_COMPLETION_ENABLED false
			set_value PRIVACY_REQUESTS_ENABLED false
			fail
		fi
		set_value PRIVACY_WORKER_ENABLED false
		if ! recreate_application; then
			set_value PRIVACY_COMPLETION_ENABLED false
			set_value PRIVACY_REQUESTS_ENABLED false
			recreate_application >/dev/null 2>&1 || true
			fail
		fi
		;;
	flags-disable)
		if systemctl is-active --quiet mycfc-privacy-worker.service; then fail; fi
		set_value PRIVACY_WORKER_ENABLED false
		set_value PRIVACY_COMPLETION_ENABLED false
		set_value PRIVACY_REQUESTS_ENABLED false
		recreate_application || fail
		;;
	worker-enable)
		[ "$(read_value PRIVACY_REQUESTS_ENABLED)" = true ] || fail
		[ "$(read_value PRIVACY_COMPLETION_ENABLED)" = true ] || fail
		set_value PRIVACY_WORKER_ENABLED true
		if ! "$deployment_dir/privacy-worker.sh" readiness >/dev/null; then
			set_value PRIVACY_WORKER_ENABLED false
			fail
		fi
		if ! systemctl enable --now mycfc-privacy-worker.service >/dev/null ||
			! systemctl is-active --quiet mycfc-privacy-worker.service; then
			systemctl disable --now mycfc-privacy-worker.service >/dev/null 2>&1 || true
			set_value PRIVACY_WORKER_ENABLED false
			fail
		fi
		;;
	worker-disable)
		systemctl disable --now mycfc-privacy-worker.service >/dev/null 2>&1 || true
		set_value PRIVACY_WORKER_ENABLED false
		;;
	retention-enable)
		set_value PRIVACY_RETENTION_ENABLED true
		if ! "$deployment_dir/privacy-retention.sh" run >/dev/null; then
			set_value PRIVACY_RETENTION_ENABLED false
			fail
		fi
		if ! systemctl enable --now mycfc-privacy-retention.timer >/dev/null; then
			systemctl disable --now mycfc-privacy-retention.timer >/dev/null 2>&1 || true
			set_value PRIVACY_RETENTION_ENABLED false
			fail
		fi
		;;
	retention-disable)
		systemctl disable --now mycfc-privacy-retention.timer >/dev/null 2>&1 || true
		set_value PRIVACY_RETENTION_ENABLED false
		;;
esac

printf '%s\n' "privacy_production_config_succeeded mode=$mode"
