#!/usr/bin/env bash
set -Eeuo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
source "$repo/scripts/terraform-stack.sh"
terraform_preview_operation_select plan
terraform_preview_operation_select discover-inputs
for invalid in '' apply '../plan' 'discover-inputs;touch injected'; do
  if terraform_preview_operation_select "$invalid" 2>/dev/null; then exit 1; fi
done
workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
mkdir "$workspace/bin"
cat > "$workspace/bin/aws" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$@" > "$MOCK_ARGS"
if [[ ${MOCK_FAIL-} == denied ]]; then
  printf '%s\n' 'AccessDeniedException: PRIVATE_DIAGNOSTIC_WITH_ACCOUNT_AND_SECRET' >&2
  exit 1
fi
if [[ ${MOCK_FAIL-} == service ]]; then
  printf '%s\n' 'PRIVATE_NETWORK_DIAGNOSTIC' >&2
  exit 1
fi
cat "$MOCK_RESPONSE"
MOCK
chmod +x "$workspace/bin/aws"
export PATH="$workspace/bin:$PATH" AWS_REGION=eu-west-1
export MOCK_ARGS="$workspace/args" MOCK_RESPONSE="$workspace/response"
export GITHUB_STEP_SUMMARY="$workspace/summary"
cat > "$MOCK_RESPONSE" <<'JSON'
["unrelated-private-name", "/MyCFC/config", "HCLOUD_TOKEN", "hetzner/config", "mycfc/`$(touch injected)`\n[bad](https://bad)", "HCLOUD_TOKEN"]
JSON
bash "$repo/scripts/discover-terraform-inputs.sh" > "$workspace/stdout" 2> "$workspace/stderr"
[[ ! -s "$workspace/stdout" && ! -s "$workspace/stderr" && ! -e injected ]]
grep -qx 'secretsmanager' "$MOCK_ARGS"
grep -qx 'list-secrets' "$MOCK_ARGS"
grep -qx 'SecretList\[\].Name' "$MOCK_ARGS"
grep -q -- '- `/MyCFC/config`' "$GITHUB_STEP_SUMMARY"
grep -q -- '- `hetzner/config`' "$GITHUB_STEP_SUMMARY"
[[ $(grep -c -- '- `HCLOUD_TOKEN`' "$GITHUB_STEP_SUMMARY") == 1 ]]
if grep -q -E 'unrelated-private-name|https://bad|\$\(|get-secret-value|describe-secret|--no-paginate' "$GITHUB_STEP_SUMMARY" "$MOCK_ARGS"; then exit 1; fi
python3 - "$GITHUB_STEP_SUMMARY" <<'PY'
import re
import sys
from pathlib import Path
names = [line for line in Path(sys.argv[1]).read_text().splitlines() if line.startswith('- ')]
assert len(names) == 4
assert all(re.fullmatch(r'- `[A-Za-z0-9/_+=.@?\-]+`', line) for line in names)
PY
printf '%s' '[]' > "$MOCK_RESPONSE"
: > "$GITHUB_STEP_SUMMARY"
bash "$repo/scripts/discover-terraform-inputs.sh"
grep -q 'No secret names matched' "$GITHUB_STEP_SUMMARY"
for failure in denied service malformed; do
  : > "$GITHUB_STEP_SUMMARY"
  export MOCK_FAIL=$failure
  printf '%s' '{"Name":"mycfc","SecretString":"PRIVATE_PAYLOAD"}' > "$MOCK_RESPONSE"
  if bash "$repo/scripts/discover-terraform-inputs.sh" > "$workspace/stdout" 2> "$workspace/stderr"; then exit 1; fi
  [[ ! -s "$GITHUB_STEP_SUMMARY" && ! -s "$workspace/stdout" ]]
  if grep -q -E 'PRIVATE_|SecretString' "$workspace/stderr"; then exit 1; fi
  if [[ "$failure" == denied ]]; then grep -q 'secretsmanager:ListSecrets' "$workspace/stderr"; fi
done
printf '%s\n' 'Terraform input discovery minimization and failure tests passed.'
