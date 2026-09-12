#!/bin/sh
set -eu

stop_gate() {
	gate=$1
	shift
	write_state "$gate"
	case "$gate" in
		merge_required) packet_kind=merge ;;
		infrastructure_apply_required) packet_kind=infrastructure ;;
		publish_deploy_required) packet_kind=release ;;
		*) packet_kind= ;;
	esac
	if [ -n "$packet_kind" ]; then
		packet="$git_dir/mycfc-$version-$packet_kind-approval.md"
		APPROVAL_SUMMARY="Resume the $version delivery at the $packet_kind gate." \
			APPROVAL_IDENTITY="version=$version; commit=${release_sha:-pending}; issues=${issues:-none}" \
			APPROVAL_PREREQUISITES="All preceding checkpoints shown by this command must pass." \
			APPROVAL_USER_IMPACT="Only the named $packet_kind action is authorized." \
			APPROVAL_IRREVERSIBLE_EFFECTS="See the gate-specific evidence before deciding." \
			APPROVAL_EVIDENCE="The command revalidates remote evidence when resumed." \
			APPROVAL_MISSING_EVIDENCE="Any missing checkpoint keeps the command stopped." \
			APPROVAL_SCOPE="No later gate, data purge, or feature activation is included." \
			RELEASE_VERSION="$version" GIT_SHA="${release_sha:-not-applicable}" \
			sh "$script_dir/approval-packet.sh" "$packet_kind" "$packet"
		printf 'approval_packet=%s\n' "$packet"
	fi
	printf 'gate=%s\n' "$gate"
	for field in "$@"; do printf '%s\n' "$field"; done
	exit 3
}

fail() {
	printf 'release orchestration rejected: %s\n' "$1" >&2
	exit 1
}

write_state() {
	phase=$1
	[ -n "${state_file:-}" ] || return 0
	temporary=$(mktemp "${state_file}.tmp.XXXXXX")
	jq -cS -n --arg contract 'mycfc/release-orchestration-state/v1' --arg version "$version" \
		--arg branch "$branch" --arg phase "$phase" --arg sha "${release_sha:-}" --arg issues "$issues" \
		'{contract:$contract,version:$version,branch:$branch,phase:$phase,release_sha:(if $sha == "" then null else $sha end),issues:($issues | split(",") | map(select(length > 0) | tonumber))}' >"$temporary"
	chmod 0600 "$temporary"
	mv "$temporary" "$state_file"
}

version=${VERSION:-${1:-}}
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
issues=${ISSUES:-}
printf '%s' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' || fail 'set VERSION to a canonical semantic version'
if [ -n "$issues" ]; then
	printf '%s' "$issues" | grep -Eq '^[1-9][0-9]*(,[1-9][0-9]*)*$' || fail 'ISSUES must contain positive issue numbers separated by commas'
	issues=$(printf '%s\n' "$issues" | tr ',' '\n' | awk '!seen[$0]++ { print $0 }' | sort -n | paste -sd, -)
fi
for command in git gh jq find sha256sum; do command -v "$command" >/dev/null 2>&1 || fail "missing command: $command"; done

git_dir=$(git rev-parse --git-dir 2>/dev/null) || fail 'not inside a Git repository'
branch=$(git branch --show-current)
[ -n "$branch" ] || fail 'detached HEAD is not releasable'
state_file="$git_dir/mycfc-release-$version.json"

if [ -f "$state_file" ]; then
	jq -e --arg version "$version" --arg branch "$branch" --arg issues "$issues" '
		.contract == "mycfc/release-orchestration-state/v1" and .version == $version and .branch == $branch and
		.issues == ($issues | split(",") | map(select(length > 0) | tonumber)) and
		(.release_sha == null or (.release_sha | test("^[0-9a-f]{40}$")))
	' "$state_file" >/dev/null || fail 'saved release state does not match this request'
	saved_release_sha=$(jq -r '.release_sha // empty' "$state_file")
	saved_phase=$(jq -r .phase "$state_file")
else
	saved_release_sha=
	saved_phase=
fi

if [ -n "$(git status --porcelain)" ]; then fail 'working tree must be clean'; fi
gh auth status >/dev/null 2>&1 || fail 'GitHub CLI authentication is required'

