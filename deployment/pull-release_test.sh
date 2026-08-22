#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
fake_bin="$work_dir/bin"
mkdir -p "$fake_bin"

cat >"$fake_bin/id" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$fake_bin/stat" <<'EOF'
#!/bin/sh
printf '0:600\n'
EOF
cat >"$fake_bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$fake_bin/logger" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_EVENT_LOG"
exit 0
EOF
cat >"$fake_bin/flock" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$fake_bin/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$fake_bin/aws" <<'EOF'
#!/bin/sh
if [ -n "${AWS_ACCESS_KEY_ID:-}" ] || [ -n "${AWS_SECRET_ACCESS_KEY:-}" ] || [ -n "${AWS_SESSION_TOKEN:-}" ]; then
	printf '%s\n' 'application AWS credentials reached the release agent' >&2
	exit 1
fi
printf '%s|%s\n' "${AWS_PROFILE:-}" "${AWS_SHARED_CREDENTIALS_FILE:-}" >>"$TEST_AWS_LOG"
case "$*" in
	*get-login-password*) printf 'password\n' ;;
	*imageDetails*imageTags*) printf '%s\n' "${TEST_RELEASE_TAG:-release-20260810183743-3e22b4a8057f99b8cbbb8c37dd189d13f03cabb4}" ;;
	*imageDigest*) printf 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
	*) printf 'unexpected aws invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_DOCKER_LOG"
case "$1" in
	login) cat >/dev/null ;;
	pull) ;;
	image)
		case "$*" in
			*org.opencontainers.image.revision*) printf '3e22b4a8057f99b8cbbb8c37dd189d13f03cabb4\n' ;;
			*) printf 'registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n' ;;
		esac
		;;
	inspect)
		case "$*" in
			*NetworkSettings.Networks*app-blue-1*) printf '172.30.0.20\n' ;;
			*NetworkSettings.Networks*app-green-1*) printf '172.30.0.21\n' ;;
			*State.Running*caddy-1*) printf 'true\n' ;;
			*Config.Image*) printf '%s\n' "${TEST_ACTIVE_IMAGE:-registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}" ;;
			*) exit 1 ;;
		esac
		;;
	compose)
		if [ "${TEST_POST_SWITCH_FAILURE:-}" = true ] && printf '%s\n' "$*" | grep -q 'exec -T caddy wget'; then
			exit 1
		fi
		;;
	logs) ;;
	*) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
url=
for argument in "$@"; do
	url=$argument
done
case "$url" in
	*/login)
		if [ "${TEST_BAD_ASSET:-}" = true ]; then
			printf '<html><body>missing asset</body></html>\n'
		else
			printf '<html><script src="/assets/app-123456789abc.js" defer></script></html>\n'
		fi
		;;
	*) ;;
