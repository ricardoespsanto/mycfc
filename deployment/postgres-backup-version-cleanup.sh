#!/bin/sh
set -eu

credential_directory=${CREDENTIALS_DIRECTORY:-}
if [ -n "$credential_directory" ]; then
  env_file=${MYCFC_BACKUP_CLEANUP_ENV_FILE:-$credential_directory/cleanup-env}
  credentials_file=${MYCFC_BACKUP_CLEANUP_CREDENTIALS_FILE:-$credential_directory/aws-credentials}
else
  env_file=${MYCFC_BACKUP_CLEANUP_ENV_FILE:-/etc/mycfc/backup-cleanup.env}
  credentials_file=${MYCFC_BACKUP_CLEANUP_CREDENTIALS_FILE:-/etc/mycfc/backup-cleanup-aws/credentials}
fi
result_file=${MYCFC_BACKUP_CLEANUP_RESULT_FILE:-${RUNTIME_DIRECTORY:-/run/mycfc-backup-cleanup}/result.log}
invocation_id=${MYCFC_BACKUP_CLEANUP_INVOCATION_ID:-${INVOCATION_ID:-}}
work_dir=
result_ready=false

log_event() {
  printf '%s\n' "$1"
  if [ "$result_ready" = true ]; then
    printf '%s\n' "$1" >>"$result_file"
  fi
}

on_exit() {
  status=$?
  if [ "$status" -ne 0 ]; then
    log_event 'backup_noncurrent_cleanup_failed'
  fi
  [ -z "$work_dir" ] || rm -rf "$work_dir"
  exit "$status"
}
trap on_exit EXIT
trap 'exit 1' HUP INT TERM

work_dir=$(mktemp -d /var/tmp/mycfc-backup-version-cleanup.XXXXXX)
if ! printf '%s' "$invocation_id" | grep -Eq '^[0-9a-f]{32}$'; then
  printf '%s\n' 'backup_noncurrent_cleanup_invocation_rejected' >&2
  exit 1
fi
printf 'invocation_id=%s\n' "$invocation_id" >"$result_file"
result_ready=true

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
BACKUP_CLEANUP_ROLE_ARN=${BACKUP_CLEANUP_ROLE_ARN:-$(read_setting BACKUP_CLEANUP_ROLE_ARN)}
BACKUP_NONCURRENT_CLEANER_ENABLED=${BACKUP_NONCURRENT_CLEANER_ENABLED:-$(read_setting BACKUP_NONCURRENT_CLEANER_ENABLED)}
BACKUP_NONCURRENT_CLEANER_DRY_RUN=${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-$(read_setting BACKUP_NONCURRENT_CLEANER_DRY_RUN)}
BACKUP_NONCURRENT_CLEANER_ENABLED=${BACKUP_NONCURRENT_CLEANER_ENABLED:-false}
BACKUP_NONCURRENT_CLEANER_DRY_RUN=${BACKUP_NONCURRENT_CLEANER_DRY_RUN:-true}
: "${BACKUP_S3_BUCKET:?set BACKUP_S3_BUCKET in the protected environment file}"
: "${BACKUP_CLEANUP_ROLE_ARN:?set BACKUP_CLEANUP_ROLE_ARN in the protected cleanup environment file}"

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
export AWS_PROFILE="${MYCFC_BACKUP_CLEANUP_AWS_PROFILE:-mycfc-backup-cleanup}"
export AWS_REGION="${AWS_REGION:-eu-west-1}"
export AWS_PAGER=""
export AWS_EC2_METADATA_DISABLED=true
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_CONFIG_FILE AWS_ROLE_ARN AWS_WEB_IDENTITY_TOKEN_FILE AWS_CONTAINER_CREDENTIALS_FULL_URI AWS_CONTAINER_CREDENTIALS_RELATIVE_URI

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
' "$credentials_file"; then
  printf '%s\n' 'backup_noncurrent_cleanup_temporary_credentials_required' >&2
  exit 1
fi

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

