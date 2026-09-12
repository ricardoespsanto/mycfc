#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
output=$work_dir/release.md
sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

RELEASE_VERSION=v1.25.0 GIT_SHA=$sha APPROVAL_SUMMARY='Automate verified releases.' \
	APPROVAL_EVIDENCE='CI and production-parity checks passed.' APPROVAL_ROLLBACK='No traffic switch on failure.' \
	APPROVAL_SCOPE='Publish and deploy only; activation remains excluded.' \
	sh "$script_dir/approval-packet.sh" release "$output"
grep -q '^# Release approval$' "$output"
grep -q 'does not approve merge' "$output"
for kind in merge infrastructure credential purge activation; do
	sh "$script_dir/approval-packet.sh" "$kind" "$work_dir/$kind.md"
	grep -qi "^# $kind approval$" "$work_dir/$kind.md"
done

if RELEASE_VERSION=v1.25.0 GIT_SHA=$sha APPROVAL_SUMMARY='password=canary' \
	sh "$script_dir/approval-packet.sh" release "$work_dir/rejected.md" >/dev/null 2>&1; then
	printf '%s\n' 'credential-shaped approval input was accepted' >&2
	exit 1
fi
if APPROVAL_SUMMARY="line one
line two" sh "$script_dir/approval-packet.sh" merge "$work_dir/multiline.md" >/dev/null 2>&1; then
	printf '%s\n' 'multiline approval input was accepted' >&2
	exit 1
fi
if grep -R -Fq 'password=canary' "$work_dir"; then
	printf '%s\n' 'approval packet leaked the redaction canary' >&2
	exit 1
fi

printf '%s\n' 'approval-packet tests passed'
