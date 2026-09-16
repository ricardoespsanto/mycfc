#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/deployment"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
path=$3
if [ -d "$path" ]; then printf '%s\n' '0:0:700'; else printf '%s\n' '0:0:600'; fi
EOF
cat >"$test_dir/bin/systemctl" <<'EOF'
#!/bin/sh
case " ${ACTIVE_SERVICES:-} " in *" $3 "*) exit 0 ;; *) exit 3 ;; esac
EOF
cat >"$test_dir/bin/flock" <<'EOF'
#!/bin/sh
if [ "${FAIL_FLOCK_FD:-}" = "$2" ]; then exit 1; fi
exit 0
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
case "$*" in
	inspect*) [ "${ACTIVE_CONTAINER:-false}" = true ] && printf '%s\n' true || printf '%s\n' false ;;
	*) exit 1 ;;
esac
EOF
cat >"$test_dir/deployment/privacy-retention.sh" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$RETENTION_CALLS"
EOF
chmod 0755 "$test_dir/bin/"* "$test_dir/deployment/privacy-retention.sh"

sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
digest=$(printf 'b%.0s' $(seq 1 64))
image="123456789012.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@sha256:$digest"
state_dir="$test_dir/state"
env_file="$test_dir/mycfc.env"
lock_file="$test_dir/operation.lock"
release_lock_file="$test_dir/release.lock"
export RETENTION_CALLS="$test_dir/retention.calls"

write_env() {
	enabled=$1
	credentials=${2:-false}
	destructive=${3:-false}
	cat >"$env_file" <<EOF
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=$enabled
PRIVACY_PRODUCTION_CREDENTIAL_OPERATIONS_ENABLED=$credentials
PRIVACY_PRODUCTION_DESTRUCTIVE_OPERATIONS_ENABLED=$destructive
GIT_SHA=$sha
MYCFC_IMAGE=$image
EOF
	chmod 0600 "$env_file"
}

write_request() {
	id=$1
	operation=$2
	target_image=${3:-$image}
	issued=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	expires=$(date -u -d "$issued + 10 minutes" +%Y-%m-%dT%H:%M:%SZ)
	request="$test_dir/request-$id.json"
	jq -cS -n --arg id "$id" --arg op "$operation" --arg sha "$sha" --arg image "$target_image" \
		--arg issued "$issued" --arg expires "$expires" \
		--argjson run "${id%-*}" --argjson attempt "${id#*-}" \
		'{contract:"mycfc/privacy-production-operation-request/v1",request_id:$id,operation:$op,source_sha:$sha,expected_image:$image,issued_at:$issued,expires_at:$expires,workflow_run_id:$run,workflow_run_attempt:$attempt}' \
		>"$request"
	chmod 0600 "$request"
	printf '%s\n' "$request"
}

run_operation() {
	request=$1
	id=$(jq -r .request_id "$request")
	mkdir -p "$state_dir/receipts"
	PATH="$test_dir/bin:$PATH" \
		MYCFC_ENV_FILE="$env_file" \
		MYCFC_PRIVACY_OPERATION_STATE_DIR="$state_dir" \
		MYCFC_PRIVACY_OPERATION_LOCK_FILE="$lock_file" \
		MYCFC_RELEASE_LOCK_FILE="$release_lock_file" \
		MYCFC_DEPLOYMENT_DIR="$test_dir/deployment" \
		sh "$script_dir/privacy-production-operation.sh" "$request" "$state_dir/receipts/$id.json"
}

write_env false
request=$(write_request 101-1 status)
if run_operation "$request" >"$test_dir/disabled.out" 2>&1; then
	printf '%s\n' 'disabled operation gate accepted a request' >&2
	exit 1
fi
grep -q 'reason=operations_disabled' "$test_dir/disabled.out"

rm -rf "$state_dir"
write_env true
request=$(write_request 102-1 status)
output=$(run_operation "$request")
printf '%s' "$output" | grep -q '^event=privacy_operation_started request_id=102-1 operation=status$'
printf '%s' "$output" | grep -q '^event=privacy_operation_succeeded request_id=102-1 operation=status '
receipt="$state_dir/receipts/102-1.json"
jq -e --arg image "$image" --arg sha "$sha" '
	(keys | sort) == ["contract","expected_image","finished_at","operation","reason","request_id","request_sha256","result","services","source_sha","started_at"] and
	.contract == "mycfc/privacy-production-operation-receipt/v1" and .request_id == "102-1" and .operation == "status" and
	.source_sha == $sha and .expected_image == $image and .result == "SUCCEEDED" and .reason == null and
	(.request_sha256 | test("^[0-9a-f]{64}$")) and ([.services[]] | all(. == false))
