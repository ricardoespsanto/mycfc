#!/bin/sh
set -eu

deployment_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
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

pending_output=$(TEST_NOW_EPOCH=1100 run_status)
printf '%s\n' "$pending_output" | grep -q '^state=pending$'

delayed_output=$(TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$delayed_output" | grep -q '^state=delayed$'

printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' >"$case_dir/state/failed-release-digest"
quarantined_output=$(TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$quarantined_output" | grep -q '^state=quarantined$'
rm "$case_dir/state/failed-release-digest"

failed_output=$(TEST_AGENT_RESULT=failed TEST_AGENT_EXIT_STATUS=1 TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$failed_output" | grep -q '^state=failed$'

printf 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' >"$case_dir/state/last-attempt-digest"
printf 'failed\n' >"$case_dir/state/last-attempt-result"
stale_failure_output=$(TEST_AGENT_RESULT=failed TEST_AGENT_EXIT_STATUS=1 TEST_NOW_EPOCH=1400 run_status)
printf '%s\n' "$stale_failure_output" | grep -q '^state=delayed$'

grep -q "^mycfc-release|$case_dir/release-aws/credentials$" "$case_dir/aws.log"
if printf '%s\n' "$current_output$pending_output$delayed_output$quarantined_output$failed_output$stale_failure_output" | grep -q 'application-secret\|release-secret'; then
	printf '%s\n' 'release status leaked a credential' >&2
	exit 1
fi

printf '%s\n' 'release-status tests passed'
