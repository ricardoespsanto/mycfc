#!/usr/bin/env bash
# Names-only discovery. Never fetch secret values or print provider diagnostics.
set -Eeuo pipefail
if [[ -z ${AWS_REGION-} || -z ${GITHUB_STEP_SUMMARY-} ]]; then
  printf '%s\n' 'Secrets Manager discovery requires the configured region and summary destination.' >&2
  exit 1
fi
umask 077
workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
if ! aws secretsmanager list-secrets --region "$AWS_REGION" --no-cli-pager \
  --query 'SecretList[].Name' --output json > "$workspace/names.json" 2> "$workspace/error"; then
  if grep -qi -E 'AccessDenied|Unauthorized' "$workspace/error"; then
    printf '%s\n' 'Secrets Manager discovery denied: the plan role needs secretsmanager:ListSecrets.' >&2
  else
    printf '%s\n' 'Secrets Manager discovery failed; no inventory result is available.' >&2
  fi
  exit 1
fi
if ! jq -e 'type == "array" and all(.[]; type == "string")' "$workspace/names.json" >/dev/null 2>&1; then
  printf '%s\n' 'Secrets Manager discovery returned an unsupported response; no inventory result is available.' >&2
  exit 1
fi
# Only matching names survive, and every non-name character is replaced before
# Markdown rendering. Never interpolate names into commands, outputs or paths.
jq -r '[.[] | select(test("mycfc|hetzner|hcloud"; "i"))] | unique | .[] |
  gsub("[^A-Za-z0-9/_+=.@-]"; "?") | "- `" + . + "`"' \
  "$workspace/names.json" > "$workspace/candidates"
{
  printf '%s\n\n' '## Terraform input discovery — secret names only'
  if [[ -s "$workspace/candidates" ]]; then
    cat "$workspace/candidates"
  else
    printf '%s\n' 'No secret names matched mycfc, hetzner or hcloud in the configured region.'
  fi
  printf '\n%s\n' 'No secret values were requested; descriptions and tags are excluded from this summary. Names alone cannot establish whether a nested payload contains the required inputs.'
} >> "$GITHUB_STEP_SUMMARY"
