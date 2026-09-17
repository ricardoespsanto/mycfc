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
cat >"$test_dir/deployment/privacy-policy-import.sh" <<'EOF'
#!/bin/sh
printf '%s\n' policy-import >>"$OPERATION_CALLS"
EOF
cat >"$test_dir/deployment/privacy-activation.sh" <<'EOF'
#!/bin/sh
printf 'activation %s\n' "$*" >>"$OPERATION_CALLS"
EOF
cat >"$test_dir/deployment/privacy-activation-courier-credentials.sh" <<'EOF'
#!/bin/sh
printf 'courier %s\n' "$*" >>"$OPERATION_CALLS"
EOF
cat >"$test_dir/deployment/privacy-activation-exchange.sh" <<'EOF'
#!/bin/sh
printf 'exchange %s\n' "$*" >>"$OPERATION_CALLS"
expires=$(date -u -d '+ 10 minutes' +%Y-%m-%dT%H:%M:%SZ)
printf 'event=privacy_activation_ceremony_opened ceremony_id=11111111-1111-4111-8111-111111111111 material_sha256=%s material_version_id=material-version-1 expires_at=%s source_sha=%s image_digest=%s schema_migration_digest=%s\n' \
	"$(printf 'c%.0s' $(seq 1 64))" "$expires" "$MOCK_SOURCE_SHA" "$MOCK_IMAGE_DIGEST" "$(printf 'd%.0s' $(seq 1 64))"
EOF
cat >"$test_dir/deployment/privacy-production-config.sh" <<'EOF'
#!/bin/sh
printf 'config %s\n' "$*" >>"$OPERATION_CALLS"
EOF
cat >"$test_dir/deployment/privacy-acceptance.sh" <<'EOF'
#!/bin/sh
printf 'acceptance %s\n' "$*" >>"$OPERATION_CALLS"
EOF
cat >"$test_dir/deployment/legacy-media-purge.sh" <<'EOF'
#!/bin/sh
printf 'legacy %s\n' "$*" >>"$OPERATION_CALLS"
mkdir -p "$(dirname -- "${4:-$3}")"
case "$1" in
	inventory) printf '%s\n' '{"versions":0,"delete_markers":0}' >"$3" ;;
	execute) printf '%s\n' '{}' >"$4" ;;
esac
EOF
chmod 0755 "$test_dir/bin/"* "$test_dir/deployment/"*.sh

sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
digest=$(printf 'b%.0s' $(seq 1 64))
image="123456789012.dkr.ecr.eu-west-1.amazonaws.com/mycfc-production@sha256:$digest"
state_dir="$test_dir/state"
env_file="$test_dir/mycfc.env"
lock_file="$test_dir/operation.lock"
release_lock_file="$test_dir/release.lock"
export RETENTION_CALLS="$test_dir/retention.calls"
export OPERATION_CALLS="$test_dir/operation.calls"
mkdir -p "$test_dir/control" "$test_dir/legacy" "$test_dir/evidence"
export MOCK_SOURCE_SHA="$sha" MOCK_IMAGE_DIGEST="sha256:$digest"

