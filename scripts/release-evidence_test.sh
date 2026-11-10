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
gates='{"guardian_intake":false,"privacy_worker":false}'
publication=$work_dir/publication.json

generate_publication() {
	env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha GIT_TREE_SHA=$tree IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
		RELEASE_TAG=release-v1.25.0-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" MIGRATION_INVENTORY_JSON="$migrations" \
		EXPECTED_GATES_JSON="$gates" PUBLISHED_AT=2026-09-12T12:00:00Z CI_RUN_ID=123 RELEASE_ISSUES="$1" \
		RELEASE_EVIDENCE_OUTPUT="$publication" sh "$script_dir/release-evidence.sh" publication
}

generate_publication 274,109,274
jq -e --arg sha "$sha" --arg digest "$digest" --arg schema "$schema" '
	.contract == "mycfc/release-publication/v1" and .version == "v1.25.0" and .git_sha == $sha and
	.image.digest == $digest and .schema.migration_digest == $schema and
	.schema.ordered_migrations == ["001_initial","002_guardian","reset-baseline-v1"] and
	.expected_gates == {guardian_intake:false,privacy_worker:false} and .issues == [109,274] and .ci_run_id == 123
' "$publication" >/dev/null
first=$(sha256sum "$publication" | awk '{print $1}')
generate_publication 274,109
test "$(sha256sum "$publication" | awk '{print $1}')" = "$first"

receipt=$work_dir/receipt.json
env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-v1.25.0-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" PUBLICATION_MANIFEST_SHA256=$manifest \
	RELEASE_RESULT=failed RELEASE_SLOT=blue RELEASE_FAILURE_PHASE=database_migrate TRAFFIC_SWITCHED=false ROLLBACK_PERFORMED=false \
	GUARDIAN_INTAKE_ACTIVE=false PRIVACY_WORKER_ACTIVE=false PRIVACY_WORKER_ACTIVATION_REQUIRED=false \
	RELEASE_STARTED_AT=2026-09-12T12:00:01Z RELEASE_FINISHED_AT=2026-09-12T12:00:03Z \
	RELEASE_EVIDENCE_OUTPUT="$receipt" sh "$script_dir/release-evidence.sh" receipt
jq -e '.contract == "mycfc/deployment-receipt/v1" and .result == "failed" and .failure_phase == "database_migrate" and .traffic_switched == false and .rollback_performed == false and .actual_gates == {guardian_intake:false,privacy_worker:false} and .privacy_worker_activation_required == false' "$receipt" >/dev/null

if env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha IMAGE_REPOSITORY=registry.example/mycfc IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-v1.25.0-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" PUBLICATION_MANIFEST_SHA256=$manifest \
	RELEASE_RESULT=succeeded RELEASE_SLOT=blue RELEASE_FAILURE_PHASE=database_migrate TRAFFIC_SWITCHED=true ROLLBACK_PERFORMED=false \
	GUARDIAN_INTAKE_ACTIVE=false PRIVACY_WORKER_ACTIVE=true PRIVACY_WORKER_ACTIVATION_REQUIRED=false \
	RELEASE_STARTED_AT=2026-09-12T12:00:03Z RELEASE_FINISHED_AT=2026-09-12T12:00:01Z \
	RELEASE_EVIDENCE_OUTPUT="$work_dir/rejected.json" sh "$script_dir/release-evidence.sh" receipt >/dev/null 2>&1; then
	printf '%s\n' 'internally impossible receipt was accepted' >&2
	exit 1
fi

canary='postgres://operator:secret@example.invalid/mycfc'
if env RELEASE_VERSION=v1.25.0 GIT_SHA=$sha GIT_TREE_SHA=$tree IMAGE_REPOSITORY="$canary" IMAGE_DIGEST=$digest \
	RELEASE_TAG=release-v1.25.0-20260912120000-$sha SCHEMA_MIGRATION_DIGEST="$schema" MIGRATION_INVENTORY_JSON="$migrations" \
	EXPECTED_GATES_JSON="$gates" PUBLISHED_AT=2026-09-12T12:00:00Z CI_RUN_ID=123 RELEASE_EVIDENCE_OUTPUT="$work_dir/rejected.json" \
	sh "$script_dir/release-evidence.sh" publication >/dev/null 2>&1; then
	printf '%s\n' 'credential-shaped repository was accepted' >&2
	exit 1
