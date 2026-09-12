#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
tree=dddddddddddddddddddddddddddddddddddddddd
digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
manifest=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
migrations='["001_initial","002_guardian","reset-baseline-v1"]'
schema=$(printf '%s' "$(printf '%s' "$migrations" | jq -r 'join("\n")')" | sha256sum | awk '{print $1}')
gates='{"guardian_intake":false,"privacy_worker":true}'
publication=$work_dir/publication.json

generate_publication() {
	env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha GIT_TREE_SHA=$tree IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
		RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" MIGRATION_INVENTORY_JSON="$migrations" \
		EXPECTED_GATES_JSON="$gates" PUBLISHED_AT=2026-09-12T12:00:00Z CI_RUN_ID=123 RELEASE_ISSUES="$1" \
		RELEASE_EVIDENCE_OUTPUT="$publication" sh "$script_dir/release-evidence.sh" publication
}

generate_publication 274,109,274
jq -e --arg sha "$sha" --arg digest "$digest" --arg schema "$schema" '
	.contract == "mycfc/release-publication/v1" and .version == "v1.25.0" and .git_sha == $sha and
	.image.digest == $digest and .schema.migration_digest == $schema and
	.schema.ordered_migrations == ["001_initial","002_guardian","reset-baseline-v1"] and
	.expected_gates == {guardian_intake:false,privacy_worker:true} and .issues == [109,274] and .ci_run_id == 123
' "$publication" >/dev/null
first=$(sha256sum "$publication" | awk '{print $1}')
generate_publication 274,109
test "$(sha256sum "$publication" | awk '{print $1}')" = "$first"

receipt=$work_dir/receipt.json
env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" PUBLICATION_MANIFEST_SHA256=$manifest \
	RELEASE_RESULT=failed RELEASE_SLOT=blue RELEASE_FAILURE_PHASE=database_migrate TRAFFIC_SWITCHED=false ROLLBACK_PERFORMED=false \
	GUARDIAN_INTAKE_ACTIVE=false PRIVACY_WORKER_ACTIVE=false PRIVACY_WORKER_ACTIVATION_REQUIRED=false \
	RELEASE_STARTED_AT=2026-09-12T12:00:01Z RELEASE_FINISHED_AT=2026-09-12T12:00:03Z \
	RELEASE_EVIDENCE_OUTPUT="$receipt" sh "$script_dir/release-evidence.sh" receipt
jq -e '.contract == "mycfc/deployment-receipt/v1" and .result == "failed" and .failure_phase == "database_migrate" and .traffic_switched == false and .rollback_performed == false and .actual_gates == {guardian_intake:false,privacy_worker:false} and .privacy_worker_activation_required == false' "$receipt" >/dev/null

if env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" PUBLICATION_MANIFEST_SHA256=$manifest \
	RELEASE_RESULT=succeeded RELEASE_SLOT=blue RELEASE_FAILURE_PHASE=database_migrate TRAFFIC_SWITCHED=true ROLLBACK_PERFORMED=false \
	GUARDIAN_INTAKE_ACTIVE=false PRIVACY_WORKER_ACTIVE=true PRIVACY_WORKER_ACTIVATION_REQUIRED=false \
	RELEASE_STARTED_AT=2026-09-12T12:00:03Z RELEASE_FINISHED_AT=2026-09-12T12:00:01Z \
	RELEASE_EVIDENCE_OUTPUT="$work_dir/rejected.json" sh "$script_dir/release-evidence.sh" receipt >/dev/null 2>&1; then
	printf '%s\n' 'internally impossible receipt was accepted' >&2
	exit 1
fi

canary='postgres://operator:secret@example.invalid/mycfc'
if env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha GIT_TREE_SHA=$tree IMAGE_REPOSITORY="$canary" IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" MIGRATION_INVENTORY_JSON="$migrations" \
	EXPECTED_GATES_JSON="$gates" PUBLISHED_AT=2026-09-12T12:00:00Z CI_RUN_ID=123 RELEASE_EVIDENCE_OUTPUT="$work_dir/rejected.json" \
	sh "$script_dir/release-evidence.sh" publication >/dev/null 2>&1; then
	printf '%s\n' 'credential-shaped repository was accepted' >&2
	exit 1
fi
if grep -R -Fq "$canary" "$work_dir"; then printf '%s\n' 'redaction canary reached release evidence' >&2; exit 1; fi

if generate_publication '274,invalid' >/dev/null 2>&1; then printf '%s\n' 'invalid issue allowlist was accepted' >&2; exit 1; fi

printf '%s\n' 'release-evidence tests passed'
