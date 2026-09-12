#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin" "$work_dir/git"

cat >"$work_dir/bin/git" <<'EOF'
#!/bin/sh
case "$*" in
	'rev-parse --git-dir') printf '%s\n' "$TEST_GIT_DIR" ;;
	'branch --show-current') printf '%s\n' "${TEST_BRANCH:-feature}" ;;
	'status --porcelain') ;;
	'rev-parse HEAD') printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa ;;
	'verify-commit '*) ;;
	'fetch --quiet origin main --tags') ;;
	'merge-base --is-ancestor '*) ;;
	'tag --merged '*) printf 'v1.24.1\n' ;;
	'diff --quiet '*) [ "${TEST_INFRA_CHANGED:-false}" != true ] ;;
	'rev-parse -q --verify refs/tags/'*) [ "${TEST_TAG_EXISTS:-false}" = true ] ;;
	'cat-file -t refs/tags/'*) printf 'tag\n' ;;
	'rev-list -n 1 refs/tags/'*) printf '%s\n' bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
	'verify-tag '*) ;;
	'tag -s -a '*) printf '%s\n' "$*" >>"$TEST_WRITES" ;;
	'push origin refs/tags/'*) printf '%s\n' "$*" >>"$TEST_WRITES" ;;
	'ls-remote --exit-code --tags origin refs/tags/'*) [ "${TEST_REMOTE_TAG_EXISTS:-${TEST_TAG_EXISTS:-false}}" = true ] ;;
	*) printf 'unexpected git: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat >"$work_dir/bin/gh" <<'EOF'
#!/bin/sh
case "$*" in
	'auth status') ;;
	'pr list '*)
		if [ "${TEST_PR_STATE:-OPEN}" = MERGED ]; then
			printf '[{"number":284,"state":"MERGED","url":"https://example.test/pr/284","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","mergeCommit":{"oid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}]\n'
		elif [ "${TEST_PR_STATE:-OPEN}" = NONE ]; then printf '[]\n'
		else printf '[{"number":284,"state":"OPEN","url":"https://example.test/pr/284","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","mergeCommit":null}]\n'; fi ;;
	'pr create '*) printf '%s\n' "$*" >>"$TEST_WRITES" ;;
	'pr merge '*) printf '%s\n' "$*" >>"$TEST_WRITES" ;;
	'api /repos/{owner}/{repo}/actions/workflows/ci.yml/runs'*) printf '123\n' ;;
	'api /repos/{owner}/{repo}/actions/workflows/deploy.yml/runs'*)
		case "${TEST_DEPLOY_RUN_STATE:-none}" in
			pending) printf '{"workflow_runs":[{"display_title":"deploy v1.25.0 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb issues=109,284","status":"in_progress","conclusion":null,"created_at":"2026-09-12T12:00:00Z","html_url":"https://example.test/runs/1"}]}\n' ;;
			failed) printf '{"workflow_runs":[{"display_title":"deploy v1.25.0 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb issues=109,284","status":"completed","conclusion":"failure","created_at":"2026-09-12T12:00:00Z","html_url":"https://example.test/runs/1"}]}\n' ;;
			*) printf '{"workflow_runs":[]}\n' ;;
		esac ;;
	'api /repos/{owner}/{repo}/commits/'*) printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
	'workflow run '*) printf '%s\n' "$*" >>"$TEST_WRITES" ;;
	'release view '*)
		[ "${TEST_RELEASE_EXISTS:-false}" = true ] || exit 1
		case "$*" in *'--jq .targetCommitish'*) printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;; *) printf '{}\n' ;; esac ;;
	'release download '*)
		for argument in "$@"; do destination=$argument; done
		mkdir -p "$destination"
		cat >"$destination/release-publication.json" <<JSON
{"contract":"mycfc/release-publication/v1","version":"v1.25.0","git_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","issues":[109,284]}
JSON
		manifest=$(sha256sum "$destination/release-publication.json" | awk '{print $1}')
		jq -cn --arg manifest "$manifest" '{contract:"mycfc/deployment-receipt/v1",version:"v1.25.0",git_sha:("b"*40),publication_manifest_sha256:$manifest,result:"succeeded",failure_phase:null,rollback_performed:false}' >"$destination/receipt.tmp"
		receipt_sha=$(sha256sum "$destination/receipt.tmp" | awk '{print $1}')
		mv "$destination/receipt.tmp" "$destination/deployment-receipt-20260912120000-$receipt_sha.json"
		;;
	*) printf 'unexpected gh: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