list_versions() {
  prefix=$1
  destination=$2
  if ! aws s3api list-object-versions --bucket "$BACKUP_S3_BUCKET" --prefix "$prefix" --output json >"$destination" 2>"$work_dir/aws-error"; then
    log_event 'backup_noncurrent_cleanup_inventory_failed'
    return 1
  fi
}

delete_version() {
  key=$1
  version_id=$2
  if ! aws s3api delete-object --bucket "$BACKUP_S3_BUCKET" --key "$key" --version-id "$version_id" >/dev/null 2>"$work_dir/delete-error"; then
    log_event 'backup_noncurrent_cleanup_delete_failed'
    return 1
  fi
}

inventory_filter='def epoch: sub("\\.[0-9]+(Z|\\+00:00)$"; "Z") | sub("\\+00:00$"; "Z") | fromdateiso8601;
def history:
  [((.Versions // [])[] | . + {EntryKind:"VERSION"}), ((.DeleteMarkers // [])[] | . + {EntryKind:"DELETE_MARKER"})]
  | group_by(.Key)[]
  | sort_by(.LastModified | epoch) as $entries
  | range(0; $entries | length) as $index
  | $entries[$index] + {SuccessorModified:(if $index + 1 < ($entries | length) then $entries[$index + 1].LastModified else null end)};
def deletion_candidates($cutoff):
  history
  | select(
      (.EntryKind == "VERSION" and .IsLatest == false and .SuccessorModified != null and (.SuccessorModified | epoch) <= $cutoff)
      or (.EntryKind == "DELETE_MARKER" and .IsLatest == false and (.LastModified | epoch) <= $cutoff)
    );
def retention_breaches($cutoff):
  history
  | select(
      (.EntryKind == "VERSION" and .IsLatest == false and .SuccessorModified != null and (.SuccessorModified | epoch) <= $cutoff)
      or (.EntryKind == "DELETE_MARKER" and (.LastModified | epoch) <= $cutoff)
    );'
now_epoch=$(date -u +%s)
delete_cutoff=$((now_epoch - delete_age_seconds))
maximum_cutoff=$((now_epoch - maximum_age_seconds))

if ! aws sts get-caller-identity --output json >"$work_dir/caller-identity.json" 2>"$work_dir/aws-error"; then
  log_event 'backup_noncurrent_cleanup_credentials_rejected'
  exit 1
fi
expected_assumed_prefix=$(printf '%s' "$BACKUP_CLEANUP_ROLE_ARN" | sed -E 's#^arn:([^:]+):iam::([0-9]{12}):role/#arn:\1:sts::\2:assumed-role/#')
caller_arn=$(jq -r '.Arn // ""' "$work_dir/caller-identity.json")
case "$caller_arn" in
  "$expected_assumed_prefix"/*) ;;
  *)
    log_event 'backup_noncurrent_cleanup_identity_rejected'
    exit 1
    ;;
esac

log_event 'backup_noncurrent_cleanup_started'
deleted=0
eligible=0
initial_overdue=0

for prefix in daily/ monthly/; do
  before="$work_dir/$(printf '%s' "$prefix" | tr / _)-before.json"
  candidates="$work_dir/$(printf '%s' "$prefix" | tr / _)-candidates.jsonl"
  list_versions "$prefix" "$before"
  jq -c --argjson cutoff "$delete_cutoff" "$inventory_filter deletion_candidates(\$cutoff) | {Key, VersionId}" "$before" >"$candidates"
  prefix_eligible=$(wc -l <"$candidates" | tr -d ' ')
  eligible=$((eligible + prefix_eligible))
  prefix_overdue=$(jq --argjson cutoff "$maximum_cutoff" "$inventory_filter [retention_breaches(\$cutoff)] | length" "$before")
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
  list_versions "$prefix" "$after_versions"
  jq -c --argjson cutoff "$delete_cutoff" "$inventory_filter
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
  list_versions "$prefix" "$verified"
  overdue=$(jq --argjson cutoff "$maximum_cutoff" "$inventory_filter [retention_breaches(\$cutoff)] | length" "$verified")
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
