#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
operator_file=${MYCFC_PRIVACY_POLICY_OPERATOR_FILE:-/etc/mycfc/privacy-production-operations/policy-operator}
policy_file=${MYCFC_PRIVACY_POLICY_FILE:-/etc/mycfc/privacy-production-operations/club-2026-09-15-v1.json}
expected_sha256=98d80915d8911b296768eddb278934cfb6c0f49c731bd50b14358756c07a163e

fail() {
	printf '%s\n' 'privacy_policy_import_failed' >&2
	exit 1
}

if [ "$(id -u 2>/dev/null || true)" != 0 ] || [ "$#" -ne 0 ]; then fail; fi
for protected in "$env_file" "$operator_file" "$policy_file"; do
	if [ ! -f "$protected" ] || [ -L "$protected" ] ||
		[ "$(stat -c '%u:%g:%a' "$protected" 2>/dev/null || true)" != '0:0:600' ]; then fail; fi
done
actor=$(tr -d '\n' <"$operator_file")
printf '%s' "$actor" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' || fail
[ "$(sha256sum "$policy_file" | awk '{print $1}')" = "$expected_sha256" ] || fail

if ! docker compose --env-file "$env_file" -f "$deployment_dir/compose.yaml" --profile privacy-policy-import \
	run --rm --no-deps -v "$policy_file:/run/privacy-policy/policy.json:ro" \
	privacy-policy-import privacy import-policy --actor "$actor" --file /run/privacy-policy/policy.json >/dev/null; then
	fail
fi
printf '%s\n' "privacy_policy_import_succeeded version=club-2026-09-15-v1 sha256=$expected_sha256"