' "$receipt" >/dev/null

replay=$(run_operation "$request")
printf '%s' "$replay" | grep -q '^event=privacy_operation_replayed request_id=102-1 operation=status result=SUCCEEDED$'

rm -rf "$state_dir"
request=$(write_request 103-1 preflight)
if ACTIVE_SERVICES=mycfc-privacy-worker.service run_operation "$request" >"$test_dir/active.out" 2>&1; then
	printf '%s\n' 'preflight accepted active privacy work' >&2
	exit 1
fi
grep -q 'reason=privacy_activity_not_quiescent' "$test_dir/active.out"
jq -e '.result == "REJECTED" and .reason == "privacy_activity_not_quiescent"' "$state_dir/receipts/103-1.json" >/dev/null
if grep -Eq 'mycfc-production-app(-blue|-green)?-1' "$script_dir/privacy-production-operation.sh"; then
	printf '%s\n' 'preflight incorrectly treats the ordinary application as privacy activity' >&2
	exit 1
fi

rm -rf "$state_dir"
request=$(write_request 104-1 shell)
if run_operation "$request" >"$test_dir/shell.out" 2>&1; then
	printf '%s\n' 'non-allowlisted shell-shaped operation was accepted' >&2
	exit 1
fi
grep -q 'reason=operation_not_allowlisted' "$test_dir/shell.out"

rm -rf "$state_dir"
bad_digest=$(printf 'c%.0s' $(seq 1 64))
request=$(write_request 105-1 status "123456789012.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@sha256:$bad_digest")
if run_operation "$request" >"$test_dir/image.out" 2>&1; then
	printf '%s\n' 'request for a non-active image was accepted' >&2
	exit 1
fi
grep -q 'reason=image_not_active' "$test_dir/image.out"

rm -rf "$state_dir"
request=$(write_request 111-1 status)
if FAIL_FLOCK_FD=8 run_operation "$request" >"$test_dir/release-lock.out" 2>&1; then
	printf '%s\n' 'operation ran concurrently with a production release' >&2
	exit 1
fi
grep -q 'reason=release_locked' "$test_dir/release-lock.out"

rm -rf "$state_dir"
write_env true false false
request=$(write_request 106-1 retention-provision)
if run_operation "$request" >"$test_dir/credential.out" 2>&1; then
	printf '%s\n' 'credential operation bypassed its independent gate' >&2
	exit 1
fi
grep -q 'reason=credential_operations_disabled' "$test_dir/credential.out"
[ ! -e "$RETENTION_CALLS" ]

rm -rf "$state_dir"
write_env true true false
request=$(write_request 107-1 retention-revoke)
if run_operation "$request" >"$test_dir/destructive.out" 2>&1; then
	printf '%s\n' 'destructive operation bypassed its independent gate' >&2
	exit 1
fi
grep -q 'reason=destructive_operations_disabled' "$test_dir/destructive.out"
[ ! -e "$RETENTION_CALLS" ]

rm -rf "$state_dir"
write_env true true false
request=$(write_request 108-1 retention-provision)
run_operation "$request" >/dev/null
[ "$(cat "$RETENTION_CALLS")" = provision ] || { printf '%s\n' 'retention provisioning received anything except its fixed mode' >&2; exit 1; }

rm -rf "$state_dir"
write_env true true false
request=$(write_request 109-1 acceptance-revoke)
if run_operation "$request" >"$test_dir/acceptance-destructive.out" 2>&1; then
	printf '%s\n' 'reserved acceptance revoke bypassed the destructive gate' >&2
	exit 1
fi
grep -q 'reason=destructive_operations_disabled' "$test_dir/acceptance-destructive.out"

rm -rf "$state_dir"
write_env true true true
request=$(write_request 110-1 acceptance-run)
if run_operation "$request" >"$test_dir/acceptance-unavailable.out" 2>&1; then
	printf '%s\n' 'unintegrated acceptance operation was reported as successful' >&2
	exit 1
fi
grep -q 'reason=operation_not_available' "$test_dir/acceptance-unavailable.out"

workflow="$repo_dir/.github/workflows/privacy-production-operations.yml"
grep -q '^    environment: production$' "$workflow"
grep -q '^          - preflight$' "$workflow"
grep -q '^          - status$' "$workflow"
if grep -Eq 'inputs\.(command|args|script|shell)' "$workflow"; then
	printf '%s\n' 'workflow exposes arbitrary command-shaped input' >&2
	exit 1
fi

printf '%s\n' 'privacy production operation state-machine tests passed'
