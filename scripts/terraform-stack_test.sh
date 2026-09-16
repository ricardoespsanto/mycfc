#!/usr/bin/env bash
# Each scenario intentionally resets its exported inputs inside an isolated shell.
# shellcheck disable=SC2030,SC2031
set -Eeuo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
source "$repo/scripts/terraform-stack.sh"
workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
cd "$workspace"
mkdir -p infra/environments/{production,hetzner}

# Simulate only the Docker boundary; no provider call or credential is made.
docker() {
  printf '%s\n' "$@" > "$workspace/docker-args"
  [[ ${STACK_TF_VARS+x} != x && ${STACK_PROVIDER_TOKEN+x} != x ]]
  if [[ "$REQUESTED_STACK" == production ]]; then
    [[ ${CLOUDFLARE_API_TOKEN-} == selected-token && ${HCLOUD_TOKEN+x} != x ]]
  else
    [[ ${HCLOUD_TOKEN-} == selected-token && ${CLOUDFLARE_API_TOKEN+x} != x ]]
  fi
}
export TERRAFORM_IMAGE=pinned-test-image
for stack in production hetzner; do
  (
    export REQUESTED_STACK=$stack STACK_TF_VARS='secret-value-do-not-log' STACK_PROVIDER_TOKEN=selected-token
    export CLOUDFLARE_API_TOKEN=wrong-inherited-token HCLOUD_TOKEN=wrong-inherited-token
    terraform_stack_prepare
    [[ "$TF_ROOT" == "infra/environments/$stack" ]]
    [[ "$TF_BACKEND_KEY" == "mycfc/$stack/terraform.tfstate" ]]
    [[ $(bash -c 'printf "%s" "$TF_BACKEND_KEY"') == "$TF_BACKEND_KEY" ]]
    [[ $(stat -c '%a' "$TF_ROOT/ci.auto.tfvars") == 600 ]]
    [[ $(cat "$TF_ROOT/ci.auto.tfvars") == secret-value-do-not-log ]]
    tf init -backend-config="key=$TF_BACKEND_KEY"
    grep -q -F -- "-chdir=$TF_ROOT" "$workspace/docker-args"
    grep -q -F -- "-backend-config=key=mycfc/$stack/terraform.tfstate" "$workspace/docker-args"
    if grep -q -F -- '-var-file=' "$workspace/docker-args"; then exit 1; fi
    tf plan -input=false -out=production.tfplan
    [[ $(tail -n 1 "$workspace/docker-args") == '-var-file=privacy-infrastructure.tfvars' ]]
    grep -q -x "$TF_PROVIDER_VARIABLE" "$workspace/docker-args"
    if [[ "$stack" == production ]]; then
      if grep -q -x HCLOUD_TOKEN "$workspace/docker-args"; then exit 1; fi
    else
      if grep -q -x CLOUDFLARE_API_TOKEN "$workspace/docker-args"; then exit 1; fi
    fi
    if grep -q -E 'selected-token|secret-value' "$workspace/docker-args"; then exit 1; fi
    tf apply -input=false production.tfplan
    if grep -q -F -- '-var-file=' "$workspace/docker-args"; then exit 1; fi
    for suffix in '' .json .review.txt; do touch "$TF_ROOT/production.tfplan$suffix"; done
    terraform_stack_cleanup
    [[ -z $(find "$TF_ROOT" -type f -print -quit) ]]
  )
done

for invalid in '' '../production' 'production/../../hetzner' 'PRODUCTION' 'hetzner;touch injected'; do
  if terraform_stack_select "$invalid" 2>/dev/null; then
    printf '%s\n' 'Invalid Terraform stack accepted.' >&2; exit 1
  fi
done
[[ ! -e injected ]]
for missing in STACK_TF_VARS STACK_PROVIDER_TOKEN; do
  (
    export REQUESTED_STACK=hetzner STACK_TF_VARS=present STACK_PROVIDER_TOKEN=present
    unset "$missing"
    if terraform_stack_prepare 2>/dev/null; then exit 1; fi
    [[ ! -e infra/environments/hetzner/ci.auto.tfvars ]]
  )
done
# Unknown selection fails before writing either secret into a checkout.
(
  export REQUESTED_STACK=unknown STACK_TF_VARS=present STACK_PROVIDER_TOKEN=present
  if terraform_stack_prepare 2>/dev/null; then exit 1; fi
)

# Exercise the real HMAC subprocess, not just shell-local backend selection.
export TF_PLAN_HMAC_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
export TF_BACKEND_BUCKET=reviewed-state-bucket AWS_REGION=eu-west-1
terraform_stack_select production
production_hmac=$(printf '%s' '{}' | python3 "$repo/scripts/terraform-plan-hmac.py" -)
terraform_stack_select hetzner
hetzner_hmac=$(printf '%s' '{}' | python3 "$repo/scripts/terraform-plan-hmac.py" -)
[[ "$production_hmac" != "$hetzner_hmac" ]]

# The source-controlled foundation posture must never enable later capabilities.
python3 - "$repo" <<'PY'
from pathlib import Path
import re
import sys
root = Path(sys.argv[1])
for stack, enabled in [('production', 'privacy_worker_infrastructure_enabled'),
                       ('hetzner', 'privacy_restore_infrastructure_enabled')]:
    text = (root / f'infra/environments/{stack}/privacy-infrastructure.tfvars').read_text()
    pairs = dict(re.findall(r'^(\w+)\s*=\s*(\S+)', text, re.M))
    assert pairs.pop(enabled) == 'true'
    assert all(value in ('false', 'null') for value in pairs.values())
PY
printf '%s\n' 'Terraform stack selection, credential isolation and durable posture tests passed.'