fi
if grep -R -Fq "$canary" "$work_dir"; then printf '%s\n' 'redaction canary reached release evidence' >&2; exit 1; fi

if generate_publication '274,invalid' >/dev/null 2>&1; then printf '%s\n' 'invalid issue allowlist was accepted' >&2; exit 1; fi

# A normal release uses the strict signed-publication/image declarations, never
# the retired destructive predecessor rehearsal. Exercise rejection across all
# independently produced evidence surfaces without a database or real image.
python3 - "$script_dir" "$work_dir" "$publication" <<'PYTHON'
import copy
import json
import os
from pathlib import Path
import subprocess
import sys

scripts, folder, publication = map(Path, sys.argv[1:])
manifest = json.loads(publication.read_text())
contract = {'contract': 'mycfc/normal-release/v1', 'version': manifest['version'],
            'candidate': manifest['git_sha'], 'database': 'mycfc',
            'parent_digest': '0' * 64, 'final_digest': manifest['schema']['migration_digest']}
cap = {'protocol': 1, 'version': manifest['version'], 'candidate': manifest['git_sha'],
       'reader_generation': 5, 'writer_generation': 2, 'snapshot_read': [1, 2],
       'schema_read': ['schedule-c1', 'schedule-p1'],
       'write': ['INTERVAL', 'START_ONLY', 'DAY', 'BAND', 'snapshot-v1', 'snapshot-v2']}

def check(accepted, contract_value=contract, cap_value=cap, manifest_value=manifest, raw=None):
    paths = [folder / name for name in ['normal-contract.json', 'normal-caps.json', 'normal-publication.json']]
    for path, value in zip(paths, [contract_value, cap_value, manifest_value]):
        path.write_text(json.dumps(value))
    if raw is not None:
        paths[0].write_text(raw)
    env = dict(os.environ, NORMAL_RELEASE_CONTRACT=str(paths[0]),
               NORMAL_RELEASE_CAPABILITIES=str(paths[1]), RELEASE_PUBLICATION_MANIFEST=str(paths[2]))
    result = subprocess.run(['sh', str(scripts / 'release-evidence.sh'), 'normal-contract'],
                            env=env, text=True, capture_output=True)
    assert (result.returncode == 0) == accepted, (accepted, result.stdout, result.stderr)
    if accepted:
        assert result.stdout == 'normal_release_contract=accepted\n'
    else:
        assert result.stderr == 'normal release contract rejected\n'

check(True)
for field, value in [('contract', 'mycfc/disposable-release/v1'), ('version', 'v99.0.0'),
                     ('candidate', 'b' * 40), ('database', 'other'), ('final_digest', 'f' * 64),
                     ('parent_digest', contract['final_digest']), ('unexpected', True)]:
    check(False, contract_value=contract | {field: value})
check(False, cap_value=cap | {'candidate': 'b' * 40})
check(False, cap_value=cap | {'reader_generation': True})
bad_manifest = copy.deepcopy(manifest)
bad_manifest['schema']['ordered_migrations'].append('999_unsigned')
check(False, manifest_value=bad_manifest)
bad_manifest = copy.deepcopy(manifest)
bad_manifest['expected_gates']['privacy_worker'] = True
check(False, manifest_value=bad_manifest)
check(False, raw=json.dumps(contract)[:-1] + ',"database":"mycfc"}')
check(False, raw=json.dumps(contract | {'parent_digest': 1.5}))
workflow = (scripts.parent / '.github/workflows/deploy.yml').read_text()
assert 'validate-disposable-candidate.py --release-image' not in workflow
assert 'docker run --rm "$IMAGE" normal-release-contract' in workflow
assert workflow.index('run: sh scripts/release-evidence.sh normal-contract') < workflow.index('- name: Publish verified application for production pickup')
for guard in ['environment: production', 'commit.verification.verified == true',
              'verification.reason == "valid"', 'gh attestation verify',
              r'.conclusion == \"success\"']:
    assert guard in workflow, guard
PYTHON

printf '%s\n' 'release-evidence tests passed'
