#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin" "$work_dir/units" "$work_dir/report"
for unit in mycfc-privacy-worker.service mycfc-privacy-retention.timer mycfc-privacy-production-operation.service mycfc-postgres-restore-drill.timer; do
	: >"$work_dir/units/$unit"
done
cat >"$work_dir/bin/id" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$work_dir/bin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$TEST_LOG"
if [ "${1-}" = is-active ]; then exit 1; fi
if [ "${TEST_SYSTEMCTL_DISABLE_FAILURE:-false}" = true ] && [ "${1-}" = disable ]; then exit 1; fi
EOF
cat >"$work_dir/bin/docker" <<'EOF'
#!/bin/sh
printf 'docker %s\n' "$*" >>"$TEST_LOG"
if [ "${1-}" = inspect ]; then exit 0; fi
if [ "${1-}" = ps ]; then exit 0; fi
EOF
cat >"$work_dir/bin/install" <<'EOF'
#!/bin/sh
for target in "$@"; do :; done
mkdir -p "$target"
chmod 0700 "$target"
EOF
chmod +x "$work_dir/bin/"*
TEST_LOG="$work_dir/actions" PATH="$work_dir/bin:$PATH" MYCFC_SYSTEMD_UNIT_DIR="$work_dir/units" \
	MYCFC_RETIREMENT_REPORT_DIR="$work_dir/report" sh "$script_dir/retire-privacy-automation.sh" >/dev/null
test ! -e "$work_dir/units/mycfc-privacy-worker.service"
test ! -e "$work_dir/units/mycfc-privacy-retention.timer"
test ! -e "$work_dir/units/mycfc-postgres-restore-drill.timer"
grep -q '^systemctl disable --now mycfc-privacy-worker.service$' "$work_dir/actions"
grep -q '^docker rm -f mycfc-production-privacy-worker-1$' "$work_dir/actions"
grep -q '^docker rm -f mycfc-production-privacy-activation-disable-bootstrap-1$' "$work_dir/actions"
grep -q '^systemctl daemon-reload$' "$work_dir/actions"
test "$(stat -c %a "$work_dir/report/latest.env")" = 600
grep -q '^credential_paths_for_separate_review=' "$work_dir/report/latest.env"

failure_dir="$work_dir/failure-units"
mkdir -p "$failure_dir"
: >"$failure_dir/mycfc-privacy-worker.service"
if TEST_SYSTEMCTL_DISABLE_FAILURE=true TEST_LOG="$work_dir/failure-actions" PATH="$work_dir/bin:$PATH" \
	MYCFC_SYSTEMD_UNIT_DIR="$failure_dir" MYCFC_RETIREMENT_REPORT_DIR="$work_dir/failure-report" \
	sh "$script_dir/retire-privacy-automation.sh" >/dev/null 2>&1; then
	printf '%s\n' 'retirement unexpectedly ignored an installed-unit disable failure' >&2
	exit 1
fi
printf '%s\n' 'privacy automation retirement tests passed'
