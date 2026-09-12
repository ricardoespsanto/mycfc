#!/bin/sh
set -eu

fail() {
	printf 'release evidence rejected: %s\n' "$1" >&2
	exit 1
}

require_value() {
	name=$1
	eval "value=\${$name-}"
	[ -n "$value" ] || fail "$name is required"
}

valid_version() {
	printf '%s' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'
}

valid_sha() {
	printf '%s' "$1" | grep -Eq '^[0-9a-f]{40}$'
}

valid_digest() {
	printf '%s' "$1" | grep -Eq '^sha256:[0-9a-f]{64}$'
}

valid_time() {
	printf '%s' "$1" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' && date -u -d "$1" +%s >/dev/null 2>&1
}

issues_json() {
	issues=${1:-}
	if [ -z "$issues" ]; then
		printf '[]\n'
		return
	fi
	printf '%s' "$issues" | grep -Eq '^[1-9][0-9]*(,[1-9][0-9]*)*$' || \
		fail 'RELEASE_ISSUES must be positive issue numbers separated by commas'
	printf '%s\n' "$issues" | tr ',' '\n' | awk '
		!seen[$0]++ { print $0 }
	' | sort -n | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)'
}

write_canonical() {
	output=$1
	shift
	temporary=$(mktemp "${output}.tmp.XXXXXX")
	trap 'rm -f "$temporary"' EXIT HUP INT TERM
	"$@" | jq -cS . >"$temporary"
	chmod 0644 "$temporary"
	mv "$temporary" "$output"
	trap - EXIT HUP INT TERM
}

publication() {
	for name in RELEASE_VERSION GIT_SHA GIT_TREE_SHA IMAGE_REPOSITORY IMAGE_DIGEST RELEASE_TAG SCHEMA_MIGRATION_DIGEST MIGRATION_INVENTORY_JSON EXPECTED_GATES_JSON PUBLISHED_AT CI_RUN_ID RELEASE_EVIDENCE_OUTPUT; do
		require_value "$name"
	done
	valid_version "$RELEASE_VERSION" || fail 'RELEASE_VERSION is not a canonical semantic version'
	valid_sha "$GIT_SHA" || fail 'GIT_SHA must be a lowercase 40-character SHA'
	valid_sha "$GIT_TREE_SHA" || fail 'GIT_TREE_SHA must be a lowercase 40-character SHA'
	valid_digest "$IMAGE_DIGEST" || fail 'IMAGE_DIGEST must be a sha256 digest'
	printf '%s' "$SCHEMA_MIGRATION_DIGEST" | grep -Eq '^[0-9a-f]{64}$' || fail 'SCHEMA_MIGRATION_DIGEST must be a lowercase SHA-256 value'
	valid_time "$PUBLISHED_AT" || fail 'PUBLISHED_AT must be a valid whole-second UTC timestamp'
	case "$RELEASE_TAG" in release-??????????????-"$GIT_SHA") ;; *) fail 'RELEASE_TAG does not bind the exact SHA' ;; esac
	printf '%s' "$IMAGE_REPOSITORY" | grep -Eq '^[A-Za-z0-9._/-]+$' || fail 'IMAGE_REPOSITORY is invalid'
	printf '%s' "$CI_RUN_ID" | grep -Eq '^[1-9][0-9]*$' || fail 'CI_RUN_ID must be a positive integer'
	printf '%s' "$MIGRATION_INVENTORY_JSON" | jq -e 'type == "array" and length > 0 and ([.[] | select(. == "reset-baseline-v1")] | length) == 1 and all(.[]; type == "string" and test("^(reset-baseline-v1|[0-9]{3,}_[A-Za-z0-9_-]+)$")) and (length == (unique | length)) and . == sort' >/dev/null || fail 'MIGRATION_INVENTORY_JSON is invalid'
	inventory_digest=$(printf '%s' "$(printf '%s' "$MIGRATION_INVENTORY_JSON" | jq -r 'join("\n")')" | sha256sum | awk '{print $1}')
	[ "$inventory_digest" = "$SCHEMA_MIGRATION_DIGEST" ] || fail 'schema migration digest does not represent MIGRATION_INVENTORY_JSON'
	printf '%s' "$EXPECTED_GATES_JSON" | jq -e 'type == "object" and (keys | sort) == ["guardian_intake","privacy_worker"] and all(.[]; type == "boolean")' >/dev/null || fail 'EXPECTED_GATES_JSON is invalid'
	issues=$(issues_json "${RELEASE_ISSUES:-}")
	write_canonical "$RELEASE_EVIDENCE_OUTPUT" jq -n \
		--arg contract 'mycfc/release-publication/v1' \
		--arg version "$RELEASE_VERSION" \
		--arg sha "$GIT_SHA" \
		--arg tree "$GIT_TREE_SHA" \
		--arg repository "$IMAGE_REPOSITORY" \
		--arg digest "$IMAGE_DIGEST" \
		--arg tag "$RELEASE_TAG" \
		--arg schema "$SCHEMA_MIGRATION_DIGEST" \
		--arg published "$PUBLISHED_AT" \
		--argjson run "$CI_RUN_ID" \
		--argjson issues "$issues" \
		--argjson migrations "$MIGRATION_INVENTORY_JSON" \
		--argjson gates "$EXPECTED_GATES_JSON" \
		'{contract:$contract,version:$version,git_sha:$sha,git_tree_sha:$tree,image:{repository:$repository,digest:$digest},release_tag:$tag,schema:{migration_digest:$schema,ordered_migrations:$migrations},expected_gates:$gates,published_at:$published,ci_run_id:$run,issues:$issues}'
}