pr_json=$(gh pr list --head "$branch" --state all --limit 1 --json number,state,url,mergeCommit,headRefOid 2>/dev/null || printf '[]')
pr_number=$(printf '%s' "$pr_json" | jq -r '.[0].number // empty')
if [ "$branch" != main ] && [ -z "$pr_number" ]; then
	if [ "${RELEASE_GITHUB_WRITE_APPROVED:-false}" != true ]; then
		stop_gate github_write_required "action=create_pull_request" "branch=$branch"
	fi
	title=${RELEASE_TITLE:-"Release $version"}
	body=$(mktemp)
	trap 'rm -f "$body"' EXIT HUP INT TERM
	{
		echo "Prepare $version for verified production delivery."
		echo
		echo "Related issues: ${issues:-none supplied}."
		echo
		echo 'Merge, deployment, infrastructure apply, and feature activation remain separate approval gates.'
	} >"$body"
	gh pr create --base main --head "$branch" --title "$title" --body-file "$body" >/dev/null
	rm -f "$body"
	trap - EXIT HUP INT TERM
	pr_json=$(gh pr list --head "$branch" --state all --limit 1 --json number,state,url,mergeCommit,headRefOid)
	pr_number=$(printf '%s' "$pr_json" | jq -r '.[0].number // empty')
	[ -n "$pr_number" ] || fail 'created pull request could not be resolved'
fi

if [ "$branch" = main ]; then
	release_sha=$(git rev-parse HEAD)
else
	pr_state=$(printf '%s' "$pr_json" | jq -r '.[0].state // empty')
	pr_url=$(printf '%s' "$pr_json" | jq -r '.[0].url // empty')
	if [ "$pr_state" != MERGED ]; then
		release_sha=$(printf '%s' "$pr_json" | jq -r '.[0].headRefOid // empty')
		printf '%s' "$release_sha" | grep -Eq '^[0-9a-f]{40}$' || fail 'open pull request has no exact head commit'
		pr_ci=$(gh api "/repos/{owner}/{repo}/actions/workflows/ci.yml/runs?branch=$branch&event=pull_request&status=completed&per_page=100" \
			--jq ".workflow_runs[] | select(.head_sha == \"$release_sha\" and .event == \"pull_request\" and .conclusion == \"success\") | .id" | head -n 1)
		[ -n "$pr_ci" ] || stop_gate ci_required "sha=$release_sha" "action=wait_for_canonical_pull_request_ci"
		if [ "${RELEASE_MERGE_APPROVED:-false}" != true ]; then
			stop_gate merge_required "pull_request=$pr_number" "url=$pr_url" "action=merge_after_explicit_approval"
		fi
		gh pr merge "$pr_number" --merge
		stop_gate merge_verification_pending "pull_request=$pr_number" "action=rerun_after_merge_is_visible"
	fi
	release_sha=$(printf '%s' "$pr_json" | jq -r '.[0].mergeCommit.oid // empty')
	printf '%s' "$release_sha" | grep -Eq '^[0-9a-f]{40}$' || fail 'merged pull request has no merge commit'
fi

git fetch --quiet origin main --tags
git merge-base --is-ancestor "$release_sha" origin/main || fail 'release commit is not on origin/main'
case "$saved_phase" in merge_required|merge_verification_pending|ci_required) ;; *)
	if [ -n "$saved_release_sha" ] && [ "$saved_release_sha" != "$release_sha" ]; then fail 'saved release identity changed'; fi
esac
verified_commit=$(gh api "/repos/{owner}/{repo}/commits/$release_sha" \
	--jq 'select(.commit.verification.verified == true and .commit.verification.reason == "valid") | .sha')
[ "$verified_commit" = "$release_sha" ] || fail 'release commit signature is not valid on GitHub'
ci_run=$(gh api "/repos/{owner}/{repo}/actions/workflows/ci.yml/runs?branch=main&event=push&status=completed&per_page=100" \
	--jq ".workflow_runs[] | select(.head_sha == \"$release_sha\" and .head_branch == \"main\" and .event == \"push\" and .conclusion == \"success\") | .id" | head -n 1)
[ -n "$ci_run" ] || stop_gate ci_required "sha=$release_sha" "action=wait_for_successful_ci"

