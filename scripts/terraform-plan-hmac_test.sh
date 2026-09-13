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
plan_role=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"account_id":"123456789012","id":"123456789012","arn":"arn:aws:sts::123456789012:assumed-role/github-infra-plan/GitHubActions","user_id":"PLAN:GitHubActions"}}]}}}')
apply_role=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"user_id":"APPLY:GitHubActions","arn":"arn:aws:sts::123456789012:assumed-role/github-infra-apply/GitHubActions","id":"123456789012","account_id":"123456789012"}}]}}}')
different_account=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"account_id":"999999999999","id":"999999999999","arn":"arn:aws:sts::999999999999:assumed-role/github-infra-apply/GitHubActions","user_id":"APPLY:GitHubActions"}}]}}}')
different_partition=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"account_id":"123456789012","id":"123456789012","arn":"arn:aws-us-gov:sts::123456789012:assumed-role/github-infra-apply/GitHubActions","user_id":"APPLY:GitHubActions"}}]}}}')
different_session=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"account_id":"123456789012","id":"123456789012","arn":"arn:aws:sts::123456789012:assumed-role/github-infra-apply/OtherSession","user_id":"APPLY:OtherSession"}}]}}}')
unrelated_role=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"account_id":"123456789012","id":"123456789012","arn":"arn:aws:sts::123456789012:assumed-role/unrelated/GitHubActions","user_id":"OTHER:GitHubActions"}}]}}}')
sensitive_plan_role_value=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","sensitive_values":{"value":true},"values":{"value":"arn:aws:sts::123456789012:assumed-role/github-infra-plan/GitHubActions"}}]}}}')
sensitive_apply_role_value=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","sensitive_values":{"value":true},"values":{"value":"arn:aws:sts::123456789012:assumed-role/github-infra-apply/GitHubActions"}}]}}}')
nested_caller_shape_plan=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","mode":"managed","type":"aws_ssm_parameter","name":"secret","sensitive_values":{"value":true},"values":{"value":{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"arn":"arn:aws:sts::123456789012:assumed-role/github-infra-plan/GitHubActions","user_id":"PLAN:GitHubActions"}}}}]}}}')
nested_caller_shape_apply=$(hmac_for '{"planned_values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","mode":"managed","type":"aws_ssm_parameter","name":"secret","sensitive_values":{"value":true},"values":{"value":{"address":"data.aws_caller_identity.current","mode":"data","type":"aws_caller_identity","name":"current","values":{"arn":"arn:aws:sts::123456789012:assumed-role/github-infra-apply/GitHubActions","user_id":"APPLY:GitHubActions"}}}}]}}}')
unordered_metadata_first=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"aws_s3_bucket.second","values":{"id":"second"}},{"address":"aws_s3_bucket.first","values":{"id":"first"}}],"child_modules":[{"address":"module.second","resources":[]},{"address":"module.first","resources":[]}]}}},"relevant_attributes":[{"resource":"aws_s3_bucket.second","attribute":["id"]},{"resource":"aws_s3_bucket.first","attribute":["id"]}]}')
unordered_metadata_second=$(hmac_for '{"prior_state":{"values":{"root_module":{"child_modules":[{"resources":[],"address":"module.first"},{"resources":[],"address":"module.second"}],"resources":[{"values":{"id":"first"},"address":"aws_s3_bucket.first"},{"values":{"id":"second"},"address":"aws_s3_bucket.second"}]}}},"relevant_attributes":[{"attribute":["id"],"resource":"aws_s3_bucket.first"},{"attribute":["id"],"resource":"aws_s3_bucket.second"}]}')
changed_prior_state=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"aws_s3_bucket.second","values":{"id":"changed"}},{"address":"aws_s3_bucket.first","values":{"id":"first"}}]}}},"relevant_attributes":[{"resource":"aws_s3_bucket.second","attribute":["id"]},{"resource":"aws_s3_bucket.first","attribute":["id"]}]}')
cloudflare_plan_permissions=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"data.cloudflare_zones.application","mode":"data","type":"cloudflare_zones","name":"application","values":{"result":[{"id":"zone-id","name":"example.org","permissions":["#zone:read"]}]}}]}}}}')
cloudflare_apply_permissions=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"data.cloudflare_zones.application","mode":"data","type":"cloudflare_zones","name":"application","values":{"result":[{"permissions":["#dns_records:edit","#zone:read"],"name":"example.org","id":"zone-id"}]}}]}}}}')
cloudflare_different_zone=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"data.cloudflare_zones.application","mode":"data","type":"cloudflare_zones","name":"application","values":{"result":[{"id":"different-zone-id","name":"example.org","permissions":["#zone:read"]}]}}]}}}}')
cloudflare_nested_permissions=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","mode":"managed","type":"aws_ssm_parameter","name":"secret","values":{"value":{"address":"data.cloudflare_zones.application","mode":"data","type":"cloudflare_zones","name":"application","values":{"result":[{"permissions":["#zone:read"]}]}}}}]}}}}')
cloudflare_nested_permissions_changed=$(hmac_for '{"prior_state":{"values":{"root_module":{"resources":[{"address":"aws_ssm_parameter.secret","mode":"managed","type":"aws_ssm_parameter","name":"secret","values":{"value":{"address":"data.cloudflare_zones.application","mode":"data","type":"cloudflare_zones","name":"application","values":{"result":[{"permissions":["#dns_records:edit"]}]}}}}]}}}}')

test "$first" = "$same_semantics"
test "$first" != "$different_secret"
test "$precise_number" != "$different_precise_number"
test "$plan_role" = "$apply_role"
test "$plan_role" != "$different_account"
test "$plan_role" != "$different_partition"
test "$plan_role" != "$different_session"
test "$plan_role" != "$unrelated_role"
test "$sensitive_plan_role_value" != "$sensitive_apply_role_value"
test "$nested_caller_shape_plan" != "$nested_caller_shape_apply"
test "$unordered_metadata_first" = "$unordered_metadata_second"
test "$unordered_metadata_first" != "$changed_prior_state"
test "$cloudflare_plan_permissions" = "$cloudflare_apply_permissions"
test "$cloudflare_plan_permissions" != "$cloudflare_different_zone"
test "$cloudflare_nested_permissions" != "$cloudflare_nested_permissions_changed"
echo "$first" | grep -Eq '^[0-9a-f]{64}$'

components=$(printf '%s\n' '{"timestamp":"ignored","variables":{"secret":{"value":"first"}},"planned_values":{}}' | python3 scripts/terraform-plan-hmac.py --components -)
echo "$components" | jq -e 'keys == ["planned_values", "variables"] and all(.[]; test("^[0-9a-f]{64}$"))' >/dev/null
component_file=$(mktemp)
trap 'rm -f "$component_file"' EXIT
printf '%s\n' '{"timestamp":"ignored","variables":{"secret":{"value":"first"}},"planned_values":{}}' > "$component_file"
file_components=$(python3 scripts/terraform-plan-hmac.py --components "$component_file")
test "$components" = "$file_components"

prior_components=$(printf '%s\n' '{"prior_state":{"format_version":"1.0","values":{"outputs":{"example":{"value":"secret"}},"root_module":{"resources":[{"address":"aws_s3_bucket.example","values":{"id":"bucket"}}]}}}}' | python3 scripts/terraform-plan-hmac.py --components -)
echo "$prior_components" | jq -e '
  has("prior_state") and
  has("prior_state.format_version") and
  has("prior_state.output[example]") and
  has("prior_state.resource[aws_s3_bucket.example]") and
  all(.[]; test("^[0-9a-f]{64}$"))
' >/dev/null

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