chmod +x "$work_dir/bin/git" "$work_dir/bin/gh"
: >"$work_dir/writes"

run_release() {
	test_tag_exists=${TEST_TAG_EXISTS:-false}
	test_remote_tag_exists=${TEST_REMOTE_TAG_EXISTS:-$test_tag_exists}
	PATH="$work_dir/bin:$PATH" TEST_GIT_DIR="$work_dir/git" TEST_WRITES="$work_dir/writes" \
		TEST_BRANCH=feature TEST_PR_STATE="${TEST_PR_STATE:-OPEN}" TEST_INFRA_CHANGED="${TEST_INFRA_CHANGED:-false}" \
		TEST_TAG_EXISTS="$test_tag_exists" TEST_RELEASE_EXISTS="${TEST_RELEASE_EXISTS:-false}" \
		TEST_REMOTE_TAG_EXISTS="$test_remote_tag_exists" \
		TEST_DEPLOY_RUN_STATE="${TEST_DEPLOY_RUN_STATE:-none}" \
		VERSION=v1.25.0 ISSUES=284,109 sh "$script_dir/release.sh"
}

if PATH="$work_dir/bin:$PATH" VERSION=v1.25.0 ISSUES=284,invalid sh "$script_dir/release.sh" >/dev/null 2>&1; then
	printf '%s\n' 'invalid release issue allowlist was accepted' >&2
	exit 1
fi

set +e
open_output=$(run_release 2>&1)
open_status=$?
set -e
[ "$open_status" -eq 3 ]
printf '%s\n' "$open_output" | grep -q '^gate=merge_required$'
[ ! -s "$work_dir/writes" ]

set +e
merge_output=$(RELEASE_MERGE_APPROVED=true run_release 2>&1)
merge_status=$?
set -e
[ "$merge_status" -eq 3 ]
printf '%s\n' "$merge_output" | grep -q '^gate=merge_verification_pending$'
grep -q '^pr merge 284 --merge$' "$work_dir/writes"
: >"$work_dir/writes"

set +e
infra_output=$(TEST_PR_STATE=MERGED TEST_INFRA_CHANGED=true run_release 2>&1)
infra_status=$?
set -e
[ "$infra_status" -eq 3 ]
printf '%s\n' "$infra_output" | grep -q '^gate=infrastructure_apply_required$'
[ ! -s "$work_dir/writes" ]

set +e
publish_output=$(TEST_PR_STATE=MERGED RELEASE_INFRA_APPLIED=true run_release 2>&1)
publish_status=$?
set -e
[ "$publish_status" -eq 3 ]
printf '%s\n' "$publish_output" | grep -q '^gate=publish_deploy_required$'
[ ! -s "$work_dir/writes" ]

set +e
pending_output=$(TEST_PR_STATE=MERGED RELEASE_INFRA_APPLIED=true RELEASE_PUBLISH_DEPLOY_APPROVED=true run_release 2>&1)
pending_status=$?
set -e
[ "$pending_status" -eq 3 ]
printf '%s\n' "$pending_output" | grep -q '^gate=deployment_verification_pending$'
grep -q '^tag -s -a v1.25.0 ' "$work_dir/writes"
grep -q '^push origin refs/tags/v1.25.0$' "$work_dir/writes"
grep -q 'workflow run deploy.yml .*issues=109,284' "$work_dir/writes"

: >"$work_dir/writes"
set +e
duplicate_output=$(TEST_PR_STATE=MERGED RELEASE_INFRA_APPLIED=true RELEASE_PUBLISH_DEPLOY_APPROVED=true \
	TEST_TAG_EXISTS=true TEST_DEPLOY_RUN_STATE=pending run_release 2>&1)
duplicate_status=$?
set -e
[ "$duplicate_status" -eq 3 ]
printf '%s\n' "$duplicate_output" | grep -q 'action=wait_for_existing_exact_deployment'
[ ! -s "$work_dir/writes" ]

delivered_output=$(TEST_PR_STATE=MERGED RELEASE_INFRA_APPLIED=true TEST_TAG_EXISTS=true TEST_RELEASE_EXISTS=true run_release)
printf '%s\n' "$delivered_output" | grep -q '^state=delivered$'
printf '%s\n' "$delivered_output" | grep -q '^gate=activation_separate$'
jq -e '.phase == "delivered" and .issues == [109,284]' "$work_dir/git/mycfc-release-v1.25.0.json" >/dev/null

printf '%s\n' 'release orchestration tests passed'
