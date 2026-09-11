#!/bin/sh
set -eu

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

cat >"$test_dir/env" <<'EOF'
BACKUP_S3_BUCKET=test-backups
BACKUP_CLEANUP_ROLE_ARN=arn:aws:iam::123456789012:role/mycfc-production-postgres-backup-cleanup
BACKUP_NONCURRENT_CLEANER_ENABLED=true
BACKUP_NONCURRENT_CLEANER_DRY_RUN=false
EOF
: >"$test_dir/writer-credentials"
cat >"$test_dir/credentials" <<'EOF'
[mycfc-backup-cleanup]
aws_access_key_id=ASIATESTTEMPORARY
aws_secret_access_key=test-secret
aws_session_token=test-session-token
EOF
chmod 0600 "$test_dir/writer-credentials"
chmod 0600 "$test_dir/credentials"

candidate_time=$(date -u -d 'now - 23 hours - 30 minutes' +%Y-%m-%dT%H:%M:%SZ)
overdue_time=$(date -u -d '25 hours ago' +%Y-%m-%dT%H:%M:%SZ)
recent_successor_time=$(date -u -d '5 minutes ago' +%Y-%m-%dT%H:%M:%SZ)

cat >"$test_dir/before.json" <<EOF
{
  "Versions": [
    {"Key":"daily/current.dump.enc","VersionId":"current","IsLatest":true,"LastModified":"$overdue_time"},
    {"Key":"daily/secret-person-key","VersionId":"current-version","IsLatest":true,"LastModified":"$candidate_time"},
    {"Key":"daily/secret-person-key","VersionId":"old-version","IsLatest":false,"LastModified":"2000-01-01T00:00:00Z"},
    {"Key":"daily/recently-noncurrent.dump.enc","VersionId":"fresh-successor","IsLatest":true,"LastModified":"$recent_successor_time"},
    {"Key":"daily/recently-noncurrent.dump.enc","VersionId":"old-payload","IsLatest":false,"LastModified":"2000-01-01T00:00:00Z"},
    {"Key":"daily/old-marker.dump.enc","VersionId":"marker-successor","IsLatest":true,"LastModified":"2999-01-01T00:00:00Z"}
  ],
  "DeleteMarkers": [
    {"Key":"daily/old-marker.dump.enc","VersionId":"old-marker","IsLatest":false,"LastModified":"$candidate_time"}
  ]
}
EOF