receipt() {
	for name in RELEASE_VERSION GIT_SHA IMAGE_REPOSITORY IMAGE_DIGEST RELEASE_TAG SCHEMA_MIGRATION_DIGEST PUBLICATION_MANIFEST_SHA256 RELEASE_RESULT RELEASE_SLOT TRAFFIC_SWITCHED ROLLBACK_PERFORMED GUARDIAN_INTAKE_ACTIVE PRIVACY_WORKER_ACTIVE PRIVACY_WORKER_ACTIVATION_REQUIRED RELEASE_STARTED_AT RELEASE_FINISHED_AT RELEASE_EVIDENCE_OUTPUT; do
		require_value "$name"
	done
	valid_version "$RELEASE_VERSION" || fail 'RELEASE_VERSION is not a canonical semantic version'
	valid_sha "$GIT_SHA" || fail 'GIT_SHA must be a lowercase 40-character SHA'
	valid_digest "$IMAGE_DIGEST" || fail 'IMAGE_DIGEST must be a sha256 digest'
	printf '%s' "$SCHEMA_MIGRATION_DIGEST" | grep -Eq '^[0-9a-f]{64}$' || fail 'SCHEMA_MIGRATION_DIGEST must be a lowercase SHA-256 value'
	valid_time "$RELEASE_STARTED_AT" || fail 'RELEASE_STARTED_AT must be a valid UTC timestamp'
	valid_time "$RELEASE_FINISHED_AT" || fail 'RELEASE_FINISHED_AT must be a valid UTC timestamp'
	[ "$(date -u -d "$RELEASE_FINISHED_AT" +%s)" -ge "$(date -u -d "$RELEASE_STARTED_AT" +%s)" ] || fail 'release finish precedes start'
	printf '%s' "$PUBLICATION_MANIFEST_SHA256" | grep -Eq '^[0-9a-f]{64}$' || fail 'PUBLICATION_MANIFEST_SHA256 is invalid'
	case "$RELEASE_RESULT" in succeeded|failed|quarantined) ;; *) fail 'RELEASE_RESULT is invalid' ;; esac
	case "$TRAFFIC_SWITCHED:$ROLLBACK_PERFORMED" in true:true|true:false|false:true|false:false) ;; *) fail 'traffic/rollback flags must be booleans' ;; esac
	case "$GUARDIAN_INTAKE_ACTIVE:$PRIVACY_WORKER_ACTIVE:$PRIVACY_WORKER_ACTIVATION_REQUIRED" in *[!a-z:]*) fail 'actual gate states must be booleans' ;; esac
	case "$GUARDIAN_INTAKE_ACTIVE" in true|false) ;; *) fail 'actual gate states must be booleans' ;; esac
	case "$PRIVACY_WORKER_ACTIVE" in true|false) ;; *) fail 'actual gate states must be booleans' ;; esac
	case "$PRIVACY_WORKER_ACTIVATION_REQUIRED" in true|false) ;; *) fail 'actual gate states must be booleans' ;; esac
	if [ "$PRIVACY_WORKER_ACTIVE" = true ] && [ "$PRIVACY_WORKER_ACTIVATION_REQUIRED" = true ]; then fail 'active privacy worker cannot require activation'; fi
	case "$RELEASE_SLOT" in blue|green|unknown) ;; *) fail 'RELEASE_SLOT is invalid' ;; esac
	case "$RELEASE_TAG" in release-??????????????-"$GIT_SHA") ;; *) fail 'RELEASE_TAG does not bind the exact SHA' ;; esac
	printf '%s' "$IMAGE_REPOSITORY" | grep -Eq '^[A-Za-z0-9._/-]+$' || fail 'IMAGE_REPOSITORY is invalid'
	case "${RELEASE_FAILURE_PHASE:-}" in ''|*[!A-Za-z0-9_-]*) [ -z "${RELEASE_FAILURE_PHASE:-}" ] || fail 'RELEASE_FAILURE_PHASE is invalid' ;; esac
	if [ "$RELEASE_RESULT" = succeeded ] && [ -n "${RELEASE_FAILURE_PHASE:-}" ]; then fail 'successful receipt cannot have a failure phase'; fi
	if [ "$RELEASE_RESULT" != succeeded ] && [ -z "${RELEASE_FAILURE_PHASE:-}" ]; then fail 'unsuccessful receipt requires a failure phase'; fi
	if [ "$ROLLBACK_PERFORMED" = true ] && [ "$TRAFFIC_SWITCHED" != true ]; then fail 'rollback cannot occur before a traffic switch'; fi
	write_canonical "$RELEASE_EVIDENCE_OUTPUT" jq -n \
		--arg contract 'mycfc/deployment-receipt/v1' \
		--arg version "$RELEASE_VERSION" \
		--arg sha "$GIT_SHA" \
		--arg repository "$IMAGE_REPOSITORY" \
		--arg digest "$IMAGE_DIGEST" \
		--arg tag "$RELEASE_TAG" \
		--arg schema "$SCHEMA_MIGRATION_DIGEST" \
		--arg manifest "$PUBLICATION_MANIFEST_SHA256" \
		--arg result "$RELEASE_RESULT" \
		--arg slot "$RELEASE_SLOT" \
		--arg phase "${RELEASE_FAILURE_PHASE:-}" \
		--arg started "$RELEASE_STARTED_AT" \
		--arg finished "$RELEASE_FINISHED_AT" \
		--argjson switched "$TRAFFIC_SWITCHED" \
		--argjson rolled_back "$ROLLBACK_PERFORMED" \
		--argjson guardian "$GUARDIAN_INTAKE_ACTIVE" \
		--argjson privacy "$PRIVACY_WORKER_ACTIVE" \
		--argjson activation_required "$PRIVACY_WORKER_ACTIVATION_REQUIRED" \
		'{contract:$contract,version:$version,git_sha:$sha,image:{repository:$repository,digest:$digest},release_tag:$tag,schema_migration_digest:$schema,publication_manifest_sha256:$manifest,result:$result,slot:$slot,failure_phase:(if $phase == "" then null else $phase end),traffic_switched:$switched,rollback_performed:$rolled_back,actual_gates:{guardian_intake:$guardian,privacy_worker:$privacy},privacy_worker_activation_required:$activation_required,started_at:$started,finished_at:$finished}'
}

case "${1:-}" in
	publication) publication ;;
	receipt) receipt ;;
	*) printf 'usage: %s publication|receipt\n' "$0" >&2; exit 2 ;;
esac
