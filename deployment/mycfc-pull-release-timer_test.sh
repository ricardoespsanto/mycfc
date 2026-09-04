#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
timer="$deployment_dir/mycfc-pull-release.timer"

grep -qx 'OnBootSec=30s' "$timer"
grep -qx 'OnUnitActiveSec=30s' "$timer"
grep -qx 'RandomizedDelaySec=10s' "$timer"
grep -qx 'AccuracySec=1s' "$timer"
grep -qx 'Persistent=true' "$timer"
grep -qx 'Unit=mycfc-pull-release.service' "$timer"

# The configured scheduling window is 30 + 10 + 1 seconds, below the
# 60-second pickup target before accounting for the short ECR query itself.
maximum_schedule_seconds=$((30 + 10 + 1))
test "$maximum_schedule_seconds" -lt 60

if command -v systemd-analyze >/dev/null 2>&1; then
	work_dir=$(mktemp -d)
	trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
	cp "$timer" "$work_dir/mycfc-pull-release.timer"
	cat >"$work_dir/mycfc-pull-release.service" <<'EOF'
[Unit]
Description=MyCFC pull-release timer validation fixture

[Service]
Type=oneshot
ExecStart=/bin/true
EOF
	systemd-analyze verify "$work_dir/mycfc-pull-release.service" "$work_dir/mycfc-pull-release.timer"
fi

printf '%s\n' 'pull-release timer tests passed'
