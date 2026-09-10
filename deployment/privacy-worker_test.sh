#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' '0:600'
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_CALLS"
case "$*" in
	*privacy-worker\ readiness) printf '%s\n' 'privacy_worker_readiness_ready' ;;
esac
EOF
chmod 0755 "$test_dir/bin/stat" "$test_dir/bin/docker"
touch "$test_dir/worker.env"
export DOCKER_CALLS="$test_dir/docker.calls"

cat >"$test_dir/main.env" <<'EOF'
PRIVACY_WORKER_ENABLED=false
PRIVACY_COMPLETION_ENABLED=false
PRIVACY_REQUESTS_ENABLED=false
EOF
output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_WORKER_ENV_FILE="$test_dir/worker.env" MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-worker.sh" readiness)
[ "$output" = privacy_worker_runtime_disabled ] || { printf '%s\n' 'disabled worker did not stay inert' >&2; exit 1; }

cat >"$test_dir/main.env" <<'EOF'
PRIVACY_WORKER_ENABLED=true
PRIVACY_COMPLETION_ENABLED=true
PRIVACY_REQUESTS_ENABLED=true
MYCFC_IMAGE=registry.invalid/mycfc@sha256:test
EOF
output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_WORKER_ENV_FILE="$test_dir/worker.env" MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-worker.sh" readiness)
[ "$output" = privacy_worker_readiness_ready ] || { printf '%s\n' 'readiness output was not preserved' >&2; exit 1; }
grep -q -- '--profile privacy-worker run --rm --no-deps privacy-worker readiness' "$DOCKER_CALLS" || { printf '%s\n' 'readiness command is not exact' >&2; exit 1; }
if grep -q 'PRIVACY_EXECUTOR_DATABASE_URL' "$DOCKER_CALLS"; then
	printf '%s\n' 'worker runner exposed the database credential' >&2
	exit 1
fi

printf '%s\n' 'privacy worker deployment tests passed'
