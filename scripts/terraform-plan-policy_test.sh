#!/bin/sh
set -eu

policy=scripts/terraform-plan-policy.jq

plan() {
  extra='{}'
  if [ "$#" -ge 2 ]; then
    extra=$2
  fi
  jq -cn \
    --argjson actions "$1" \
    --argjson extra "$extra" \
    '{
      format_version: "1.2",
      errored: false,
      complete: true,
      configuration: {root_module: {resources: []}},
      resource_changes: [{address: "test.example", change: {actions: $actions}}]
    } + $extra'
}

accepts() {
  printf '%s\n' "$1" | jq -e -f "$policy" >/dev/null
}

rejects() {
  if accepts "$1"; then
    echo 'unsafe Terraform plan was accepted' >&2
    return 1
  fi
}

for actions in '["no-op"]' '["create"]' '["read"]' '["update"]'; do
  candidate=$(plan "$actions")
  accepts "$candidate"
done

for actions in \
  '["delete"]' \
  '["delete","create"]' \
  '["create","delete"]' \
  '["forget"]' \
  '["create","forget"]' \
  '["future-action"]'; do
  candidate=$(plan "$actions")
  rejects "$candidate"
done

for extra in \
  '{"format_version":"2.0"}' \
  '{"errored":true}' \
  '{"complete":false}' \
  '{"deferred_changes":[{}]}' \
  '{"action_invocations":[{}]}' \
  '{"deferred_action_invocations":[{}]}' \
  '{"resource_changes":[{"change":{"actions":["no-op"],"importing":{"id":"example"}}}]}' \
  '{"configuration":{"root_module":{"resources":[{"provisioners":[{"type":"local-exec"}]}]}}}'; do
  candidate=$(plan '["no-op"]' "$extra")
  rejects "$candidate"
done

echo 'terraform plan policy tests passed'
