#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
script=$script_dir/release-upgrade-test.sh
sh -n "$script"

line_number() {
	pattern=$1
	line=$(grep -nF "$pattern" "$script" | head -n 1 | cut -d: -f1)
	case "$line" in ''|*[!0-9]*) printf 'missing release-upgrade phase: %s\n' "$pattern" >&2; exit 1 ;; esac
	printf '%s\n' "$line"
}

previous=0
for phase in \
	'phase=predecessor-bootstrap' \
	'phase=predecessor-migrate' \
	'phase=guardian-provision' \
	'phase=candidate-bootstrap' \
	'phase=candidate-migrate' \
	'phase=candidate-harden' \
	'phase=guardian-bind' \
	'phase=candidate-start'; do
	current=$(line_number "$phase")
	if [ "$current" -le "$previous" ]; then
		printf 'release-upgrade phase is out of production order: %s\n' "$phase" >&2
		exit 1
	fi
	previous=$current
done

if grep -Eq 'caddy|traffic[-_ ]switch|active-slot' "$script"; then
	printf 'release-upgrade gate must never switch traffic\n' >&2
	exit 1
fi
grep -q 'candidate_started.*docker rm -f' "$script"
grep -q 'postgres_started.*docker rm -f' "$script"
grep -q 'network_created.*docker network rm' "$script"
grep -Fq "tr '[:upper:]' '[:lower:]'" "$script"

printf '%s\n' 'release-upgrade control tests passed'