write_env() {
	enabled=$1
	credentials=${2:-false}
	destructive=${3:-false}
	cat >"$env_file" <<EOF
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=$enabled
PRIVACY_PRODUCTION_CREDENTIAL_OPERATIONS_ENABLED=$credentials
PRIVACY_PRODUCTION_DESTRUCTIVE_OPERATIONS_ENABLED=$destructive
PRIVACY_PRODUCTION_ACTIVATION_OPERATIONS_ENABLED=${4:-false}
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
		--arg evidence "${4:-0000000000000000000000000000000000000000000000000000000000000000}" \
		--arg issued "$issued" --arg expires "$expires" \
		--argjson run "${id%-*}" --argjson attempt "${id#*-}" \
		'{contract:"mycfc/privacy-production-operation-request/v2",request_id:$id,operation:$op,source_sha:$sha,expected_image:$image,evidence_sha256:$evidence,issued_at:$issued,expires_at:$expires,workflow_run_id:$run,workflow_run_attempt:$attempt}' \
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
		MYCFC_PRIVACY_OPERATION_CONTROL_DIR="$test_dir/control" \
		MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR="$test_dir/evidence" \
		MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_DIR="$test_dir/legacy" \
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
	(keys | sort) == ["contract","evidence_sha256","expected_image","finished_at","operation","reason","request_id","request_sha256","result","services","source_sha","started_at"] and
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
write_env true true false false
request=$(write_request 118-1 activation-courier-provision)
run_operation "$request" >/dev/null
grep -q '^courier provision$' "$OPERATION_CALLS"

rm -rf "$state_dir"
write_env true true false false
request=$(write_request 119-1 activation-courier-revoke)
if run_operation "$request" >"$test_dir/courier-revoke.out" 2>&1; then
	printf '%s\n' 'courier revoke bypassed the destructive gate' >&2
	exit 1
fi
grep -q 'reason=destructive_operations_disabled' "$test_dir/courier-revoke.out"

rm -rf "$state_dir"
write_env true true true false
request=$(write_request 110-1 acceptance-run)
if run_operation "$request" >"$test_dir/acceptance-activation-gate.out" 2>&1; then
	printf '%s\n' 'synthetic acceptance bypassed the activation gate' >&2
	exit 1
fi
grep -q 'reason=activation_operations_disabled' "$test_dir/acceptance-activation-gate.out"

rm -rf "$state_dir"
write_env true true true true
request=$(write_request 117-1 acceptance-canary-retry)
run_operation "$request" >/dev/null
grep -q '^acceptance canary-retry$' "$OPERATION_CALLS"

rm -rf "$state_dir"
write_env true true true true
request=$(write_request 112-1 policy-import "$image" 98d80915d8911b296768eddb278934cfb6c0f49c731bd50b14358756c07a163e)
run_operation "$request" >/dev/null
grep -q '^policy-import$' "$OPERATION_CALLS"

rm -rf "$state_dir"
write_env true true true false
request=$(write_request 113-1 flags-enable)
if run_operation "$request" >"$test_dir/activation-gate.out" 2>&1; then
	printf '%s\n' 'activation operation bypassed its independent gate' >&2
	exit 1
fi
grep -q 'reason=activation_operations_disabled' "$test_dir/activation-gate.out"

rm -rf "$state_dir"
write_env true true true true
request=$(write_request 114-1 activation-disable)
run_operation "$request" >/dev/null
grep -q '^activation disable$' "$OPERATION_CALLS"

for file in restore-attestation.json infrastructure.json provider-registry.json schema-inventory.json restore-attestation.key artifact-public.key; do
	printf '%s\n' "$file" >"$test_dir/evidence/$file"
	chmod 0600 "$test_dir/evidence/$file"
done
jq -n --arg contract mycfc/privacy-activation-evidence-set/v1 \
	--arg restore "$(sha256sum "$test_dir/evidence/restore-attestation.json" | awk '{print $1}')" \
	--arg infrastructure "$(sha256sum "$test_dir/evidence/infrastructure.json" | awk '{print $1}')" \
	--arg provider "$(sha256sum "$test_dir/evidence/provider-registry.json" | awk '{print $1}')" \
	--arg schema "$(sha256sum "$test_dir/evidence/schema-inventory.json" | awk '{print $1}')" \
	--arg restore_key "$(sha256sum "$test_dir/evidence/restore-attestation.key" | awk '{print $1}')" \
	--arg artifact_key "$(sha256sum "$test_dir/evidence/artifact-public.key" | awk '{print $1}')" \
	'{contract:$contract,files:{"restore-attestation.json":$restore,"infrastructure.json":$infrastructure,"provider-registry.json":$provider,"schema-inventory.json":$schema,"restore-attestation.key":$restore_key,"artifact-public.key":$artifact_key}}' \
	>"$test_dir/control/activation-evidence-set.json"
chmod 0600 "$test_dir/control/activation-evidence-set.json"
manifest_sha=$(sha256sum "$test_dir/control/activation-evidence-set.json" | awk '{print $1}')

rm -rf "$state_dir"
write_env true true true false
request=$(write_request 120-1 activation-ceremony-open "$image" "$manifest_sha")
if run_operation "$request" >"$test_dir/ceremony-gate.out" 2>&1; then
	printf '%s\n' 'ceremony open bypassed the activation gate' >&2
	exit 1
fi
grep -q 'reason=activation_operations_disabled' "$test_dir/ceremony-gate.out"

rm -rf "$state_dir"
write_env true true true true
request=$(write_request 121-1 activation-ceremony-open "$image" "$manifest_sha")
output=$(run_operation "$request")
printf '%s' "$output" | grep -q '^event=privacy_activation_ceremony_opened .* request_id=121-1$'
grep -q '^exchange open$' "$OPERATION_CALLS"

rm -rf "$state_dir"
printf '%s\n' '{"contract":"mycfc/privacy-infrastructure-posture/v1"}' >"$test_dir/control/infrastructure.json"
chmod 0600 "$test_dir/control/infrastructure.json"
infrastructure_sha=$(sha256sum "$test_dir/control/infrastructure.json" | awk '{print $1}')
write_env true false false false
request=$(write_request 115-1 infrastructure-observe "$image" "$infrastructure_sha")
run_operation "$request" >/dev/null

rm -rf "$state_dir"
write_env true false true false
request=$(write_request 116-1 legacy-purge "$image" "$digest")
run_operation "$request" >/dev/null
grep -q "^legacy execute $image $digest $test_dir/legacy/purge-116-1.json$" "$OPERATION_CALLS"

workflow="$repo_dir/.github/workflows/privacy-production-operations.yml"
grep -q '^    environment: production$' "$workflow"
grep -q '^          - preflight$' "$workflow"
grep -q '^          - status$' "$workflow"
grep -q '^          - activation-disable$' "$workflow"
grep -q '^          - activation-courier-provision$' "$workflow"
grep -q '^          - activation-courier-rotate$' "$workflow"
grep -q '^          - activation-courier-revoke$' "$workflow"
grep -q '^          - activation-ceremony-open$' "$workflow"
if grep -Eq '^          - activation-(prepare|activate)$' "$workflow"; then
	printf '%s\n' 'workflow retained the manual approval-staging bypass' >&2
	exit 1
fi
grep -q '^          - worker-enable$' "$workflow"
grep -q '^          - acceptance-run$' "$workflow"
grep -q '^          - acceptance-canary-recovery$' "$workflow"
if grep -Eq 'inputs\.(command|args|script|shell)' "$workflow"; then
	printf '%s\n' 'workflow exposes arbitrary command-shaped input' >&2
	exit 1
fi
unit="$script_dir/mycfc-privacy-production-operation.service"
grep -q 'ReadWritePaths=.* /etc/mycfc/deployment ' "$unit"
grep -q '^ReadWritePaths=/etc/mycfc ' "$unit"
grep -q 'ReadOnlyPaths=.*-/etc/mycfc/privacy-acceptance ' "$unit"
grep -q -- '-/var/lib/mycfc/legacy-media-purge' "$unit"

printf '%s\n' 'privacy production operation state-machine tests passed'
