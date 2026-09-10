#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin"

cat >"$work_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' '0:600'
EOF
cat >"$work_dir/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$work_dir/bin/stat" "$work_dir/bin/logger"

key=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
printf '%s\n' "$key" >"$work_dir/key"
image='registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
valid_until=$(date -u -d '+1 day' +%Y-%m-%dT%H:%M:%SZ)

jq -n \
	--arg completed_at "$now" \
	--arg valid_until "$valid_until" \
	'{contract:"mycfc/privacy-restore-drill-attestation/v1",result:"SUCCEEDED",completed_at:$completed_at,valid_until:$valid_until,image_digest:"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",backup:{created_at:"2026-09-01T02:15:00Z",manifest_key_sha256:("b"*64),manifest_version:"manifest-version",manifest_sha256:("c"*64),dump_key_sha256:("d"*64),dump_version:"dump-version",dump_sha256:("e"*64)},schema_migration_digest:("f"*64),ledger:{inventory_sha256:("1"*64),object_count:2},replay:{imported_count:1,replayed_count:1,already_applied_count:0,absence_verified_count:1}}' \
	>"$work_dir/payload.json"
canonical=$(jq -Sc . "$work_dir/payload.json")
hmac=$(printf '%s' "$canonical" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$key" -binary | od -An -v -tx1 | tr -d ' \n')
jq --arg hmac "$hmac" '. + {auth_hmac_sha256:$hmac}' "$work_dir/payload.json" >"$work_dir/attestation.json"

run_verify() {
	env PATH="$work_dir/bin:$PATH" \
		MYCFC_RESTORE_ATTESTATION_FILE="$work_dir/attestation.json" \
		MYCFC_RESTORE_ATTESTATION_AUTH_KEY_FILE="$work_dir/key" \
		sh "$deployment_dir/verify-privacy-restore-attestation.sh" "$1"
}

run_verify "$image" | grep -Eq '^privacy_restore_promotion_gate_succeeded attestation_sha256=[0-9a-f]{64} age_seconds=[0-9]+ image_digest=sha256:[0-9a-f]{64}$'

if run_verify 'registry.example/mycfc@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' >/dev/null 2>&1; then
	printf '%s\n' 'An attestation for a different image passed verification.' >&2
	exit 1
fi

jq '.ledger.object_count = 3' "$work_dir/attestation.json" >"$work_dir/tampered.json"
mv "$work_dir/tampered.json" "$work_dir/attestation.json"
if run_verify "$image" >/dev/null 2>&1; then
	printf '%s\n' 'A tampered restore attestation passed verification.' >&2
	exit 1
fi

printf '%s\n' 'privacy restore attestation verification tests passed'
