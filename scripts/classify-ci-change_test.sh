#!/usr/bin/env bash
set -Eeuo pipefail

root_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
classifier="$root_dir/scripts/classify-ci-change.sh"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
repository="$work_dir/repository"

git init -q "$repository"
git -C "$repository" config user.name 'MyCFC CI test'
git -C "$repository" config user.email 'ci@example.test'
git -C "$repository" config commit.gpgSign false

commit_changes() {
	git -C "$repository" add -A
	git -C "$repository" commit -qm "$1"
}

assert_diff() {
	expected=$1
	base_sha=$2
	head_sha=$3
	event_name=${4:-pull_request}
	actual=$(cd "$repository" && "$classifier" "$event_name" "$base_sha" "$head_sha")
	if [[ $actual != "mode=$expected" ]]; then
		printf 'expected mode=%s, got %s for %s..%s (%s)\n' "$expected" "$actual" "$base_sha" "$head_sha" "$event_name" >&2
		exit 1
	fi
}

mkdir -p "$repository/docs" "$repository/internal"
printf '# Project\n' >"$repository/README.md"
printf '# Terms\n' >"$repository/docs/terms.md"
printf 'package internal\n' >"$repository/internal/example.go"
commit_changes baseline

base=$(git -C "$repository" rev-parse HEAD)
printf '\nMore documentation.\n' >>"$repository/docs/terms.md"
commit_changes docs
head=$(git -C "$repository" rev-parse HEAD)
assert_diff docs "$base" "$head"

base=$head
printf '# Security\n' >"$repository/SECURITY.md"
mkdir -p "$repository/docs/nested"
printf '# Nested\n' >"$repository/docs/nested/guide.md"
commit_changes allowed_docs
head=$(git -C "$repository" rev-parse HEAD)
assert_diff docs "$base" "$head"

base=$head
printf '\nMixed.\n' >>"$repository/docs/terms.md"
printf 'package changed\n' >"$repository/internal/example.go"
commit_changes mixed
head=$(git -C "$repository" rev-parse HEAD)
assert_diff full "$base" "$head"

for unsafe_path in go.mod package-lock.json .node-version .github/workflows/ci.yml .github/CODEOWNERS Dockerfile infra/bootstrap/main.tf scripts/example.sh scripts/classify-ci-change.sh Makefile AGENTS.md docs/AGENTS.md docs/nested/AGENTS.md ui/static/dist/manifest.json docs/diagram.svg docs/architecture.dot docs/example.png docs/prototype.html; do
	base=$head
	mkdir -p "$repository/$(dirname -- "$unsafe_path")"
	printf 'change\n' >"$repository/$unsafe_path"
	commit_changes unsafe_path
	head=$(git -C "$repository" rev-parse HEAD)
	assert_diff full "$base" "$head"
done

printf 'source\n' >"$repository/internal/renamed.go"
commit_changes rename_source
source_head=$(git -C "$repository" rev-parse HEAD)
mkdir -p "$repository/docs/archive"
mv "$repository/internal/renamed.go" "$repository/docs/archive/renamed.md"
commit_changes code_to_docs_rename
head=$(git -C "$repository" rev-parse HEAD)
assert_diff full "$source_head" "$head"

base=$head
mv "$repository/docs/terms.md" "$repository/docs/nested/terms.md"
commit_changes docs_rename
head=$(git -C "$repository" rev-parse HEAD)
assert_diff docs "$base" "$head"

base=$head
rm "$repository/docs/nested/terms.md"
commit_changes docs_delete
head=$(git -C "$repository" rev-parse HEAD)
assert_diff docs "$base" "$head"

assert_diff full "$head" "$head"
assert_diff full missing "$head"
assert_diff full "$base" "$head" push

base=$head
printf 'unusual\n' >"$repository/internal/line
break.go"
commit_changes unusual_filename
head=$(git -C "$repository" rev-parse HEAD)
assert_diff full "$base" "$head"

printf '%s\n' 'CI change-classifier tests passed'
