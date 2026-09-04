#!/usr/bin/env bash
set -Eeuo pipefail

event_name=${1:-}
base_sha=${2:-}
head_sha=${3:-}

full_ci() {
	printf '%s\n' 'mode=full'
	exit 0
}

# Full CI is mandatory outside pull requests, including every push to main.
[[ $event_name == pull_request ]] || full_ci

# Missing refs, shallow/incomplete history, or any diff failure are ambiguous and
# must take the full path. Classification is an optimization, never a gate bypass.
if [[ -z $base_sha || -z $head_sha ]] ||
	! git cat-file -e "$base_sha^{commit}" 2>/dev/null ||
	! git cat-file -e "$head_sha^{commit}" 2>/dev/null ||
	! git merge-base "$base_sha" "$head_sha" >/dev/null 2>&1; then
	printf '%s\n' 'warning: unable to resolve CI classification refs; running full CI' >&2
	full_ci
fi

changed_paths=$(mktemp)
trap 'rm -f "$changed_paths"' EXIT HUP INT TERM
if ! git diff --no-renames --name-only -z "$base_sha...$head_sha" >"$changed_paths"; then
	printf '%s\n' 'warning: unable to compute CI change set; running full CI' >&2
	full_ci
fi
[[ -s $changed_paths ]] || full_ci

while IFS= read -r -d '' changed_path; do
	case "$changed_path" in
		AGENTS.md | */AGENTS.md) full_ci ;;
		README.md | SECURITY.md | docs/*.md) ;;
		*) full_ci ;;
	esac
done <"$changed_paths"

printf '%s\n' 'mode=docs'
