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
release_tag="release-20260817123456-$sha"

cat >"$fake_bin/aws" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
tag=$(printf '%s\n' "$*" | sed -n 's/.*imageTag=\([^ ]*\).*/\1/p')
case "$1 $2" in
"ecr describe-images")
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
	;;
"ecr batch-get-image")
	[ -f "$TEST_GIT_STATE_FILE" ] || { printf '%s\n' 'missing image' >&2; exit 1; }
	printf '%s\n' '{"schemaVersion":2}'
	;;
"ecr put-image")
	: >"$TEST_RELEASE_STATE_FILE"
	[ "${TEST_RELEASE_PUT_RACE:-false}" != true ] || exit 1
	;;
*) printf '%s\n' "unexpected aws command: $*" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
case "$1 $2 $3" in
"buildx imagetools inspect") printf '%s\n' "$TEST_REVISION" ;;
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
	mode=${1:-}
	if [ "$mode" = prepare ]; then
		shift
	else
		mode=
	fi
	mkdir -p "$case_dir"
	: >"$case_dir/log"
	: >"$case_dir/output"
	: >"$case_dir/git-state"
	rm -f "$case_dir/release-state"
	if [ "${1:-}" = TEST_GIT_EXISTS=false ]; then
		rm "$case_dir/git-state"
	fi
	env PATH="$fake_bin:$PATH" TEST_LOG="$case_dir/log" TEST_GIT_STATE_FILE="$case_dir/git-state" TEST_RELEASE_STATE_FILE="$case_dir/release-state" TEST_GIT_DIGEST="$digest" TEST_OTHER_DIGEST="$other_digest" TEST_REVISION="$sha" AWS_REGION=eu-west-1 ECR_REPOSITORY=registry.example/mycfc ECR_REPOSITORY_NAME=mycfc-production GIT_SHA="$sha" GITHUB_OUTPUT="$case_dir/output" RELEASE_TAG="$release_tag" IMAGE_DIGEST="$digest" "$@" sh "$deployment_dir/publish-release-image.sh" "$mode"
}

prepare_fresh_case="$work_dir/prepare-fresh"
run_case "$prepare_fresh_case" prepare TEST_GIT_EXISTS=false
grep -q '^build_required=true$' "$prepare_fresh_case/output"
grep -q "^git_tag=git-$sha$" "$prepare_fresh_case/output"
grep -q "^release_tag=$release_tag$" "$prepare_fresh_case/output"

prepare_retry_case="$work_dir/prepare-retry"
run_case "$prepare_retry_case" prepare
grep -q '^build_required=false$' "$prepare_retry_case/output"
grep -q "^image_digest=$digest$" "$prepare_retry_case/output"

matching_case="$work_dir/matching"
run_case "$matching_case" TEST_RELEASE_STATE=same
grep -q "^image=registry.example/mycfc@$digest$" "$matching_case/output"
grep -q "^registry=registry.example$" "$matching_case/output"
grep -q "^release_tag=$release_tag$" "$matching_case/output"
grep -q "^sha=$sha$" "$matching_case/output"
grep -q '^buildx imagetools inspect ' "$matching_case/log"
if grep -Eq '^(build|tag|pull|push) ' "$matching_case/log"; then
	printf '%s\n' 'verification unexpectedly loaded or retagged an image' >&2
	exit 1
fi

promotion_case="$work_dir/promotion"
run_case "$promotion_case"
grep -q '^ecr batch-get-image ' "$promotion_case/log"
grep -q '^ecr put-image ' "$promotion_case/log"
[ -f "$promotion_case/release-state" ]

promotion_race_case="$work_dir/promotion-race"
run_case "$promotion_race_case" TEST_RELEASE_PUT_RACE=true
grep -q '^ecr put-image ' "$promotion_race_case/log"
grep -q "^release_tag=$release_tag$" "$promotion_race_case/output"

bad_label_case="$work_dir/bad-label"
if run_case "$bad_label_case" TEST_REVISION=0000000000000000000000000000000000000000; then
	printf '%s\n' 'mismatched image revision unexpectedly promoted' >&2
	exit 1
fi
if grep -q '^ecr put-image ' "$bad_label_case/log"; then
	printf '%s\n' 'mismatched image revision reached release promotion' >&2
	exit 1
fi

collision_case="$work_dir/collision"
if run_case "$collision_case" TEST_RELEASE_STATE=other; then
	printf '%s\n' 'conflicting release tag unexpectedly promoted' >&2
	exit 1
fi
if grep -q '^ecr put-image ' "$collision_case/log"; then
	printf '%s\n' 'release collision attempted to overwrite the tag' >&2
	exit 1
fi

printf '%s\n' 'publish-release-image tests passed'
