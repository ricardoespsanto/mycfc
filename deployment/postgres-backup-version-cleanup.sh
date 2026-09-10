#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
credentials_file=${MYCFC_BACKUP_CREDENTIALS_FILE:-/etc/mycfc/backup-aws/credentials}
work_dir=$(mktemp -d /var/tmp/mycfc-backup-version-cleanup.XXXXXX)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

read_setting() {
  setting_name=$1
  (
    set -a
    . "$env_file"
    set +a
    eval "printf '%s' \"\${$setting_name-}\""
  )
}

BACKUP_S3_BUCKET=${BACKUP_S3_BUCKET:-$(read_setting BACKUP_S3_BUCKET)}
AWS_REGION=${AWS_REGION:-$(read_setting AWS_REGION)}
BACKUP_NONCURRENT_CLEANER_ENABLED=${BACKUP_NONCURRENT_CLEANER_ENABLED:-$(read_setting BACKUP_NONCURRENT_CLEANER_ENABLED)}
BACKUP_NONCURRENT_CLEANER_DRY_RUN=${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-$(read_setting BACKUP_NONCURRENT_CLEANER_DRY_RUN)}
BACKUP_NONCURRENT_CLEANER_ENABLED=${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}
BACKUP_NONCURRENT_CLEANER_DRY_RUN=${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}
: "${BACKUP_S3_BUCKET:?set BACKUP_S3_BUCKET in the protected environment file}"

if [ "${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}" != true ]; then
  printf '%s\n' 'backup_noncurrent_cleanup_disabled' >&2
  exit 1
fi
case "${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}" in
  true|false) ;;
  *)
    printf '%s\n' 'BACKUP_NONCURRENT_CLEANER_DRY_RUN must be true or false.' >&2
    exit 1
    ;;
esac

export AWS_SHARED_CREDENTIALS_FILE="$credentials_file"
export AWS_PROFILE="${AWS_PROFILE:-mycfc-backup}"
export AWS_REGION="${AWS_REGION:-eu-west-1}"
export AWS_PAGER=""
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN

delete_age_seconds=${BACKUP_NONCURRENT_DELETE_AGE_SECONDS:-82800}
maximum_age_seconds=86400
case "$delete_age_seconds" in
  ''|*[!0-9]*)
    printf '%s\n' 'Backup version cleanup ages must be positive integer seconds.' >&2
    exit 1
    ;;
esac
if [ "$delete_age_seconds" -eq 0 ] || [ "$delete_age_seconds" -gt 82800 ]; then
  printf '%s\n' 'Backup version deletion must begin after zero and no later than the approved 23-hour boundary.' >&2
  exit 1
fi

log_event() {
  printf '%s\n' "$1"
  logger -t mycfc-backup-cleanup -- "$1"
}

list_versions() {
  aws s3api list-object-versions --bucket "$BACKUP_S3_BUCKET" --prefix "$1" --output json
}

delete_version() {
  key=$1
  version_id=$2
  if ! aws s3api delete-object --bucket "$BACKUP_S3_BUCKET" --key "$key" --version-id "$version_id" >/dev/null 2>"$work_dir/delete-error"; then
    log_event 'backup_noncurrent_cleanup_delete_failed'
    return 1
  fi
}

timestamp_filter='def epoch: sub("\\.[0-9]+(Z|\\+00:00)$"; "Z") | sub("\\+00:00$"; "Z") | fromdateiso8601;'
now_epoch=$(date -u +%s)
delete_cutoff=$((now_epoch - delete_age_seconds))
maximum_cutoff=$((now_epoch - maximum_age_seconds))

log_event 'backup_noncurrent_cleanup_started'
deleted=0
eligible=0
initial_overdue=0

