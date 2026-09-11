#!/bin/sh
set -eu

result_file=${MYCFC_BACKUP_CLEANUP_RESULT_FILE:-/run/mycfc-backup-cleanup/result.log}
expected_invocation_id=${MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID:-${MONITOR_INVOCATION_ID:-}}

if [ -z "${MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID:-}" ] && [ "${MONITOR_UNIT:-}" != mycfc-postgres-backup-version-cleanup.service ]; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_identity_failed' >&2
	exit 1
fi

if ! printf '%s' "$expected_invocation_id" | grep -Eq '^[0-9a-f]{32}$'; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_identity_failed' >&2
	exit 1
fi

if [ ! -f "$result_file" ] || [ -L "$result_file" ]; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_missing' >&2
	exit 1
fi

if [ "$(wc -c <"$result_file" | tr -d ' ')" -gt 4096 ]; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_rejected' >&2
	exit 1
fi

recorded_invocation_id=$(sed -n '1s/^invocation_id=//p' "$result_file")
if [ "$recorded_invocation_id" != "$expected_invocation_id" ]; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_stale' >&2
	exit 1
fi

if ! tail -n +2 "$result_file" | awk '
  /^backup_noncurrent_cleanup_(started|failed|inventory_failed|credentials_rejected|identity_rejected|delete_failed|verification_failed)$/ { count++; last = $0; next }
  /^backup_noncurrent_cleanup_inventory eligible_count=[0-9]+ overdue_count=[0-9]+$/ { count++; last = $0; next }
  /^backup_noncurrent_cleanup_(sla_breach_detected|sla_breached) overdue_count=[0-9]+$/ { count++; last = $0; next }
  /^backup_noncurrent_cleanup_succeeded deleted_count=[0-9]+$/ { count++; last = $0; next }
  { invalid = 1 }
  END {
    terminal = (last == "backup_noncurrent_cleanup_failed" || last ~ /^backup_noncurrent_cleanup_inventory / || last ~ /^backup_noncurrent_cleanup_succeeded /)
    exit (invalid || count == 0 || !terminal)
  }
'; then
	printf '%s\n' 'backup_noncurrent_cleanup_log_rejected' >&2
	exit 1
fi

tail -n +2 "$result_file"