esac
EOF
chmod +x "$fake_bin"/*

setup_case() {
	case_dir=$1
	mkdir -p "$case_dir/state" "$case_dir/runtime" "$case_dir/release-aws"
	cat >"$case_dir/mycfc.env" <<'EOF'
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=application-key
AWS_SECRET_ACCESS_KEY=application-secret
ECR_REPOSITORY_URL=registry.example/mycfc
MYCFC_DOMAIN=example.com
MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
APP_VERSION=old
APP_RELEASED_AT=2026-08-09T00:00:00Z
GIT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
EOF
	chmod 0600 "$case_dir/mycfc.env"
	: >"$case_dir/release-aws/credentials"
	chmod 0600 "$case_dir/release-aws/credentials"
	printf 'legacy\n' >"$case_dir/state/active-slot"
	cat >"$case_dir/state/caddy-upstream.caddy" <<'EOF'
reverse_proxy app:8080 {
	health_uri /health/live
}
EOF
	: >"$case_dir/docker.log"
	: >"$case_dir/aws.log"
	: >"$case_dir/events.log"
}

run_release() {
	case_dir=$1
	shift
	env \
		PATH="$fake_bin:$PATH" \
		TEST_DOCKER_LOG="$case_dir/docker.log" \
		TEST_AWS_LOG="$case_dir/aws.log" \
		TEST_EVENT_LOG="$case_dir/events.log" \
		MYCFC_ENV_FILE="$case_dir/mycfc.env" \
		MYCFC_DEPLOYMENT_STATE_DIR="$case_dir/state" \
		MYCFC_RUNTIME_DIR="$case_dir/runtime" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$case_dir/release-aws/credentials" \
		"$@" \
		sh "$deployment_dir/pull-release.sh"
}

success_case="$work_dir/success"
setup_case "$success_case"
sed 's/sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/' "$success_case/mycfc.env" >"$success_case/mycfc.env.current"
mv "$success_case/mycfc.env.current" "$success_case/mycfc.env"
chmod 0600 "$success_case/mycfc.env"
run_release "$success_case" TEST_ACTIVE_IMAGE=registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
test "$(cat "$success_case/state/active-slot")" = blue
test "$(cat "$success_case/state/last-attempt-result")" = succeeded
grep -q "^sha256:bbbb.*succeeded" "$success_case/state/last-attempt"
test "$(cat "$success_case/state/release-timeline-digest")" = 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
test "$(cat "$success_case/state/release-published-at")" = '2026-08-10T18:37:43Z'
for milestone in agent-started detected image-pulled migration-completed candidate-ready traffic-switched deployment-completed; do
	test -s "$success_case/state/release-$milestone-at"
done
awk '
	/release_agent-started/ { agent = NR }
	/release_detected/ { detected = NR }
	/release_image-pulled/ { pulled = NR }
	/release_migration-completed/ { migrated = NR }
	/release_candidate-ready/ { ready = NR }
	/release_traffic-switched/ { switched = NR }
	/release_deployment-completed/ { completed = NR }
	END { exit !(agent < detected && detected < pulled && pulled < migrated && migrated < ready && ready < switched && switched < completed) }
' "$success_case/events.log"
grep -q 'reverse_proxy app-blue:8080' "$success_case/state/caddy-upstream.caddy"
grep -q '^MYCFC_IMAGE=.*bbbbbbbb' "$success_case/mycfc.env"
grep -q -- '--profile blue up -d --no-deps --force-recreate app-blue' "$success_case/docker.log"
grep -q 'exec -T caddy caddy reload' "$success_case/docker.log"
grep -q "^mycfc-release|$success_case/release-aws/credentials$" "$success_case/aws.log"
if grep -q 'cloudflared' "$success_case/docker.log"; then
	printf '%s\n' 'ordinary releases must not operate on cloudflared' >&2
	exit 1
fi

failure_case="$work_dir/failure"
setup_case "$failure_case"
if run_release "$failure_case" TEST_BAD_ASSET=true; then
	printf '%s\n' 'release without a fingerprinted asset unexpectedly succeeded' >&2
	exit 1
fi
test "$(cat "$failure_case/state/active-slot")" = legacy
grep -q 'reverse_proxy app:8080' "$failure_case/state/caddy-upstream.caddy"
grep -q '^MYCFC_IMAGE=.*aaaaaaaa' "$failure_case/mycfc.env"
test "$(cat "$failure_case/state/failed-release-digest")" = 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
test "$(cat "$failure_case/state/last-attempt-result")" = failed
test -s "$failure_case/state/release-image-pulled-at"
test -s "$failure_case/state/release-migration-completed-at"
test ! -f "$failure_case/state/release-candidate-ready-at"
grep -q -- '--profile blue stop app-blue' "$failure_case/docker.log"

post_switch_case="$work_dir/post-switch-failure"
setup_case "$post_switch_case"
if run_release "$post_switch_case" TEST_POST_SWITCH_FAILURE=true; then
	printf '%s\n' 'release with a failed post-switch check unexpectedly succeeded' >&2
	exit 1
fi
test "$(cat "$post_switch_case/state/active-slot")" = legacy
grep -q 'reverse_proxy app:8080' "$post_switch_case/state/caddy-upstream.caddy"
grep -q '^MYCFC_IMAGE=.*aaaaaaaa' "$post_switch_case/mycfc.env"
test "$(cat "$post_switch_case/state/failed-release-digest")" = 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
awk -F '\t' '$2 == "failed" { found = 1 } END { exit !found }' "$post_switch_case/state/last-attempt"
test -s "$post_switch_case/state/release-traffic-switched-at"
test ! -f "$post_switch_case/state/release-deployment-completed-at"
grep -q -- '--profile blue stop app-blue' "$post_switch_case/docker.log"
test "$(grep -c 'exec -T caddy caddy reload' "$post_switch_case/docker.log")" -ge 2

: >"$failure_case/docker.log"
detected_before=$(cat "$failure_case/state/release-detected-at")
run_release "$failure_case"
test "$(cat "$failure_case/state/last-attempt-result")" = quarantined
test "$(cat "$failure_case/state/release-detected-at")" = "$detected_before"
if grep -Eq 'pull registry|up -d|run --rm|force-recreate' "$failure_case/docker.log"; then
	printf '%s\n' 'quarantined digest was retried' >&2
	exit 1
fi

# A new release tag for the same digest is a distinct publication timeline.
old_timeline_tag=$(cat "$failure_case/state/release-timeline-tag")
run_release "$failure_case" TEST_RELEASE_TAG=release-20260810190000-3e22b4a8057f99b8cbbb8c37dd189d13f03cabb4
test "$(cat "$failure_case/state/release-timeline-tag")" != "$old_timeline_tag"
test "$(cat "$failure_case/state/release-published-at")" = '2026-08-10T19:00:00Z'
test -s "$failure_case/state/release-agent-started-at"
test -s "$failure_case/state/release-detected-at"
test ! -f "$failure_case/state/release-image-pulled-at"
awk -F '\t' '$2 == "quarantined" { found = 1 } END { exit !found }' "$failure_case/state/last-attempt"

if grep -q 'application-secret\|release-secret' "$success_case/events.log" "$failure_case/events.log" "$post_switch_case/events.log"; then
	printf '%s\n' 'release timeline leaked a credential' >&2
	exit 1
fi

printf '%s\n' 'pull-release tests passed'
