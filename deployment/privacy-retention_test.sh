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
printf '%s\n' 'privacy_retention_succeeded due_count=0'
EOF
chmod 0755 "$test_dir/bin/stat" "$test_dir/bin/docker"
touch "$test_dir/retention.env"

cat >"$test_dir/main.env" <<'EOF'
PRIVACY_RETENTION_ENABLED=false
EOF
output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_RETENTION_ENV_FILE="$test_dir/retention.env" MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-retention.sh")
[ "$output" = privacy_retention_disabled ] || { printf '%s\n' 'disabled retention runner did not stay inert' >&2; exit 1; }

cat >"$test_dir/main.env" <<'EOF'
PRIVACY_RETENTION_ENABLED=true
MYCFC_IMAGE=registry.invalid/mycfc@sha256:test
EOF
export DOCKER_CALLS="$test_dir/docker.calls"
output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_RETENTION_ENV_FILE="$test_dir/retention.env" MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-retention.sh")
printf '%s' "$output" | grep -q '^privacy_retention_succeeded ' || { printf '%s\n' 'enabled retention runner did not preserve safe evidence' >&2; exit 1; }
grep -q -- '--profile maintenance run --rm --no-deps privacy-retention' "$DOCKER_CALLS" || { printf '%s\n' 'retention compose command is not exact' >&2; exit 1; }
if grep -q 'PRIVACY_RETENTION_DATABASE_URL' "$DOCKER_CALLS"; then
	printf '%s\n' 'retention runner exposed the database credential' >&2
	exit 1
fi

printf '%s\n' 'privacy retention deployment tests passed'