for prefix in daily/ monthly/; do
  before="$work_dir/$(printf '%s' "$prefix" | tr / _)-before.json"
  candidates="$work_dir/$(printf '%s' "$prefix" | tr / _)-candidates.jsonl"
  list_versions "$prefix" >"$before"
  jq -c --argjson cutoff "$delete_cutoff" "$timestamp_filter
    [(.Versions // [])[], (.DeleteMarkers // [])[]]
    | .[]
    | select(.IsLatest == false)
    | select((.LastModified | epoch) <= \$cutoff)
    | {Key, VersionId}" "$before" >"$candidates"
  prefix_eligible=$(wc -l <"$candidates" | tr -d ' ')
  eligible=$((eligible + prefix_eligible))
  prefix_overdue=$(jq --argjson cutoff "$maximum_cutoff" "$timestamp_filter
    [
      ((.Versions // [])[] | select(.IsLatest == false)),
      (.DeleteMarkers // [])[]
      | select((.LastModified | epoch) <= \$cutoff)
    ] | length" "$before")
  initial_overdue=$((initial_overdue + prefix_overdue))
  if [ "$prefix_overdue" -ne 0 ]; then
    log_event "backup_noncurrent_cleanup_sla_breach_detected overdue_count=$prefix_overdue"
  fi

  if [ "$BACKUP_NONCURRENT_CLEANER_DRY_RUN" = true ]; then
    continue
  fi

  while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    key=$(printf '%s' "$candidate" | jq -r .Key)
    version_id=$(printf '%s' "$candidate" | jq -r .VersionId)
    delete_version "$key" "$version_id"
    deleted=$((deleted + 1))
  done <"$candidates"

  after_versions="$work_dir/$(printf '%s' "$prefix" | tr / _)-after-versions.json"
  orphan_markers="$work_dir/$(printf '%s' "$prefix" | tr / _)-orphan-markers.jsonl"
  list_versions "$prefix" >"$after_versions"
  jq -c --argjson cutoff "$delete_cutoff" "$timestamp_filter
    . as \$root
    | (\$root.DeleteMarkers // [])[]
    | select(.IsLatest == true)
    | select((.LastModified | epoch) <= \$cutoff)
    | . as \$marker
    | select(([((\$root.Versions // [])[]), ((\$root.DeleteMarkers // [])[])] | map(select(.Key == \$marker.Key))) | length == 1)
    | {Key, VersionId}" "$after_versions" >"$orphan_markers"

  while IFS= read -r marker; do
    [ -n "$marker" ] || continue
    key=$(printf '%s' "$marker" | jq -r .Key)
    version_id=$(printf '%s' "$marker" | jq -r .VersionId)
    delete_version "$key" "$version_id"
    deleted=$((deleted + 1))
  done <"$orphan_markers"

  verified="$work_dir/$(printf '%s' "$prefix" | tr / _)-verified.json"
  list_versions "$prefix" >"$verified"
  overdue=$(jq --argjson cutoff "$maximum_cutoff" "$timestamp_filter
    . as \$root
    |
    [
      (\$root.Versions // [])[]
      | select(.IsLatest == false)
      | select((.LastModified | epoch) <= \$cutoff)
    ] + [
      (\$root.DeleteMarkers // [])[]
      | select((.LastModified | epoch) <= \$cutoff)
    ] | length" "$verified")
  if [ "$overdue" -ne 0 ]; then
    log_event 'backup_noncurrent_cleanup_verification_failed'
    exit 1
  fi
done

if [ "$BACKUP_NONCURRENT_CLEANER_DRY_RUN" = true ]; then
  log_event "backup_noncurrent_cleanup_inventory eligible_count=$eligible overdue_count=$initial_overdue"
  if [ "$initial_overdue" -ne 0 ]; then
    log_event "backup_noncurrent_cleanup_sla_breached overdue_count=$initial_overdue"
    exit 1
  fi
  exit 0
fi

if [ "$initial_overdue" -ne 0 ]; then
  log_event "backup_noncurrent_cleanup_sla_breached overdue_count=$initial_overdue"
  exit 1
fi
log_event "backup_noncurrent_cleanup_succeeded deleted_count=$deleted"
