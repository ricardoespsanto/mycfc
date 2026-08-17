#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
fake_bin="$work_dir/bin"
mkdir -p "$fake_bin"

sha=0123456789012345678901234567890123456789
digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
other_digest=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc

cat >"$fake_bin/aws" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
tag=$(printf '%s\n' "$*" | sed -n 's/.*imageTag=\([^ ]*\).*/\1/p')
case "$tag" in
git-*)
	[ -f "$TEST_GIT_STATE_FILE" ] || { printf '%s\n' 'An error occurred (ImageNotFoundException)' >&2; exit 255; }
	printf '%s\n' "$TEST_GIT_DIGEST"
	;;
release-*)
	case "${TEST_RELEASE_STATE:-auto}" in
		auto)
			[ -f "$TEST_RELEASE_STATE_FILE" ] || { printf '%s\n' 'An error occurred (ImageNotFoundException)' >&2; exit 255; }
			printf '%s\n' "$TEST_GIT_DIGEST"
			;;
		same) printf '%s\n' "$TEST_GIT_DIGEST" ;;
		other) printf '%s\n' "$TEST_OTHER_DIGEST" ;;
		esac
	;;
*) printf '%s\n' "unexpected AWS tag: $tag" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
case "$1" in
build|tag|pull) exit 0 ;;
push)
	case "$2" in
	*:git-*)
		: >"$TEST_GIT_STATE_FILE"
		[ "${TEST_GIT_PUSH_RACE:-false}" != true ] || exit 1
		;;
	*:release-*)
		: >"$TEST_RELEASE_STATE_FILE"
		[ "${TEST_RELEASE_PUSH_RACE:-false}" != true ] || exit 1
		;;
	esac
	exit 0
	;;
image)
	printf '%s\n' "$TEST_REVISION"
	;;
*) printf '%s\n' "unexpected docker command: $*" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/date" <<'EOF'
#!/bin/sh
printf '%s\n' 20260817123456
EOF
chmod +x "$fake_bin"/*

run_case() {
	case_dir=$1
	shift
	mkdir -p "$case_dir"
	: >"$case_dir/log"
	: >"$case_dir/output"
	: >"$case_dir/git-state"
	rm -f "$case_dir/release-state"
	if [ "${1:-}" = TEST_GIT_EXISTS=false ]; then
		rm "$case_dir/git-state"
	fi
	env PATH="$fake_bin:$PATH" TEST_LOG="$case_dir/log" TEST_GIT_STATE_FILE="$case_dir/git-state" TEST_RELEASE_STATE_FILE="$case_dir/release-state" TEST_GIT_DIGEST="$digest" TEST_OTHER_DIGEST="$other_digest" TEST_REVISION="$sha" AWS_REGION=eu-west-1 ECR_REPOSITORY=registry.example/mycfc ECR_REPOSITORY_NAME=mycfc-production GIT_SHA="$sha" GITHUB_OUTPUT="$case_dir/output" "$@" sh "$deployment_dir/publish-release-image.sh"
}

fresh_case="$work_dir/fresh"
run_case "$fresh_case" TEST_GIT_EXISTS=false
grep -q '^build ' "$fresh_case/log"
grep -q "^push registry.example/mycfc:git-$sha$" "$fresh_case/log"
grep -q "^release_tag=release-20260817123456-$sha$" "$fresh_case/output"

git_race_case="$work_dir/git-race"
run_case "$git_race_case" TEST_GIT_EXISTS=false TEST_GIT_PUSH_RACE=true
grep -q "^push registry.example/mycfc:git-$sha$" "$git_race_case/log"
grep -q '^tag ' "$git_race_case/log"

retry_case="$work_dir/retry"
run_case "$retry_case" TEST_GIT_EXISTS=true
grep -q "^pull registry.example/mycfc@$digest$" "$retry_case/log"
if grep -q '^build ' "$retry_case/log" || grep -q ":git-$sha$" "$retry_case/log"; then
	printf '%s\n' 'verified retry unexpectedly rebuilt or pushed the git tag' >&2
	exit 1
fi

release_race_case="$work_dir/release-race"
run_case "$release_race_case" TEST_GIT_EXISTS=true TEST_RELEASE_PUSH_RACE=true
grep -q '^push .*release-' "$release_race_case/log"
grep -q "^release_tag=release-20260817123456-$sha$" "$release_race_case/output"

release_retry_case="$work_dir/release-retry"
run_case "$release_retry_case" TEST_GIT_EXISTS=true TEST_RELEASE_STATE=same
if grep -q '^tag ' "$release_retry_case/log" || grep -q '^push .*release-' "$release_retry_case/log"; then
	printf '%s\n' 'existing matching release tag was unnecessarily pushed' >&2
	exit 1
fi

bad_label_case="$work_dir/bad-label"
if run_case "$bad_label_case" TEST_GIT_EXISTS=true TEST_REVISION=0000000000000000000000000000000000000000; then
	printf '%s\n' 'mismatched image revision unexpectedly promoted' >&2
	exit 1
fi
if grep -q '^tag ' "$bad_label_case/log" || grep -q '^push .*release-' "$bad_label_case/log"; then
	printf '%s\n' 'mismatched image revision reached release promotion' >&2
	exit 1
fi

collision_case="$work_dir/collision"
if run_case "$collision_case" TEST_GIT_EXISTS=true TEST_RELEASE_STATE=other; then
	printf '%s\n' 'conflicting release tag unexpectedly promoted' >&2
	exit 1
fi
if grep -q '^tag ' "$collision_case/log"; then
	printf '%s\n' 'release collision attempted to overwrite the tag' >&2
	exit 1
fi

printf '%s\n' 'publish-release-image tests passed'
