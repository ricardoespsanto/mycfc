#!/bin/sh
set -eu

# This verifier intentionally runs from an independently pinned PostgreSQL
# client image. It has no candidate application binary, mounted replay input,
# AWS identity, provider configuration, or direct table access.
observer_image=${MYCFC_RESTORE_OBSERVER_IMAGE:-postgres:16.9-alpine3.21@sha256:9f2364d2e5382f9ec8689d36d09292e6d3e442c55b83304206d6b179e56157c5}

fail() {
	printf '%s\n' 'privacy_restore_observer_failed' >&2
	exit 1
}

if [ "$#" -ne 7 ] || [ -z "${PRIVACY_RESTORE_OBSERVER_DATABASE_URL:-}" ] || [ -z "${PRIVACY_RESTORE_OBSERVER_NETWORK:-}" ]; then
	fail
fi
input_source=$1
inventory_sha256=$2
schema_migration_digest=$3
policy_version=$4
executor_version=$5
plan_schema_version=$6
image_digest=$7

case "$observer_image" in
	postgres:16.9-alpine3.21@sha256:[0-9a-f][0-9a-f]*) ;;
	*) fail ;;
esac
if ! printf '%s' "$observer_image" | grep -Eq '^postgres:16\.9-alpine3\.21@sha256:[0-9a-f]{64}$' ||
	! printf '%s' "$inventory_sha256:$schema_migration_digest" | grep -Eq '^[0-9a-f]{64}:[0-9a-f]{64}$' ||
	! printf '%s' "$image_digest" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
	fail
fi
case "$input_source" in LIVE_LEDGER | SYNTHETIC_BOOTSTRAP) ;; *) fail ;; esac
for value in "$policy_version" "$executor_version" "$plan_schema_version"; do
	if ! printf '%s' "$value" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'; then
		fail
	fi
done

query="SELECT replay_count,source_already_applied_count,synthetic_count,verified_run_count,expected_checkpoint_count,succeeded_checkpoint_count,provider_absent_count,consent_clock_verified_count,closure_v4_count,erasure_effective_at_verified_count,membership_postcondition_contract,encode(membership_postcondition_sha256,'hex'),membership_postcondition_verified_count,membership_count,variation_count,encode(evidence_sha256,'hex') FROM privacy_restore_observe_inventory(:'input_source',decode(:'inventory_sha256','hex'),decode(:'schema_migration_digest','hex'),:'policy_version',:'executor_version',:'plan_schema_version',:'image_digest');"
DATABASE_URL=$PRIVACY_RESTORE_OBSERVER_DATABASE_URL
export DATABASE_URL
result=$(docker run --rm --network "$PRIVACY_RESTORE_OBSERVER_NETWORK" \
	-e DATABASE_URL \
	"$observer_image" sh -ec 'exec psql "$DATABASE_URL" "$@"' sh -X -q -A -t -F '|' -v ON_ERROR_STOP=1 \
	-v input_source="$input_source" -v inventory_sha256="$inventory_sha256" -v schema_migration_digest="$schema_migration_digest" \
	-v policy_version="$policy_version" -v executor_version="$executor_version" -v plan_schema_version="$plan_schema_version" -v image_digest="$image_digest" \
	-c "$query") || fail

if [ "$(printf '%s\n' "$result" | sed '/^$/d' | wc -l | tr -d ' ')" -ne 1 ]; then
	fail
fi
IFS='|' read -r replay_count source_already_applied_count synthetic_count verified_run_count expected_checkpoint_count succeeded_checkpoint_count provider_absent_count consent_clock_verified_count closure_v4_count erasure_effective_at_verified_count membership_postcondition_contract membership_postcondition_sha256 membership_postcondition_verified_count membership_count variation_count evidence_sha256 extra <<EOF
$result
EOF
if [ -n "$extra" ]; then fail; fi
for value in "$replay_count" "$source_already_applied_count" "$synthetic_count" "$verified_run_count" "$expected_checkpoint_count" "$succeeded_checkpoint_count" "$provider_absent_count" "$consent_clock_verified_count" "$closure_v4_count" "$erasure_effective_at_verified_count" "$membership_postcondition_verified_count" "$membership_count" "$variation_count"; do
	case "$value" in '' | *[!0-9]*) fail ;; esac
done
if [ "$replay_count" -lt 1 ] || [ "$source_already_applied_count" -gt "$replay_count" ] ||
	[ "$verified_run_count" -ne "$replay_count" ] || [ "$expected_checkpoint_count" -lt 1 ] ||
	[ "$expected_checkpoint_count" -ne "$succeeded_checkpoint_count" ] || [ "$provider_absent_count" -ne "$replay_count" ] ||
	[ "$consent_clock_verified_count" -ne "$replay_count" ] || [ "$closure_v4_count" -ne "$replay_count" ] ||
	[ "$erasure_effective_at_verified_count" -ne "$replay_count" ] || [ "$membership_postcondition_verified_count" -ne "$replay_count" ] ||
	[ "$membership_postcondition_contract" != 'mycfc/membership-history-postcondition/v1' ] ||
	! printf '%s:%s' "$membership_postcondition_sha256" "$evidence_sha256" | grep -Eq '^[0-9a-f]{64}:[0-9a-f]{64}$'; then
	fail
fi
case "$input_source:$synthetic_count" in
	LIVE_LEDGER:0) ;;
	SYNTHETIC_BOOTSTRAP:0) fail ;;
	SYNTHETIC_BOOTSTRAP:*) [ "$synthetic_count" -eq "$replay_count" ] || fail ;;
	*) fail ;;
esac

jq -cn \
	--arg image_digest "${observer_image##*@}" \
	--arg evidence_sha256 "$evidence_sha256" \
	--argjson replay_count "$replay_count" \
	--argjson source_already_applied_count "$source_already_applied_count" \
	--argjson synthetic_count "$synthetic_count" \
	--argjson verified_run_count "$verified_run_count" \
	--argjson expected_checkpoint_count "$expected_checkpoint_count" \
	--argjson succeeded_checkpoint_count "$succeeded_checkpoint_count" \
	--argjson provider_absent_count "$provider_absent_count" \
	--argjson consent_clock_verified_count "$consent_clock_verified_count" \
	--argjson closure_v4_count "$closure_v4_count" \
	--argjson erasure_effective_at_verified_count "$erasure_effective_at_verified_count" \
	--arg membership_postcondition_contract "$membership_postcondition_contract" \
	--arg membership_postcondition_sha256 "$membership_postcondition_sha256" \
	--argjson membership_postcondition_verified_count "$membership_postcondition_verified_count" \
	--argjson membership_count "$membership_count" --argjson variation_count "$variation_count" \
	'{image_digest:$image_digest,replay_count:$replay_count,source_already_applied_count:$source_already_applied_count,synthetic_count:$synthetic_count,verified_run_count:$verified_run_count,expected_checkpoint_count:$expected_checkpoint_count,succeeded_checkpoint_count:$succeeded_checkpoint_count,provider_absent_count:$provider_absent_count,consent_clock_verified_count:$consent_clock_verified_count,closure_v4_count:$closure_v4_count,erasure_effective_at_verified_count:$erasure_effective_at_verified_count,membership_postcondition_contract:$membership_postcondition_contract,membership_postcondition_sha256:$membership_postcondition_sha256,membership_postcondition_verified_count:$membership_postcondition_verified_count,membership_count:$membership_count,variation_count:$variation_count,evidence_sha256:$evidence_sha256}'
