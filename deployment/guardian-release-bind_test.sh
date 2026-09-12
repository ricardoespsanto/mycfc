#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"
printf '%s\n' 'MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_dir/main.env"
cat >"$test_dir/release.env" <<'EOF'
GUARDIAN_RELEASE_BIND_DATABASE_URL=postgres://mycfc_guardian_release_bind:secret@postgres:5432/mycfc?sslmode=disable
GUARDIAN_RELEASE_BIND_EXPECTED_DATABASE=mycfc
EOF
chmod 600 "$test_dir/main.env" "$test_dir/release.env"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' "${TEST_UID:-0}"
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
case "$*" in
  *release.env|*main.env) printf '%s\n' '0:0:600' ;;
  *) /usr/bin/stat "$@" ;;
esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s image=%s\n' "$*" "${MYCFC_IMAGE:-from-env-file}" >>"$TEST_DOCKER_CALLS"
printf '%s\n' 'container-command-stdout'
printf '%s\n' 'container-command-stderr' >&2
EOF
chmod +x "$test_dir/bin/id" "$test_dir/bin/stat" "$test_dir/bin/docker"
export TEST_DOCKER_CALLS="$test_dir/docker.calls"

run_provision() {
	PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_GUARDIAN_RELEASE_BIND_ENV_FILE="$test_dir/release.env" \
	 MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/guardian-release-bind.sh" "$@"
}

output=$(run_provision provision 2>&1)
[ "$(printf '%s\n' "$output" | grep -c '^container-command-stdout$')" -eq 1 ]
[ "$(printf '%s\n' "$output" | grep -c '^container-command-stderr$')" -eq 1 ]
grep -q 'slog.Info("guardian release bind role provisioned", "event", "guardian_release_bind_role_provisioned")' "$script_dir/../cmd/server/main.go"
grep -q -- '--profile guardian-release-bind-bootstrap run --rm guardian-release-bind-bootstrap' "$TEST_DOCKER_CALLS"
MYCFC_GUARDIAN_RELEASE_BIND_PROVISION_IMAGE=registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run_provision provision >/dev/null
grep -q 'image=registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' "$TEST_DOCKER_CALLS"
if MYCFC_GUARDIAN_RELEASE_BIND_PROVISION_IMAGE=registry.example/mycfc:mutable run_provision provision >/dev/null 2>&1; then echo 'mutable bootstrap image accepted' >&2; exit 1; fi
if TEST_UID=1000 run_provision provision >/dev/null 2>&1; then echo 'non-root release role provisioning accepted' >&2; exit 1; fi
printf '%s\n' 'UNEXPECTED=value' >>"$test_dir/release.env"
if run_provision provision >/dev/null 2>&1; then echo 'unknown release credential input accepted' >&2; exit 1; fi

release_service=$(awk '/^  guardian-release-bind:/{copy=1} copy && /^  [a-z][a-z-]*:/ && !/^  guardian-release-bind:/{exit} copy{print}' "$script_dir/compose.yaml")
printf '%s' "$release_service" | grep -q '/etc/mycfc/guardian-release-bind.env'
printf '%s' "$release_service" | grep -q 'GUARDIAN_RUNTIME_IMAGE_DIGEST'
if printf '%s' "$release_service" | grep -Eq 'production-config|DATABASE_URL:|AWS_|APP_VERSION|GIT_SHA|guardian-activation|privacy-|bootstrap'; then
	echo 'runtime release binding service received an unrelated or owner capability' >&2
	exit 1
fi
if ! grep -q 'guardian-release-bind' "$script_dir/pull-release.sh"; then
	echo 'supported release does not invoke release cutoff' >&2
	exit 1
fi

install_script=$script_dir/install.sh
grep -q 'guardian_release_bind_env_file=/etc/mycfc/guardian-release-bind.env' "$install_script"
grep -q 'stage the release-bind login before running install.sh' "$install_script"
if grep -Eq 'provision-guardian-release-bind|guardian-release-bind\.sh provision' "$install_script"; then
	echo 'installer automatically provisions the release-bind capability' >&2
	exit 1
fi
awk '
	/guardian-release-bind\.sh"$/ { executable = NR }
	/systemctl start mycfc-pull-release\.service/ { release = NR }
	END { exit !(executable && release && executable < release) }
' "$install_script"

printf '%s\n' 'guardian release bind deployment tests passed'
