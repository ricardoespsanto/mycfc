#!/usr/bin/env bash
# Sourced by the protected Terraform workflows; selection never accepts a path.
terraform_stack_select() {
  case "${1-}" in
    production)
      TF_ROOT=infra/environments/production
      TF_BACKEND_KEY=mycfc/production/terraform.tfstate
      TF_PROVIDER_VARIABLE=CLOUDFLARE_API_TOKEN
      ;;
    hetzner)
      TF_ROOT=infra/environments/hetzner
      TF_BACKEND_KEY=mycfc/hetzner/terraform.tfstate
      TF_PROVIDER_VARIABLE=HCLOUD_TOKEN
      ;;
    *) printf '%s\n' 'Unsupported Terraform stack.' >&2; return 1 ;;
  esac
  export TF_BACKEND_KEY
}

terraform_stack_prepare() {
  terraform_stack_select "${REQUESTED_STACK-}" || return 1
  if [[ -z "${STACK_TF_VARS-}" || -z "${STACK_PROVIDER_TOKEN-}" ]]; then
    printf '%s\n' 'Selected Terraform stack configuration is missing.' >&2
    return 1
  fi
  # Do not forward another stack's provider credential, even if inherited.
  unset CLOUDFLARE_API_TOKEN HCLOUD_TOKEN
  printf -v "$TF_PROVIDER_VARIABLE" '%s' "$STACK_PROVIDER_TOKEN"
  export "${TF_PROVIDER_VARIABLE?}"
  mkdir -p .cache/terraform/plugin-cache
  umask 077
  printf '%s' "$STACK_TF_VARS" > "$TF_ROOT/ci.auto.tfvars"
  unset STACK_TF_VARS STACK_PROVIDER_TOKEN
}

terraform_stack_cleanup() {
  rm -f "$TF_ROOT/ci.auto.tfvars" "$TF_ROOT/production.tfplan" \
    "$TF_ROOT/production.tfplan.json" "$TF_ROOT/production.tfplan.review.txt"
}

tf() {
  local operation=${1:?}
  shift
  docker run --rm --user "$(id -u):$(id -g)" \
    -v "$PWD:/workspace" -v "$PWD/.cache/terraform/plugin-cache:/terraform-plugin-cache" \
    -w /workspace -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN \
    -e AWS_REGION -e "$TF_PROVIDER_VARIABLE" -e TF_IN_AUTOMATION -e TF_PLUGIN_CACHE_DIR \
    "$TERRAFORM_IMAGE" "-chdir=$TF_ROOT" "$operation" "$@"
}

terraform_preview_operation_select() {
  case "${1-}" in
    plan|discover-inputs) ;;
    *) printf '%s\n' 'Unsupported Terraform preview operation.' >&2; return 1 ;;
  esac
}
