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
	'branch --show-current') printf '%s\n' "${TEST_BRANCH:-main}" ;;
	'status --porcelain') [ "${TEST_DIRTY:-false}" != true ] || printf ' M file\n' ;;
	'rev-parse HEAD') printf '%s\n' bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
	'rev-parse origin/main') printf '%s\n' "${TEST_ORIGIN_MAIN:-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb}" ;;
	'fetch --quiet origin main --tags') ;;
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
	'api /repos/{owner}/{repo}/commits/'*) printf 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
	'api /repos/{owner}/{repo}/actions/workflows/ci.yml/runs'*)
		if [ "${TEST_CI_GREEN:-true}" = true ]; then printf '123\n'; else printf '\n'; fi ;;
	'api /repos/{owner}/{repo}/actions/workflows/deploy.yml/runs'*)
		case "${TEST_DEPLOY_RUN_STATE:-none}" in
			pending) printf '{"workflow_runs":[{"display_title":"deploy v1.25.0 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb issues=109,284","status":"in_progress","conclusion":null,"created_at":"2026-09-12T12:00:00Z","html_url":"https://example.test/runs/1"}]}\n' ;;
			failed) printf '{"workflow_runs":[{"display_title":"deploy v1.25.0 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb issues=109,284","status":"completed","conclusion":"failure","created_at":"2026-09-12T12:00:00Z","html_url":"https://example.test/runs/1"}]}\n' ;;
			*) printf '{"workflow_runs":[]}\n' ;;
		esac ;;
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
	PATH="$work_dir/bin:$PATH" TEST_GIT_DIR="$work_dir/git" TEST_WRITES="$work_dir/writes" \
		TEST_BRANCH="${TEST_BRANCH:-main}" TEST_TAG_EXISTS="$test_tag_exists" \
		TEST_RELEASE_EXISTS="${TEST_RELEASE_EXISTS:-false}" \
		TEST_REMOTE_TAG_EXISTS="${TEST_REMOTE_TAG_EXISTS:-$test_tag_exists}" \
		TEST_DEPLOY_RUN_STATE="${TEST_DEPLOY_RUN_STATE:-none}" \
		TEST_CI_GREEN="${TEST_CI_GREEN:-true}" \
		TEST_ORIGIN_MAIN="${TEST_ORIGIN_MAIN:-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb}" \
		VERSION=v1.25.0 ISSUES=284,109 sh "$script_dir/release.sh"
}

# Invalid issues rejected
if PATH="$work_dir/bin:$PATH" VERSION=v1.25.0 ISSUES=284,invalid sh "$script_dir/release.sh" >/dev/null 2>&1; then
	printf '%s\n' 'invalid release issue allowlist was accepted' >&2
	exit 1
fi

# Must run from main
set +e
branch_output=$(TEST_BRANCH=feature run_release 2>&1)
branch_status=$?
set -e
[ "$branch_status" -ne 0 ]
printf '%s\n' "$branch_output" | grep -q 'run release from main'

# Dirty tree rejected
set +e
dirty_output=$(TEST_DIRTY=true run_release 2>&1)
dirty_status=$?
set -e
[ "$dirty_status" -ne 0 ]
printf '%s\n' "$dirty_output" | grep -q 'working tree must be clean'

# Local main must match origin/main
set +e
diverged_output=$(TEST_ORIGIN_MAIN=cccccccccccccccccccccccccccccccccccccccc run_release 2>&1)
diverged_status=$?
set -e
[ "$diverged_status" -ne 0 ]
printf '%s\n' "$diverged_output" | grep -q 'does not match origin/main'

# CI must be green
set +e
ci_output=$(TEST_CI_GREEN=false run_release 2>&1)
ci_status=$?
set -e
[ "$ci_status" -ne 0 ]
printf '%s\n' "$ci_output" | grep -q 'no successful CI'

# Happy path: tag + dispatch
: >"$work_dir/writes"
dispatch_output=$(run_release)
printf '%s\n' "$dispatch_output" | grep -q '^state=deploy_dispatched$'
grep -q '^tag -s -a v1.25.0 ' "$work_dir/writes"
grep -q '^push origin refs/tags/v1.25.0$' "$work_dir/writes"
grep -q 'workflow run deploy.yml' "$work_dir/writes"

# Existing pending deploy does not re-dispatch
: >"$work_dir/writes"
pending_output=$(TEST_TAG_EXISTS=true TEST_DEPLOY_RUN_STATE=pending run_release)
printf '%s\n' "$pending_output" | grep -q '^state=deploy_in_progress$'
[ ! -s "$work_dir/writes" ]

# Failed deploy is explicit
set +e
failed_output=$(TEST_TAG_EXISTS=true TEST_DEPLOY_RUN_STATE=failed run_release 2>&1)
failed_status=$?
set -e
[ "$failed_status" -ne 0 ]
printf '%s\n' "$failed_output" | grep -q 'concluded failure'

# Delivered when release assets exist
delivered_output=$(TEST_TAG_EXISTS=true TEST_RELEASE_EXISTS=true run_release)
printf '%s\n' "$delivered_output" | grep -q '^state=delivered$'
printf '%s\n' "$delivered_output" | grep -q 'activation'

printf '%s\n' 'release orchestration tests passed'
