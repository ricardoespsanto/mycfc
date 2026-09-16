#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"
cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' 0:0:600
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"$POLICY_DOCKER_CALL"
EOF
chmod 0755 "$test_dir/bin/"*
printf '%s\n' 'MYCFC_IMAGE=example.invalid/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_dir/mycfc.env"
printf '%s\n' 7f40fdc4-1653-4aa6-8cd5-e44f25c2fd85 >"$test_dir/operator"
cp "$repo_dir/internal/privacyrequests/policies/club-2026-09-15-v1.json" "$test_dir/policy.json"
chmod 0600 "$test_dir/mycfc.env" "$test_dir/operator" "$test_dir/policy.json"
export POLICY_DOCKER_CALL="$test_dir/docker.call"

PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/mycfc.env" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	MYCFC_PRIVACY_POLICY_OPERATOR_FILE="$test_dir/operator" MYCFC_PRIVACY_POLICY_FILE="$test_dir/policy.json" \
	sh "$script_dir/privacy-policy-import.sh" >"$test_dir/output"
grep -q '^privacy_policy_import_succeeded version=club-2026-09-15-v1 sha256=98d80915d8911b296768eddb278934cfb6c0f49c731bd50b14358756c07a163e$' "$test_dir/output"
grep -q -- '--profile privacy-policy-import run --rm --no-deps -v .*/policy.json:/run/privacy-policy/policy.json:ro privacy-policy-import privacy import-policy --actor 7f40fdc4-1653-4aa6-8cd5-e44f25c2fd85 --file /run/privacy-policy/policy.json' "$POLICY_DOCKER_CALL"

printf '\n' >>"$test_dir/policy.json"
if PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/mycfc.env" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	MYCFC_PRIVACY_POLICY_OPERATOR_FILE="$test_dir/operator" MYCFC_PRIVACY_POLICY_FILE="$test_dir/policy.json" \
	sh "$script_dir/privacy-policy-import.sh" >/dev/null 2>&1; then
	printf '%s\n' 'modified canonical policy was imported' >&2
	exit 1
fi

printf '%s\n' 'privacy policy import deployment tests passed'