cat >"$test_dir/overdue.json" <<EOF
{
  "Versions": [
    {"Key":"daily/overdue-secret-key","VersionId":"current-version","IsLatest":true,"LastModified":"$overdue_time"},
    {"Key":"daily/overdue-secret-key","VersionId":"overdue-version","IsLatest":false,"LastModified":"2000-01-01T00:00:00Z"}
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
test "$AWS_SHARED_CREDENTIALS_FILE" = "$EXPECTED_CLEANUP_CREDENTIALS_FILE"
test "$AWS_PROFILE" = mycfc-backup-cleanup
if [ "$1" = sts ]; then
  test "$2" = get-caller-identity
  if [ "${FAKE_STS_FAILURE:-false}" = true ]; then
    printf '%s\n' 'SECRET_AWS_STS_ERROR' >&2
    exit 9
  fi
  if [ "${FAKE_WRONG_ROLE:-false}" = true ]; then
    printf '%s\n' '{"Account":"123456789012","Arn":"arn:aws:sts::123456789012:assumed-role/unrelated-role/test"}'
  else
    printf '%s\n' '{"Account":"123456789012","Arn":"arn:aws:sts::123456789012:assumed-role/mycfc-production-postgres-backup-cleanup/test"}'
  fi
  exit 0
fi
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
    if [ "${FAKE_LIST_FAILURE:-false}" = true ]; then
      printf '%s\n' 'SECRET_AWS_OBJECT_KEY_IN_ERROR' >&2
      exit 7
    fi
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
  rm -f "$test_dir/daily_.count" "$test_dir/monthly_.count" "$test_dir/deleted" "$test_dir/result.log"
  PATH="$test_dir/bin:$PATH" \
    FAKE_STATE_DIR="$test_dir" \
    FAKE_PERSIST_OVERDUE="${FAKE_PERSIST_OVERDUE:-false}" \
    FAKE_INITIAL_OVERDUE="${FAKE_INITIAL_OVERDUE:-false}" \
    FAKE_LIST_FAILURE="${FAKE_LIST_FAILURE:-false}" \
    FAKE_STS_FAILURE="${FAKE_STS_FAILURE:-false}" \
    FAKE_WRONG_ROLE="${FAKE_WRONG_ROLE:-false}" \
    MYCFC_BACKUP_CLEANUP_ENV_FILE="$test_dir/env" \
    MYCFC_BACKUP_CREDENTIALS_FILE="$test_dir/writer-credentials" \
    MYCFC_BACKUP_CLEANUP_CREDENTIALS_FILE="$test_dir/credentials" \
    MYCFC_BACKUP_CLEANUP_AWS_PROFILE=mycfc-backup-cleanup \
    AWS_PROFILE=mycfc-backup \
    EXPECTED_CLEANUP_CREDENTIALS_FILE="$test_dir/credentials" \
    MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/result.log" \
    MYCFC_BACKUP_CLEANUP_INVOCATION_ID=0123456789abcdef0123456789abcdef \
    sh "$root_dir/deployment/postgres-backup-version-cleanup.sh"
}

output=$(run_cleanup)
printf '%s' "$output" | grep -q 'backup_noncurrent_cleanup_succeeded deleted_count=3'
test "$(wc -l <"$test_dir/deleted" | tr -d ' ')" -eq 3
grep -q 'secret-person-key|old-version' "$test_dir/deleted"
grep -q 'old-marker.dump.enc|old-marker' "$test_dir/deleted"
grep -q 'orphan-marker.dump.enc|orphan-marker' "$test_dir/deleted"
if grep -q 'recently-noncurrent.dump.enc' "$test_dir/deleted"; then
  printf '%s\n' 'cleanup aged a noncurrent version from its upload instead of its successor' >&2
  exit 1
fi

if FAKE_WRONG_ROLE=true run_cleanup >"$test_dir/wrong-role-output" 2>&1; then
  printf '%s\n' 'cleanup accepted credentials from the wrong role' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_identity_rejected' "$test_dir/wrong-role-output"
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
grep -q 'backup_noncurrent_cleanup_failed' "$test_dir/failure-output"

if FAKE_LIST_FAILURE=true run_cleanup >"$test_dir/unexpected-failure-output" 2>&1; then
  printf '%s\n' 'cleanup concealed an unexpected list failure' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_failed' "$test_dir/unexpected-failure-output"
if grep -q 'SECRET_AWS_OBJECT_KEY_IN_ERROR' "$test_dir/unexpected-failure-output" "$test_dir/result.log"; then
  printf '%s\n' 'cleanup leaked raw AWS stderr' >&2
  exit 1
fi

if FAKE_STS_FAILURE=true run_cleanup >"$test_dir/sts-failure-output" 2>&1; then
  printf '%s\n' 'cleanup accepted rejected temporary credentials' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_credentials_rejected' "$test_dir/sts-failure-output"
if grep -q 'SECRET_AWS_STS_ERROR' "$test_dir/sts-failure-output" "$test_dir/result.log"; then
  printf '%s\n' 'cleanup leaked raw STS stderr' >&2
  exit 1
fi

if FAKE_INITIAL_OVERDUE=true run_cleanup >"$test_dir/cleaned-breach-output" 2>&1; then
  printf '%s\n' 'cleanup concealed an initial SLA breach after successful deletion' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_sla_breached' "$test_dir/cleaned-breach-output"

if PATH="$test_dir/bin:$PATH" \
  MYCFC_BACKUP_CLEANUP_ENV_FILE="$test_dir/env" \
  MYCFC_BACKUP_CREDENTIALS_FILE="$test_dir/writer-credentials" \
  MYCFC_BACKUP_CLEANUP_CREDENTIALS_FILE="$test_dir/credentials" \
  MYCFC_BACKUP_CLEANUP_AWS_PROFILE=mycfc-backup-cleanup \
  AWS_PROFILE=mycfc-backup \
  EXPECTED_CLEANUP_CREDENTIALS_FILE="$test_dir/credentials" \
  MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/result.log" \
  MYCFC_BACKUP_CLEANUP_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  BACKUP_NONCURRENT_CLEANER_ENABLED=true \
  BACKUP_NONCURRENT_CLEANER_DRY_RUN=false \
  BACKUP_NONCURRENT_DELETE_AGE_SECONDS=86400 \
  sh "$root_dir/deployment/postgres-backup-version-cleanup.sh" >/dev/null 2>&1; then
  printf '%s\n' 'cleanup accepted a deletion age without a verification buffer' >&2
  exit 1
fi

cat >"$test_dir/standing-credentials" <<'EOF'
[mycfc-backup-cleanup]
aws_access_key_id=AKIATESTSTANDING
aws_secret_access_key=test-secret
EOF
chmod 0600 "$test_dir/standing-credentials"
if PATH="$test_dir/bin:$PATH" \
  MYCFC_BACKUP_CLEANUP_ENV_FILE="$test_dir/env" \
  MYCFC_BACKUP_CLEANUP_CREDENTIALS_FILE="$test_dir/standing-credentials" \
  MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/result.log" \
  MYCFC_BACKUP_CLEANUP_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  sh "$root_dir/deployment/postgres-backup-version-cleanup.sh" >"$test_dir/standing-output" 2>&1; then
  printf '%s\n' 'cleanup accepted a standing access key' >&2
  exit 1
fi
grep -q 'backup_noncurrent_cleanup_temporary_credentials_required' "$test_dir/standing-output"

cat >"$test_dir/safe-result.log" <<'EOF'
invocation_id=0123456789abcdef0123456789abcdef
backup_noncurrent_cleanup_started
backup_noncurrent_cleanup_inventory eligible_count=2 overdue_count=0
EOF
MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/safe-result.log" \
  MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  sh "$root_dir/deployment/postgres-backup-version-cleanup-cloudwatch.sh" >"$test_dir/sanitized-output"
grep -q 'eligible_count=2 overdue_count=0' "$test_dir/sanitized-output"
printf '%s\n' 'daily/private-object-key' >>"$test_dir/safe-result.log"
if MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/safe-result.log" \
  MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  sh "$root_dir/deployment/postgres-backup-version-cleanup-cloudwatch.sh" >"$test_dir/rejected-log-output" 2>&1; then
  printf '%s\n' 'CloudWatch sanitizer accepted non-allowlisted content' >&2
  exit 1
fi

cat >"$test_dir/stale-result.log" <<'EOF'
invocation_id=fedcba9876543210fedcba9876543210
backup_noncurrent_cleanup_succeeded deleted_count=1
EOF
if MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/stale-result.log" \
  MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  sh "$root_dir/deployment/postgres-backup-version-cleanup-cloudwatch.sh" >"$test_dir/stale-log-output" 2>&1; then
  printf '%s\n' 'CloudWatch sanitizer accepted a stale cleanup result' >&2
  exit 1
fi
grep -q '^backup_noncurrent_cleanup_log_stale$' "$test_dir/stale-log-output"
grep -q '^backup_noncurrent_cleanup_log_rejected$' "$test_dir/rejected-log-output"
if grep -q 'private-object-key' "$test_dir/rejected-log-output"; then
  printf '%s\n' 'CloudWatch sanitizer echoed rejected content' >&2
  exit 1
fi

printf '%s\n' 'invocation_id=0123456789abcdef0123456789abcdef' >"$test_dir/incomplete-result.log"
if MYCFC_BACKUP_CLEANUP_RESULT_FILE="$test_dir/incomplete-result.log" \
  MYCFC_BACKUP_CLEANUP_EXPECTED_INVOCATION_ID=0123456789abcdef0123456789abcdef \
  sh "$root_dir/deployment/postgres-backup-version-cleanup-cloudwatch.sh" >"$test_dir/incomplete-log-output" 2>&1; then
  printf '%s\n' 'CloudWatch sanitizer accepted an incomplete cleanup result' >&2
  exit 1
fi
grep -q '^backup_noncurrent_cleanup_log_rejected$' "$test_dir/incomplete-log-output"

grep -q '^DynamicUser=true$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
grep -q '^InaccessiblePaths=/etc/mycfc$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
grep -q '^LoadCredential=cleanup-env:/etc/mycfc/backup-cleanup.env$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
grep -q '^LoadCredential=aws-credentials:/etc/mycfc/backup-cleanup-aws/credentials$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
grep -q '^OnSuccess=mycfc-postgres-backup-version-cleanup-log.service$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
grep -q '^OnFailure=mycfc-postgres-backup-version-cleanup-log.service$' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"
if grep -q 'run-with-cloudwatch-logs.sh' "$root_dir/deployment/mycfc-postgres-backup-version-cleanup.service"; then
  printf '%s\n' 'cleanup service still runs with release logging credentials' >&2
  exit 1
fi

printf '%s\n' 'postgres backup version cleanup tests passed'
