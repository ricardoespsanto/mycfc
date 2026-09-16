#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/config" "$test_dir/state/acceptance"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
path=$3
case "$path" in */acceptance) printf '%s\n' '0:0:700' ;; *) printf '%s\n' '0:0:600' ;; esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_LOG"
mode=
for value in "$@"; do mode=$value; done
case "$mode" in
	provision|rotate|revoke) exit 0 ;;
	*) cat "$DOCKER_OUTPUT_FILE" ;;
esac
EOF
chmod 0755 "$test_dir/bin/"*

for file in mycfc.env privacy-acceptance.env admin-database-url login-database-url app-database-url executor-database-url signing-private.key tombstone-public.key tombstone-locator.key aws-credentials; do
	printf '%s\n' test >"$test_dir/config/$file"
	chmod 0600 "$test_dir/config/$file"
done
printf '%s\n' acceptance-v1 >"$test_dir/config/signing-key-id"
openssl genpkey -algorithm Ed25519 -out "$test_dir/private.pem" >/dev/null 2>&1
openssl pkey -in "$test_dir/private.pem" -pubout -outform DER 2>/dev/null | tail -c 32 | base64 -w0 >"$test_dir/config/signing-public.key"
printf '\n' >>"$test_dir/config/signing-public.key"
chmod 0600 "$test_dir/config/signing-key-id" "$test_dir/config/signing-public.key"

make_output() {
	mode=$1
	outcome=$2
	conditions=$3
	events=$4
	payload=$(jq -cS -n --arg mode "$mode" --arg outcome "$outcome" --argjson conditions "$conditions" '{contract:"mycfc/privacy-synthetic-acceptance/v1",mode:$mode,outcome:$outcome,image_digest:("sha256:" + ("a" * 64)),schema_digest:("b" * 64),fixture_sha256:("c" * 64),manifest_sha256:("d" * 64),policy_sha256:("e" * 64),started_at:"2026-09-16T17:00:00Z",observed_at:"2026-09-16T17:01:00Z",simulated_notices:1,checkpoints:1,conditions:$conditions}')
	digest=$(printf '%s' "$payload" | sha256sum | awk '{print $1}')
	message="$test_dir/message"
	{ printf '%s\0%s\0' mycfc/privacy-synthetic-acceptance/v1 acceptance-v1; printf '%s' "$payload"; } >"$message"
	signature=$(openssl pkeyutl -sign -inkey "$test_dir/private.pem" -rawin -in "$message" | base64 -w0)
	{
		[ -z "$events" ] || printf '%s\n' "$events"
		jq -cS -n --arg key acceptance-v1 --argjson payload "$payload" --arg digest "$digest" --arg signature "$signature" '{contract:"mycfc/privacy-synthetic-acceptance/v1",key_id:$key,payload:$payload,payload_sha256:$digest,signature:$signature}'
	} >"$test_dir/docker-output"
}

run_wrapper() {
	PATH="$test_dir/bin:$PATH" \
		DOCKER_LOG="$test_dir/docker.log" \
		DOCKER_OUTPUT_FILE="$test_dir/docker-output" \
		MYCFC_ENV_FILE="$test_dir/config/mycfc.env" \
		MYCFC_PRIVACY_ACCEPTANCE_ENV_FILE="$test_dir/config/privacy-acceptance.env" \
		MYCFC_PRIVACY_ACCEPTANCE_DIR="$test_dir/config" \
		MYCFC_PRIVACY_OPERATION_STATE_DIR="$test_dir/state" \
		MYCFC_RUNTIME_DIR="$test_dir" \
		MYCFC_IMAGE="registry.example/mycfc@sha256:$(printf 'a%.0s' $(seq 1 64))" \
		MYCFC_PRIVACY_ACCEPTANCE_EVIDENCE_OUTPUT="${EVIDENCE_OUTPUT:-$test_dir/state/acceptance/$1.json}" \
		MYCFC_DEPLOYMENT_DIR="$test_dir" \
		sh "$script_dir/privacy-acceptance.sh" "$1"
}

: >"$test_dir/docker-output"
run_wrapper provision
grep -q -- 'privacy-acceptance-admin-database-url:ro' "$test_dir/docker.log"
grep -q -- 'privacy-acceptance-database-url:ro' "$test_dir/docker.log"

events='event=privacy_acceptance_canary_retry_observed count=1
event=privacy_acceptance_canary_recovery_observed count=1'
make_output canary-retry COMPLETED '["retry","recovery"]' "$events"
output=$(run_wrapper canary-retry)
[ "$output" = "$events" ]
jq -e '.contract == "mycfc/privacy-synthetic-acceptance/v1" and .payload.mode == "canary-retry"' "$test_dir/state/acceptance/canary-retry.json" >/dev/null
grep -q -- 'AWS_SHARED_CREDENTIALS_FILE=/run/secrets/mycfc/aws-credentials' "$test_dir/docker.log"

make_output canary-failure CANARY_VERIFIED '["failure"]' 'event=privacy_acceptance_canary_failure_observed count=1'
head -n 1 "$test_dir/docker-output" >"$test_dir/tampered-output"
tail -n 1 "$test_dir/docker-output" | jq -c 'if (.signature | startswith("A")) then .signature = ("B" + .signature[1:]) else .signature = ("A" + .signature[1:]) end' >>"$test_dir/tampered-output"
mv "$test_dir/tampered-output" "$test_dir/docker-output"
if run_wrapper canary-failure >"$test_dir/tampered.out" 2>&1; then
	printf '%s\n' 'tampered acceptance evidence was accepted' >&2
	exit 1
fi
[ ! -e "$test_dir/state/acceptance/canary-failure.json" ]

make_output run COMPLETED '[]' ''
if EVIDENCE_OUTPUT="$test_dir/outside.json" run_wrapper run >"$test_dir/path.out" 2>&1; then
	printf '%s\n' 'acceptance evidence escaped the protected state directory' >&2
	exit 1
fi
[ ! -e "$test_dir/outside.json" ]

printf '%s\n' 'privacy acceptance host wrapper tests passed'