previous_tag=$(git tag --merged "$release_sha" --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+' | grep -Fxv "$version" | head -n 1 || true)
if [ -n "$previous_tag" ] && git diff --quiet "$previous_tag..$release_sha" -- infra/; then
	infra_changed=false
else
	infra_changed=true
fi
if [ "$infra_changed" = true ] && [ "${RELEASE_INFRA_APPLIED:-false}" != true ]; then
	stop_gate infrastructure_apply_required "from=${previous_tag:-none}" "sha=$release_sha" "action=review_plan_and_apply_outside_release_command"
fi

if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
	test "$(git cat-file -t "refs/tags/$version")" = tag || fail 'release tag exists but is not annotated'
	test "$(git rev-list -n 1 "refs/tags/$version")" = "$release_sha" || fail 'release tag points to another commit'
	git verify-tag "$version" >/dev/null 2>&1 || fail 'release tag signature is not valid locally'
else
	if [ "${RELEASE_PUBLISH_DEPLOY_APPROVED:-false}" != true ]; then
		stop_gate publish_deploy_required "version=$version" "sha=$release_sha" "issues=${issues:-none}" "action=sign_tag_push_and_dispatch"
	fi
	git tag -s -a "$version" "$release_sha" -m "MyCFC $version"
	git verify-tag "$version" >/dev/null 2>&1 || fail 'new release tag signature did not verify'
	git push origin "refs/tags/$version"
fi

if ! git ls-remote --exit-code --tags origin "refs/tags/$version" >/dev/null 2>&1; then
	[ "${RELEASE_PUBLISH_DEPLOY_APPROVED:-false}" = true ] || stop_gate publish_deploy_required "version=$version" "sha=$release_sha" "action=push_signed_tag_and_dispatch"
	git push origin "refs/tags/$version"
fi

if ! gh release view "$version" --json tagName,targetCommitish >/dev/null 2>&1; then
	run_name="deploy $version $release_sha issues=$issues"
	runs=$(gh api '/repos/{owner}/{repo}/actions/workflows/deploy.yml/runs?event=workflow_dispatch&per_page=100')
	exact_runs=$(printf '%s' "$runs" | jq -c --arg name "$run_name" '[.workflow_runs[] | select(.display_title == $name)]')
	pending_run=$(printf '%s' "$exact_runs" | jq -r '[.[] | select(.status == "queued" or .status == "in_progress" or .status == "waiting" or .status == "requested" or .status == "pending")] | sort_by(.created_at) | last | .html_url // empty')
	if [ -n "$pending_run" ]; then
		stop_gate deployment_verification_pending "version=$version" "sha=$release_sha" "run=$pending_run" "action=wait_for_existing_exact_deployment"
	fi
	completed_conclusion=$(printf '%s' "$exact_runs" | jq -r '[.[] | select(.status == "completed")] | sort_by(.created_at) | last | .conclusion // empty')
	if [ "$completed_conclusion" = success ]; then
		stop_gate deployment_verification_pending "version=$version" "sha=$release_sha" "action=wait_for_release_record_from_successful_exact_deployment"
	fi
	if [ -n "$completed_conclusion" ] && [ "$completed_conclusion" != success ] && [ "${RELEASE_DEPLOY_RETRY_APPROVED:-false}" != true ]; then
		stop_gate deployment_retry_required "version=$version" "sha=$release_sha" "last_conclusion=$completed_conclusion" "action=approve_exact_deployment_retry"
	fi
	if [ "${RELEASE_PUBLISH_DEPLOY_APPROVED:-false}" = true ]; then
		gh workflow run deploy.yml --ref main -f "sha=$release_sha" -f "version=$version" -f "issues=$issues"
	fi
	stop_gate deployment_verification_pending "version=$version" "sha=$release_sha" "action=wait_for_exact_deployment"
fi
release_target=$(gh release view "$version" --json targetCommitish --jq .targetCommitish)
case "$release_target" in "$release_sha"|"$version") ;; *) fail 'published release target does not match the approved release' ;; esac
evidence_dir=$(mktemp -d)
trap 'rm -rf "$evidence_dir"' EXIT HUP INT TERM
gh release download "$version" --pattern release-publication.json --pattern 'deployment-receipt-*.json' --dir "$evidence_dir"
publication="$evidence_dir/release-publication.json"
receipt=$(find "$evidence_dir" -maxdepth 1 -type f -name 'deployment-receipt-*.json' -print | sort | tail -n 1)
[ -n "$receipt" ] || fail 'GitHub release has no immutable deployment receipt'
receipt_name=${receipt##*/}
receipt_file_sha=$(sha256sum "$receipt" | awk '{print $1}')
case "$receipt_name" in deployment-receipt-??????????????-"$receipt_file_sha".json) ;; *) fail 'deployment receipt asset name does not bind its contents' ;; esac
expected_issues=$(printf '%s\n' "$issues" | tr ',' '\n' | awk 'NF { print $0 }' | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)')
jq -e --arg version "$version" --arg sha "$release_sha" --argjson issues "$expected_issues" '
	.contract == "mycfc/release-publication/v1" and .version == $version and .git_sha == $sha and .issues == $issues
' "$publication" >/dev/null || fail 'GitHub publication manifest does not match the approved identity'
manifest_sha=$(sha256sum "$publication" | awk '{print $1}')
jq -e --arg version "$version" --arg sha "$release_sha" --arg manifest "$manifest_sha" '
	.contract == "mycfc/deployment-receipt/v1" and .version == $version and .git_sha == $sha and
	.publication_manifest_sha256 == $manifest and .result == "succeeded" and .failure_phase == null and .rollback_performed == false
' "$receipt" >/dev/null || fail 'GitHub deployment receipt does not prove the approved production identity'
rm -rf "$evidence_dir"
trap - EXIT HUP INT TERM
write_state delivered
printf 'state=delivered\nversion=%s\nsha=%s\nissues=%s\n' "$version" "$release_sha" "${issues:-none}"
printf 'gate=activation_separate\naction=use_the_feature_specific_approval_packet_and_live_authorization\n'
