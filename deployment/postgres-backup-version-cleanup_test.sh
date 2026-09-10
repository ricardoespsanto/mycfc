#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

cat >"$test_dir/env" <<'EOF'
BACKUP_S3_BUCKET=test-backups
BACKUP_NONCURRENT_CLEANER_ENABLED=true
BACKUP_NONCURRENT_CLEANER_DRY_RUN=false
EOF
: >"$test_dir/credentials"

candidate_time=$(date -u -d 'now - 23 hours - 30 minutes' +%Y-%m-%dT%H:%M:%SZ)
overdue_time=$(date -u -d '25 hours ago' +%Y-%m-%dT%H:%M:%SZ)

cat >"$test_dir/before.json" <<EOF
{
  "Versions": [
    {"Key":"daily/current.dump.enc","VersionId":"current","IsLatest":true,"LastModified":"$overdue_time"},
    {"Key":"daily/secret-person-key","VersionId":"old-version","IsLatest":false,"LastModified":"$candidate_time"},
    {"Key":"daily/new-version.dump.enc","VersionId":"new-version","IsLatest":false,"LastModified":"2999-01-01T00:00:00Z"}
  ],
  "DeleteMarkers": [
    {"Key":"daily/old-marker.dump.enc","VersionId":"old-marker","IsLatest":false,"LastModified":"$candidate_time"}
  ]
}
EOF

cat >"$test_dir/overdue.json" <<EOF
{
  "Versions": [
    {"Key":"daily/overdue-secret-key","VersionId":"overdue-version","IsLatest":false,"LastModified":"$overdue_time"}
  ],
  "DeleteMarkers": []
}
EOF

cat >"$test_dir/orphan.json" <<EOF
{
  "Versions": [],
  "DeleteMarkers": [
    {"Key":"daily/orphan-marker.dump.enc","VersionId":"orphan-marker","IsLatest":true,"LastModified":"$candidate_time"}
  ]
}
EOF

cat >"$test_dir/empty.json" <<'EOF'
{"Versions":[],"DeleteMarkers":[]}
EOF

cat >"$test_dir/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$test_dir/bin/logger"

cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
set -eu
test "$1" = s3api
operation=$2
shift 2
prefix=
key=
version_id=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --prefix) prefix=$2; shift 2 ;;
    --key) key=$2; shift 2 ;;
    --version-id) version_id=$2; shift 2 ;;
    *) shift ;;
  esac
done
case "$operation" in
  list-object-versions)
    count_file="$FAKE_STATE_DIR/$(printf '%s' "$prefix" | tr / _).count"
    count=0
    [ ! -f "$count_file" ] || count=$(cat "$count_file")
    count=$((count + 1))
    printf '%s' "$count" >"$count_file"
    if [ "${FAKE_PERSIST_OVERDUE:-false}" = true ] && [ "$prefix" = daily/ ]; then
      cat "$FAKE_STATE_DIR/overdue.json"
    elif [ "${FAKE_INITIAL_OVERDUE:-false}" = true ] && [ "$prefix" = daily/ ] && [ "$count" -eq 1 ]; then
      cat "$FAKE_STATE_DIR/overdue.json"
    elif [ "$prefix" = daily/ ] && [ "$count" -eq 1 ]; then
      cat "$FAKE_STATE_DIR/before.json"
    elif [ "$prefix" = daily/ ] && [ "$count" -eq 2 ]; then
      cat "$FAKE_STATE_DIR/orphan.json"
    else
      cat "$FAKE_STATE_DIR/empty.json"
    fi
    ;;
  delete-object)
    printf '%s|%s\n' "$key" "$version_id" >>"$FAKE_STATE_DIR/deleted"
    printf '%s\n' '{}'
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$test_dir/bin/aws"

run_cleanup() {
  rm -f "$test_dir/daily_.count" "$test_dir/monthly_.count" "$test_dir/deleted"
  PATH="$test_dir/bin:$PATH" \
    FAKE_STATE_DIR="$test_dir" \
    FAKE_PERSIST_OVERDUE="${FAKE_PERSIST_OVERDUE:-false}" \
    FAKE_INITIAL_OVERDUE="${FAKE_INITIAL_OVERDUE:-false}" \
    MYCFC_ENV_FILE="$test_dir/env" \
    MYCFC_BACKUP_CREDENTIALS_FILE="$test_dir/credentials" \
    sh "$root_dir/deployment/postgres-backup-version-cleanup.sh"
}

output=$(run_cleanup)
printf '%s' "$output" | grep -q 'backup_noncurrent_cleanup_succeeded deleted_count=3'
test "$(wc -l <"$test_dir/deleted" | tr -d ' ')" -eq 3
grep -q 'secret-person-key|old-version' "$test_dir/deleted"
grep -q 'old-marker.dump.enc|old-marker' "$test_dir/deleted"
grep -q 'orphan-marker.dump.enc|orphan-marker' "$test_dir/deleted"
if printf '%s' "$output" | grep -q 'secret-person-key'; then
  printf '%s\n' 'cleanup output leaked an object key' >&2
  exit 1
fi

rm -f "$test_dir/deleted"
dry_output=$(BACKUP_NONCURRENT_CLEANER_DRY_RUN=true run_cleanup)
printf '%s' "$dry_output" | grep -q 'backup_noncurrent_cleanup_inventory eligible_count=2 overdue_count=0'
test ! -f "$test_dir/deleted"

if BACKUP_NONCURRENT_CLEANER_ENABLED=false run_cleanup >/dev/null 2>&1; then
  printf '%s\n' 'cleanup ran while its executable gate was disabled' >&2
  exit 1
fi

if FAKE_PERSIST_OVERDUE=true run_cleanup >"$test_dir/failure-output" 2>&1; then
  printf '%s\n' 'cleanup unexpectedly accepted an overdue version after deletion' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_verification_failed' "$test_dir/failure-output"
grep -q 'backup_noncurrent_cleanup_sla_breach_detected' "$test_dir/failure-output"

if FAKE_INITIAL_OVERDUE=true run_cleanup >"$test_dir/cleaned-breach-output" 2>&1; then
  printf '%s\n' 'cleanup concealed an initial SLA breach after successful deletion' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_sla_breached' "$test_dir/cleaned-breach-output"

if PATH="$test_dir/bin:$PATH" \
  MYCFC_ENV_FILE="$test_dir/env" \
  MYCFC_BACKUP_CREDENTIALS_FILE="$test_dir/credentials" \
  BACKUP_NONCURRENT_CLEANER_ENABLED=true \
  BACKUP_NONCURRENT_CLEANER_DRY_RUN=false \
  BACKUP_NONCURRENT_DELETE_AGE_SECONDS=86400 \
  sh "$root_dir/deployment/postgres-backup-version-cleanup.sh" >/dev/null 2>&1; then
  printf '%s\n' 'cleanup accepted a deletion age without a verification buffer' >&2
  exit 1
fi

printf '%s\n' 'postgres backup version cleanup tests passed'
