#!/bin/sh
# Simplified MyCFC release: from a clean, CI-green main tip, create a signed
# annotated tag, push it, and dispatch deploy.yml. The only production human
# gate is the GitHub Environment approval on deploy.yml.
set -eu

fail() {
	printf 'release rejected: %s\n' "$1" >&2
	exit 1
}

info() {
	printf '%s\n' "$*"
}

version=${VERSION:-${1:-}}
issues=${ISSUES:-}
printf '%s' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' || fail 'set VERSION to a canonical semantic version (for example v1.26.0)'
if [ -n "$issues" ]; then
	printf '%s' "$issues" | grep -Eq '^[1-9][0-9]*(,[1-9][0-9]*)*$' || fail 'ISSUES must contain positive issue numbers separated by commas'
	issues=$(printf '%s\n' "$issues" | tr ',' '\n' | awk '!seen[$0]++ { print $0 }' | sort -n | paste -sd, -)
fi
for command in git gh jq find sha256sum; do command -v "$command" >/dev/null 2>&1 || fail "missing command: $command"; done

git rev-parse --git-dir >/dev/null 2>&1 || fail 'not inside a Git repository'
branch=$(git branch --show-current)
[ -n "$branch" ] || fail 'detached HEAD is not releasable'
[ "$branch" = main ] || fail 'run release from main after the change is merged'

if [ -n "$(git status --porcelain)" ]; then fail 'working tree must be clean'; fi
gh auth status >/dev/null 2>&1 || fail 'GitHub CLI authentication is required'

git fetch --quiet origin main --tags
release_sha=$(git rev-parse HEAD)
origin_main=$(git rev-parse origin/main)
[ "$release_sha" = "$origin_main" ] || fail "local main ($release_sha) does not match origin/main ($origin_main); pull or push first"

verified_commit=$(gh api "/repos/{owner}/{repo}/commits/$release_sha" \
	--jq 'select(.commit.verification.verified == true and .commit.verification.reason == "valid") | .sha')
[ "$verified_commit" = "$release_sha" ] || fail 'HEAD commit signature is not valid on GitHub'

ci_run=$(gh api "/repos/{owner}/{repo}/actions/workflows/ci.yml/runs?branch=main&event=push&status=completed&per_page=100" \
	--jq ".workflow_runs[] | select(.head_sha == \"$release_sha\" and .head_branch == \"main\" and .event == \"push\" and .conclusion == \"success\") | .id" | head -n 1)
[ -n "$ci_run" ] || fail "no successful CI push run for $release_sha; wait for CI on main"

# Create and push signed annotated tag when missing.
if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
	test "$(git cat-file -t "refs/tags/$version")" = tag || fail 'release tag exists but is not annotated'
	test "$(git rev-list -n 1 "refs/tags/$version")" = "$release_sha" || fail "tag $version points at a different commit"
	git verify-tag "$version" >/dev/null 2>&1 || fail 'existing release tag signature is not valid locally'
	info "tag $version already points at $release_sha"
else
	git tag -s -a "$version" "$release_sha" -m "MyCFC $version"
	git verify-tag "$version" >/dev/null 2>&1 || fail 'new release tag signature did not verify'
	git push origin "refs/tags/$version"
	info "created and pushed signed tag $version -> $release_sha"
fi

if ! git ls-remote --exit-code --tags origin "refs/tags/$version" >/dev/null 2>&1; then
	git push origin "refs/tags/$version"
fi

