#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
fake_bin="$work_dir/bin"
mkdir -p "$fake_bin"

cat >"$fake_bin/id" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$fake_bin/stat" <<'EOF'
#!/bin/sh
printf '0:600\n'
EOF
cat >"$fake_bin/aws" <<'EOF'
#!/bin/sh
if [ -n "${AWS_ACCESS_KEY_ID:-}" ] || [ -n "${AWS_SECRET_ACCESS_KEY:-}" ] || [ -n "${AWS_SESSION_TOKEN:-}" ]; then
	printf '%s\n' 'application AWS credentials reached release status' >&2
	exit 1
fi
printf '%s|%s\n' "${AWS_PROFILE:-}" "${AWS_SHARED_CREDENTIALS_FILE:-}" >>"$TEST_AWS_LOG"
case "$*" in
	*imageDetails*imageTags*) printf 'release-20260811090000-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
	*imageDigest*) printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
	*) printf 'unexpected aws invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
case "$*" in
	*Config.Image*) printf 'registry.example/mycfc@%s\n' "$TEST_RUNNING_DIGEST" ;;
	*org.opencontainers.image.revision*) printf '%s\n' "$TEST_RUNNING_SHA" ;;
	*) exit 1 ;;
esac
EOF
cat >"$fake_bin/systemctl" <<'EOF'
#!/bin/sh
case "$*" in
	*--property=Result*) printf '%s\n' "$TEST_AGENT_RESULT" ;;
	*--property=ExecMainStatus*) printf '%s\n' "$TEST_AGENT_EXIT_STATUS" ;;
	*--property=ExecMainExitTimestamp*) printf 'Tue 2026-08-11 09:02:00 UTC\n' ;;
	*) exit 1 ;;
esac
EOF
cat >"$fake_bin/date" <<'EOF'
#!/bin/sh
case "$*" in
	*-d*2026-08-11T09:00:30Z*) printf '1030\n' ;;
	*-d*2026-08-11T09:00:35Z*) printf '1035\n' ;;
	*-d*2026-08-11T09:00:40Z*) printf '1040\n' ;;
	*-d*2026-08-11T09:00:50Z*) printf '1050\n' ;;
	*-d*2026-08-11T09:00:55Z*) printf '1055\n' ;;
	*-d*2026-08-11T09:01:00Z*) printf '1060\n' ;;
	*-d*2026-08-11T09:01:05Z*) printf '1065\n' ;;
	*-d*) printf '1000\n' ;;
	*) printf '%s\n' "$TEST_NOW_EPOCH" ;;
