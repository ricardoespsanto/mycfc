#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin"

cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$OBSERVER_CALLS"
printf '%s\n' "${OBSERVER_RESULT:-1|0|0|1|5|5|1|1|1|1|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
EOF
chmod 0755 "$test_dir/bin/docker"
export OBSERVER_CALLS="$test_dir/calls"
export PRIVACY_RESTORE_OBSERVER_DATABASE_URL='postgres://mycfc_restore_observer:secret@restore-db:5432/mycfc_restore?sslmode=disable'
export PRIVACY_RESTORE_OBSERVER_NETWORK='mycfc-restore-test'

output=$(PATH="$test_dir/bin:$PATH" sh "$script_dir/privacy-restore-observer.sh" LIVE_LEDGER \
	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb privacy-v1 privacy-erasure-executor/v2 privacy-erasure-plan/v2 \
	sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc)
printf '%s\n' "$output" | jq -e '.replay_count==1 and .synthetic_count==0 and .verified_run_count==1 and .expected_checkpoint_count==5 and .succeeded_checkpoint_count==5 and .provider_absent_count==1 and .consent_clock_verified_count==1 and .closure_v3_count==1 and .erasure_effective_at_verified_count==1 and (.image_digest|test("^sha256:[0-9a-f]{64}$"))' >/dev/null
grep -q -- '--network mycfc-restore-test -e DATABASE_URL postgres:16.9-alpine3.21@sha256:' "$OBSERVER_CALLS"
if grep -q 'secret' "$OBSERVER_CALLS"; then
	printf '%s\n' 'observer database password was passed on the command line' >&2
	exit 1
fi

export OBSERVER_RESULT='1|0|1|1|5|5|1|1|1|1|dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
PATH="$test_dir/bin:$PATH" sh "$script_dir/privacy-restore-observer.sh" SYNTHETIC_BOOTSTRAP \
	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb privacy-v1 privacy-erasure-executor/v2 privacy-erasure-plan/v2 \
	sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc >/dev/null

export OBSERVER_RESULT='1|0|0|1|5|5|1|1|1|1|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
if PATH="$test_dir/bin:$PATH" sh "$script_dir/privacy-restore-observer.sh" SYNTHETIC_BOOTSTRAP \
	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb privacy-v1 privacy-erasure-executor/v2 privacy-erasure-plan/v2 \
	sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc >/dev/null 2>&1; then
	printf '%s\n' 'observer accepted synthetic mode without a synthetic fixture' >&2
	exit 1
fi

export OBSERVER_RESULT='1|0|0|1|5|4|1|1|1|1|aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
if PATH="$test_dir/bin:$PATH" sh "$script_dir/privacy-restore-observer.sh" LIVE_LEDGER \
	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb privacy-v1 privacy-erasure-executor/v2 privacy-erasure-plan/v2 \
	sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc >/dev/null 2>&1; then
	printf '%s\n' 'observer accepted incomplete checkpoints' >&2
	exit 1
fi

printf '%s\n' 'privacy restore observer tests passed'
