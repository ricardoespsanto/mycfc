#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/deployment" "$test_dir/state"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' 0:0:600
EOF
cat >"$test_dir/bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$CONFIG_CALLS"
[ "${DOCKER_FAIL:-false}" != true ] || exit 1
case "$1" in inspect) printf '%s\n' 172.30.0.20 ;; esac
EOF
cat >"$test_dir/bin/curl" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$CONFIG_CALLS"
case "$1:$2" in
	is-active:--quiet) [ "${WORKER_ACTIVE:-false}" = true ] || [ -f "$SYSTEMCTL_STATE" ] ;;
	enable:--now) [ "${SYSTEMCTL_ENABLE_FAIL:-false}" != true ] || exit 1; : >"$SYSTEMCTL_STATE" ;;
	disable:--now) rm -f "$SYSTEMCTL_STATE" ;;
	*) exit 0 ;;
esac
EOF
cat >"$test_dir/deployment/privacy-worker.sh" <<'EOF'
#!/bin/sh
printf 'worker %s\n' "$*" >>"$CONFIG_CALLS"
[ "${READINESS_FAIL:-false}" != true ]
EOF
cat >"$test_dir/deployment/privacy-retention.sh" <<'EOF'
#!/bin/sh
printf 'retention %s\n' "$*" >>"$CONFIG_CALLS"
[ "${RETENTION_FAIL:-false}" != true ]
EOF
chmod 0755 "$test_dir/bin/"* "$test_dir/deployment/"*.sh
printf '%s\n' blue >"$test_dir/state/active-slot"
export CONFIG_CALLS="$test_dir/calls"
export SYSTEMCTL_STATE="$test_dir/systemctl.state"
env_file="$test_dir/mycfc.env"

write_env() {
	cat >"$env_file" <<EOF
MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
PRIVACY_REQUESTS_ENABLED=$1
PRIVACY_COMPLETION_ENABLED=$2
PRIVACY_WORKER_ENABLED=$3
PRIVACY_RETENTION_ENABLED=$4
EOF
	chmod 0600 "$env_file"
}

run_config() {
	PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$env_file" MYCFC_DEPLOYMENT_DIR="$test_dir/deployment" \
		MYCFC_STATE_DIR="$test_dir/state" sh "$script_dir/privacy-production-config.sh" "$1"
}

write_env false false false false
run_config flags-enable >/dev/null
grep -q '^PRIVACY_REQUESTS_ENABLED=true$' "$env_file"
grep -q '^PRIVACY_COMPLETION_ENABLED=true$' "$env_file"
grep -q 'force-recreate app-blue' "$CONFIG_CALLS"

run_config worker-enable >/dev/null
grep -q '^PRIVACY_WORKER_ENABLED=true$' "$env_file"
grep -q '^systemctl enable --now mycfc-privacy-worker.service$' "$CONFIG_CALLS"

run_config worker-disable >/dev/null
grep -q '^PRIVACY_WORKER_ENABLED=false$' "$env_file"

WORKER_ACTIVE=true
export WORKER_ACTIVE
if run_config flags-disable >/dev/null 2>&1; then
	printf '%s\n' 'flags were disabled while the worker was active' >&2
	exit 1
fi
unset WORKER_ACTIVE
run_config flags-disable >/dev/null
grep -q '^PRIVACY_REQUESTS_ENABLED=false$' "$env_file"
grep -q '^PRIVACY_COMPLETION_ENABLED=false$' "$env_file"

write_env false false false false
if DOCKER_FAIL=true run_config flags-enable >/dev/null 2>&1; then
	printf '%s\n' 'application recreate failure was accepted' >&2
	exit 1
fi
grep -q '^PRIVACY_REQUESTS_ENABLED=false$' "$env_file"
grep -q '^PRIVACY_COMPLETION_ENABLED=false$' "$env_file"
grep -q '^PRIVACY_WORKER_ENABLED=false$' "$env_file"

write_env true true false false
if READINESS_FAIL=true run_config worker-enable >/dev/null 2>&1; then
	printf '%s\n' 'worker readiness failure was accepted' >&2
	exit 1
fi
grep -q '^PRIVACY_WORKER_ENABLED=false$' "$env_file"

write_env false false false false
if RETENTION_FAIL=true run_config retention-enable >/dev/null 2>&1; then
	printf '%s\n' 'retention failure was accepted' >&2
	exit 1
fi
grep -q '^PRIVACY_RETENTION_ENABLED=false$' "$env_file"

write_env false false false false
if SYSTEMCTL_ENABLE_FAIL=true run_config retention-enable >/dev/null 2>&1; then
	printf '%s\n' 'retention timer enable failure was accepted' >&2
	exit 1
fi
grep -q '^PRIVACY_RETENTION_ENABLED=false$' "$env_file"
test ! -e "$SYSTEMCTL_STATE"

printf '%s\n' 'privacy production configuration tests passed'