esac
EOF
chmod +x "$fake_bin"/*

case_dir="$work_dir/case"
mkdir -p "$case_dir/state" "$case_dir/release-aws"
cat >"$case_dir/mycfc.env" <<'EOF'
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=application-key
AWS_SECRET_ACCESS_KEY=application-secret
ECR_REPOSITORY_URL=registry.example/mycfc
EOF
chmod 0600 "$case_dir/mycfc.env"
: >"$case_dir/release-aws/credentials"
chmod 0600 "$case_dir/release-aws/credentials"
printf 'blue\n' >"$case_dir/state/active-slot"
printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-digest"
printf 'release-20260811090000-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-tag"
printf '2026-08-11T09:00:30Z\n' >"$case_dir/state/release-agent-started-at"
printf '2026-08-11T09:00:35Z\n' >"$case_dir/state/release-detected-at"
printf '2026-08-11T09:00:40Z\n' >"$case_dir/state/release-image-pulled-at"
printf '2026-08-11T09:00:50Z\n' >"$case_dir/state/release-migration-completed-at"
printf '2026-08-11T09:00:55Z\n' >"$case_dir/state/release-candidate-ready-at"
printf '2026-08-11T09:01:00Z\n' >"$case_dir/state/release-traffic-switched-at"
printf '2026-08-11T09:01:05Z\n' >"$case_dir/state/release-deployment-completed-at"
: >"$case_dir/aws.log"

run_status() {
	env \
		PATH="$fake_bin:$PATH" \
		TEST_AWS_LOG="$case_dir/aws.log" \
		MYCFC_ENV_FILE="$case_dir/mycfc.env" \
		MYCFC_DEPLOYMENT_STATE_DIR="$case_dir/state" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$case_dir/release-aws/credentials" \
		TEST_RUNNING_SHA="${TEST_RUNNING_SHA:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}" \
		TEST_RUNNING_DIGEST="${TEST_RUNNING_DIGEST:-sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}" \
		TEST_AGENT_RESULT="${TEST_AGENT_RESULT:-success}" \
		TEST_AGENT_EXIT_STATUS="${TEST_AGENT_EXIT_STATUS:-0}" \
		TEST_NOW_EPOCH="${TEST_NOW_EPOCH:-1100}" \
		sh "$deployment_dir/release-status.sh"
}

current_output=$(TEST_RUNNING_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb TEST_RUNNING_DIGEST=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run_status)
printf '%s\n' "$current_output" | grep -q '^state=current$'
printf '%s\n' "$current_output" | grep -q '^active_slot=blue$'
printf '%s\n' "$current_output" | grep -q '^running_release_sha=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb$'
printf '%s\n' "$current_output" | grep -q '^release_published_at=2026-08-11T09:00:00Z$'
printf '%s\n' "$current_output" | grep -q '^publication_to_agent_start_seconds=30$'
printf '%s\n' "$current_output" | grep -q '^publication_to_detection_seconds=35$'
printf '%s\n' "$current_output" | grep -q '^publication_to_traffic_switch_seconds=60$'
printf '%s\n' "$current_output" | grep -q '^publication_to_deployment_seconds=65$'

pending_output=$(TEST_NOW_EPOCH=1100 run_status)
printf '%s\n' "$pending_output" | grep -q '^state=pending$'

printf 'release-20260810090000-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-tag"
delayed_output=$(TEST_NOW_EPOCH=1100 run_status)
printf '%s\n' "$delayed_output" | grep -q '^state=delayed$'
printf 'release-20260811090000-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-tag"

printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/failed-release-digest"
quarantined_output=$(TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$quarantined_output" | grep -q '^state=quarantined$'
rm "$case_dir/state/failed-release-digest"

failed_output=$(TEST_AGENT_RESULT=failed TEST_AGENT_EXIT_STATUS=1 TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$failed_output" | grep -q '^state=failed$'

printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\tchecking\t2026-08-11T09:00:35Z\n' >"$case_dir/state/last-attempt"
checking_failure_output=$(TEST_AGENT_RESULT=failed TEST_AGENT_EXIT_STATUS=1 TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$checking_failure_output" | grep -q '^state=failed$'

printf 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\tfailed\t2026-08-10T09:00:35Z\n' >"$case_dir/state/last-attempt"
printf 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' >"$case_dir/state/release-timeline-digest"
printf 'release-20260810090000-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' >"$case_dir/state/release-timeline-tag"
stale_failure_output=$(TEST_AGENT_RESULT=failed TEST_AGENT_EXIT_STATUS=1 TEST_NOW_EPOCH=1100 run_status)
printf '%s\n' "$stale_failure_output" | grep -q '^state=delayed$'
printf '%s\n' "$stale_failure_output" | grep -q '^agent_started_at=unknown$'
printf '%s\n' "$stale_failure_output" | grep -q '^publication_to_traffic_switch_seconds=unknown$'

# Legacy fields can be mid-update during a digest rollover. Once the atomic
# record exists, its digest/result/time association is authoritative.
printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\tchecking\t2026-08-11T09:00:35Z\n' >"$case_dir/state/last-attempt"
printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/last-attempt-digest"
printf 'failed\n' >"$case_dir/state/last-attempt-result"
printf '2026-08-10T09:00:35Z\n' >"$case_dir/state/last-attempt-at"
printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-digest"
printf 'release-20260811090000-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/release-timeline-tag"
rollover_output=$(TEST_AGENT_RESULT=success TEST_AGENT_EXIT_STATUS=0 TEST_NOW_EPOCH=1100 run_status)
printf '%s\n' "$rollover_output" | grep -q '^state=pending$'
printf '%s\n' "$rollover_output" | grep -q '^last_attempt_result=checking$'

grep -q "^mycfc-release|$case_dir/release-aws/credentials$" "$case_dir/aws.log"
if printf '%s\n' "$current_output$pending_output$delayed_output$quarantined_output$failed_output$checking_failure_output$stale_failure_output" | grep -q 'application-secret\|release-secret'; then
	printf '%s\n' 'release status leaked a credential' >&2
	exit 1
fi

printf '%s\n' 'release-status tests passed'