# If a GitHub release with evidence already exists, verify and exit delivered.
if gh release view "$version" --json tagName,targetCommitish >/dev/null 2>&1; then
	release_target=$(gh release view "$version" --json targetCommitish --jq .targetCommitish)
	case "$release_target" in "$release_sha"|"$version") ;; *) fail 'published release target does not match HEAD' ;; esac
	evidence_dir=$(mktemp -d)
	trap 'rm -rf "$evidence_dir"' EXIT HUP INT TERM
	gh release download "$version" --pattern release-publication.json --pattern 'deployment-receipt-*.json' --dir "$evidence_dir"
	publication="$evidence_dir/release-publication.json"
	receipt=$(find "$evidence_dir" -maxdepth 1 -type f -name 'deployment-receipt-*.json' -print | sort | tail -n 1)
	[ -n "$receipt" ] || fail 'GitHub release has no deployment receipt yet; wait for deploy.yml'
	receipt_name=${receipt##*/}
	receipt_file_sha=$(sha256sum "$receipt" | awk '{print $1}')
	case "$receipt_name" in deployment-receipt-??????????????-"$receipt_file_sha".json) ;; *) fail 'deployment receipt asset name does not bind its contents' ;; esac
	expected_issues=$(printf '%s\n' "$issues" | tr ',' '\n' | awk 'NF { print $0 }' | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)')
	jq -e --arg version "$version" --arg sha "$release_sha" --argjson issues "$expected_issues" '
		.contract == "mycfc/release-publication/v1" and .version == $version and .git_sha == $sha and
		($issues == [] or .issues == $issues)
	' "$publication" >/dev/null || fail 'publication manifest does not match this release identity'
	manifest_sha=$(sha256sum "$publication" | awk '{print $1}')
	jq -e --arg version "$version" --arg sha "$release_sha" --arg manifest "$manifest_sha" '
		.contract == "mycfc/deployment-receipt/v1" and .version == $version and .git_sha == $sha and
		.publication_manifest_sha256 == $manifest and .result == "succeeded" and .failure_phase == null and .rollback_performed == false
	' "$receipt" >/dev/null || fail 'deployment receipt does not prove a successful production deploy'
	rm -rf "$evidence_dir"
	trap - EXIT HUP INT TERM
	info "state=delivered"
	info "version=$version"
	info "sha=$release_sha"
	info "issues=${issues:-none}"
	info "note=feature activation (guardian, etc.) remains a separate operator procedure"
	exit 0
fi

# Dispatch deploy when no release record exists yet.
run_name="deploy $version $release_sha issues=$issues"
runs=$(gh api '/repos/{owner}/{repo}/actions/workflows/deploy.yml/runs?event=workflow_dispatch&per_page=50')
exact_runs=$(printf '%s' "$runs" | jq -c --arg name "$run_name" '[.workflow_runs[] | select(.display_title == $name)]')
pending_run=$(printf '%s' "$exact_runs" | jq -r '[.[] | select(.status == "queued" or .status == "in_progress" or .status == "waiting" or .status == "requested" or .status == "pending")] | sort_by(.created_at) | last | .html_url // empty')
if [ -n "$pending_run" ]; then
	info "state=deploy_in_progress"
	info "version=$version"
	info "sha=$release_sha"
	info "run=$pending_run"
	info "action=approve the production environment if prompted, then re-run this command to verify delivery"
	exit 0
fi

completed_conclusion=$(printf '%s' "$exact_runs" | jq -r '[.[] | select(.status == "completed")] | sort_by(.created_at) | last | .conclusion // empty')
if [ "$completed_conclusion" = success ]; then
	info "state=deploy_succeeded_waiting_for_release_assets"
	info "version=$version"
	info "sha=$release_sha"
	info "action=re-run once the GitHub release assets appear"
	exit 0
fi
if [ -n "$completed_conclusion" ] && [ "$completed_conclusion" != success ]; then
	fail "previous deploy for this exact identity concluded $completed_conclusion; fix and re-run make release after addressing the failure"
fi

gh workflow run deploy.yml --ref main -f "sha=$release_sha" -f "version=$version" -f "issues=$issues"
info "state=deploy_dispatched"
info "version=$version"
info "sha=$release_sha"
info "issues=${issues:-none}"
info "action=approve the GitHub Environment 'production' when prompted, then re-run make release VERSION=$version to confirm delivery"
