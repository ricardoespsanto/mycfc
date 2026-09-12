#!/bin/sh
set -eu

TF_PLAN_HMAC_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
TF_BACKEND_BUCKET=mycfc-state
TF_BACKEND_KEY=mycfc/production/terraform.tfstate
AWS_REGION=eu-west-1
export TF_PLAN_HMAC_KEY TF_BACKEND_BUCKET TF_BACKEND_KEY AWS_REGION

hmac_for() {
  printf '%s\n' "$1" | python3 scripts/terraform-plan-hmac.py -
}

first=$(hmac_for '{"format_version":"1.2","timestamp":"2026-09-12T10:00:00Z","variables":{"secret":{"value":"first"}}}')
same_semantics=$(hmac_for '{"variables":{"secret":{"value":"first"}},"timestamp":"2026-09-12T10:01:00Z","format_version":"1.2"}')
different_secret=$(hmac_for '{"format_version":"1.2","timestamp":"2026-09-12T10:00:00Z","variables":{"secret":{"value":"second"}}}')
precise_number=$(hmac_for '{"format_version":"1.2","planned_values":{"outputs":{"value":{"value":0.123456789012345671}}}}')
different_precise_number=$(hmac_for '{"format_version":"1.2","planned_values":{"outputs":{"value":{"value":0.123456789012345672}}}}')
plan_role=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","values":{"account_id":"123456789012","id":"123456789012","arn":"arn:aws:sts::123456789012:assumed-role/github-infra-plan/GitHubActions","user_id":"PLAN:GitHubActions"}}]}}}')
apply_role=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","values":{"user_id":"APPLY:GitHubActions","arn":"arn:aws:sts::123456789012:assumed-role/github-infra-apply/GitHubActions","id":"123456789012","account_id":"123456789012"}}]}}}')
different_account=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","values":{"account_id":"999999999999","id":"999999999999","arn":"arn:aws:sts::999999999999:assumed-role/github-infra-apply/GitHubActions","user_id":"APPLY:GitHubActions"}}]}}}')

test "$first" = "$same_semantics"
test "$first" != "$different_secret"
test "$precise_number" != "$different_precise_number"
test "$plan_role" = "$apply_role"
test "$plan_role" != "$different_account"
echo "$first" | grep -Eq '^[0-9a-f]{64}$'

TF_BACKEND_BUCKET=wrong-state
export TF_BACKEND_BUCKET
different_backend=$(hmac_for '{"format_version":"1.2","timestamp":"2026-09-12T10:00:00Z","variables":{"secret":{"value":"first"}}}')
test "$first" != "$different_backend"
TF_BACKEND_BUCKET=mycfc-state
export TF_BACKEND_BUCKET

TF_PLAN_HMAC_KEY=too-short
export TF_PLAN_HMAC_KEY
if hmac_for '{"format_version":"1.2"}' >/dev/null 2>&1; then
  echo 'invalid HMAC key was accepted' >&2
  exit 1
fi

echo 'terraform plan HMAC tests passed'
