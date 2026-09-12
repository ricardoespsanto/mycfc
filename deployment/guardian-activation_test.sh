#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/state" "$test_dir/approval"
touch "$test_dir/main.env" "$test_dir/operator.env" "$test_dir/approval/approval.json"
printf '%s\n' 'MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_dir/main.env"
cat >"$test_dir/operator.env" <<'EOF'
GUARDIAN_ACTIVATION_DATABASE_URL=postgres://mycfc_guardian_activation_operator:secret@postgres:5432/mycfc?sslmode=disable
GUARDIAN_ACTIVATION_EXPECTED_DATABASE=mycfc
GUARDIAN_ACTIVATION_ACTOR_REF=10000000-0000-0000-0000-000000000001
EOF
printf '%s\n' blue >"$test_dir/state/active-slot"
chmod 600 "$test_dir/main.env" "$test_dir/operator.env" "$test_dir/approval/approval.json"
chmod 700 "$test_dir/approval"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' "${TEST_UID:-0}"
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
case "$*" in
  *operator.env|*main.env|*approval.json) printf '%s\n' '0:0:600' ;;
  *approval) printf '%s\n' '0:0:700' ;;
  *) /usr/bin/stat "$@" ;;
esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
if [ "$1" = inspect ]; then
  printf '%s\n' "${TEST_RUNNING_IMAGE:-$MYCFC_IMAGE}"
  exit 0
fi
printf '%s\n' "$*" >>"$TEST_DOCKER_CALLS"
case "$*" in
	*' guardian-activation enable') printf '%s\n' 'event=guardian_activation_enabled changed=true policy_sha256=redacted approval_sha256=redacted' ;;
	*' guardian-activation disable') printf '%s\n' 'event=guardian_activation_disabled intake=blocked relationships_revoked=2 credentials_revoked=1 sessions_revoked=3' ;;
	*) printf '%s\n' 'event=guardian_activation_status state=DISABLED' ;;
esac
EOF
chmod +x "$test_dir/bin/id" "$test_dir/bin/stat" "$test_dir/bin/docker"
export TEST_DOCKER_CALLS="$test_dir/docker.calls"

run_operator() {
	PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_GUARDIAN_ACTIVATION_ENV_FILE="$test_dir/operator.env" \
	 MYCFC_GUARDIAN_ACTIVATION_APPROVAL_DIR="$test_dir/approval" MYCFC_STATE_DIR="$test_dir/state" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	 sh "$script_dir/guardian-activation.sh" "$@"
}

run_operator status >/dev/null
grep -q -- '--profile guardian-activation run --rm --no-deps guardian-activation status' "$TEST_DOCKER_CALLS"
run_operator preflight >/dev/null
grep -q -- '--profile guardian-activation run --rm --no-deps guardian-activation preflight' "$TEST_DOCKER_CALLS"
enable_output=$(run_operator enable)
printf '%s\n' "$enable_output" | grep -q 'event=guardian_activation_enabled changed=true'
grep -q -- '--profile guardian-activation run --rm --no-deps guardian-activation enable' "$TEST_DOCKER_CALLS"
disable_output=$(run_operator disable)
printf '%s\n' "$disable_output" | grep -q 'relationships_revoked=2 credentials_revoked=1 sessions_revoked=3'
grep -q -- '--profile guardian-activation run --rm --no-deps guardian-activation disable' "$TEST_DOCKER_CALLS"
run_operator provision >/dev/null
grep -q -- '--profile guardian-activation-bootstrap run --rm guardian-activation-bootstrap' "$TEST_DOCKER_CALLS"

if TEST_UID=1000 run_operator status >/dev/null 2>&1; then echo 'non-root operator accepted' >&2; exit 1; fi
if TEST_RUNNING_IMAGE=registry.example/other@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run_operator status >/dev/null 2>&1; then
	echo 'mismatched active image accepted' >&2; exit 1
fi
printf '%s\n' 'UNEXPECTED=value' >>"$test_dir/operator.env"
if run_operator status >/dev/null 2>&1; then echo 'unknown operator input accepted' >&2; exit 1; fi

operator_service=$(awk '/^  guardian-activation:/{copy=1} copy{print} copy && /^  [a-z][a-z-]*:/{if (++services > 1) exit}' "$script_dir/compose.yaml")
printf '%s' "$operator_service" | grep -q '/app/guardian-activation'
if printf '%s' "$operator_service" | grep -Eq 'privacy-activation|privacy-worker|APP_DB_PASSWORD'; then
	echo 'guardian operator received an unrelated capability' >&2; exit 1
fi
if grep -Eq 'guardian-activation\.sh|provision-guardian-activation|guardian-activation-bootstrap' "$script_dir/pull-release.sh"; then
	echo 'routine release invokes the guardian activation control plane' >&2
	exit 1
fi

printf '%s\n' 'guardian activation deployment tests passed'
