#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
state_dir=${MYCFC_PRIVACY_OPERATION_STATE_DIR:-/var/lib/mycfc/privacy-operations}
deployment_dir=${MYCFC_DEPLOYMENT_DIR:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)}
control_dir=${MYCFC_PRIVACY_OPERATION_CONTROL_DIR:-/etc/mycfc/privacy-production-operations}
legacy_evidence_dir=${MYCFC_LEGACY_MEDIA_PURGE_EVIDENCE_DIR:-/var/lib/mycfc/legacy-media-purge}
request_file=${1:-}
receipt_output=${2:-}
lock_file=${MYCFC_PRIVACY_OPERATION_LOCK_FILE:-/run/mycfc-privacy-production-operation.lock}
release_lock_file=${MYCFC_RELEASE_LOCK_FILE:-/run/mycfc-pull-release.lock}

request_id=
operation=
source_sha=
expected_image=
request_sha256=
evidence_sha256=
receipt_trusted=false
started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

event() {
	printf '%s\n' "$1"
}

valid_request_id() {
	printf '%s' "$1" | grep -Eq '^[1-9][0-9]{0,19}-[1-9][0-9]{0,4}$'
}

valid_image() {
	printf '%s' "$1" | grep -Eq '^[0-9]{12}\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/mycfc-production@sha256:[0-9a-f]{64}$'
}

write_receipt() {
	result=$1
	reason=$2
	finished_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	temporary=$(mktemp "$state_dir/.privacy-operation-receipt.XXXXXX")
	trap 'rm -f "$temporary"' EXIT HUP INT TERM
	jq -cS -n \
		--arg contract 'mycfc/privacy-production-operation-receipt/v1' \
		--arg request_id "$request_id" \
		--arg operation "$operation" \
		--arg source_sha "$source_sha" \
		--arg expected_image "$expected_image" \
		--arg request_sha256 "$request_sha256" \
		--arg evidence_sha256 "$evidence_sha256" \
		--arg result "$result" \
		--arg reason "$reason" \
		--arg started_at "$started_at" \
		--arg finished_at "$finished_at" \
		--argjson worker_active "$(service_active mycfc-privacy-worker.service)" \
		--argjson retention_active "$(service_active mycfc-privacy-retention.service)" \
		--argjson restore_active "$(service_active mycfc-postgres-restore-drill.service)" \
		--argjson cleanup_active "$(service_active mycfc-postgres-backup-version-cleanup.service)" \
		'{contract:$contract,request_id:$request_id,operation:$operation,source_sha:$source_sha,expected_image:$expected_image,evidence_sha256:$evidence_sha256,request_sha256:$request_sha256,result:$result,reason:(if $reason == "" then null else $reason end),started_at:$started_at,finished_at:$finished_at,services:{privacy_worker:$worker_active,privacy_retention:$retention_active,privacy_restore:$restore_active,backup_cleanup:$cleanup_active}}' \
		>"$temporary"
	chmod 0600 "$temporary"
	mv "$temporary" "$receipt_output"
	trap - EXIT HUP INT TERM
}

reject() {
	reason=$1
	if [ -n "$request_id" ] && [ "$receipt_trusted" = true ] && [ -d "$state_dir" ]; then
		write_receipt REJECTED "$reason"
	fi
	event "event=privacy_operation_rejected request_id=${request_id:-untrusted} operation=${operation:-untrusted} reason=$reason"
	exit 1
}

service_active() {
	if systemctl is-active --quiet "$1" 2>/dev/null; then
		printf true
	else
		printf false
	fi
}

privacy_operation_container_active() {
	for container in \
		mycfc-production-privacy-worker-1 \
		mycfc-production-privacy-retention-1 \
		mycfc-production-privacy-acceptance-1 \
		mycfc-production-privacy-activation-1 \
		mycfc-production-privacy-activation-disable-1; do
		if [ "$(docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null || true)" = true ]; then
			return 0
		fi
	done
	return 1
}

operation_is_credential_change() {
	case "$1" in
		retention-provision | retention-rotate | retention-revoke | acceptance-provision | acceptance-rotate | acceptance-revoke | activation-disable-provision | activation-courier-provision | activation-courier-rotate | activation-courier-revoke) return 0 ;;
		*) return 1 ;;
	esac
}

operation_is_destructive() {
	case "$1" in
		retention-revoke | acceptance-revoke | acceptance-run | acceptance-canary-* | legacy-purge | legacy-credential-remove | retention-run | retention-enable | activation-courier-revoke | activation-ceremony-open | flags-enable | worker-enable) return 0 ;;
		*) return 1 ;;
	esac
}

operation_is_activation_change() {
	case "$1" in
		policy-import | acceptance-run | acceptance-canary-* | activation-record | activation-ceremony-open | flags-enable | worker-enable) return 0 ;;
		*) return 1 ;;
	esac
}

require_evidence_file() {
	file=$1
	contract=$2
	if [ "$evidence_sha256" = "$(printf '0%.0s' $(seq 1 64))" ] || [ ! -f "$file" ] || [ -L "$file" ] ||
		[ "$(stat -c '%u:%g:%a' "$file" 2>/dev/null || true)" != '0:0:600' ] ||
		[ "$(sha256sum "$file" | awk '{print $1}')" != "$evidence_sha256" ] ||
		! jq -e --arg contract "$contract" 'type == "object" and .contract == $contract' "$file" >/dev/null 2>&1; then
		return 1
	fi
}

require_manifest_binding() {
	manifest=$1
	shift
	for name in "$@"; do
		path=${MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR:-/etc/mycfc/privacy-activation/evidence}/$name
		expected=$(jq -r --arg name "$name" '.files[$name] // empty' "$manifest" 2>/dev/null || true)
		if ! printf '%s' "$expected" | grep -Eq '^[0-9a-f]{64}$' || [ ! -f "$path" ] || [ -L "$path" ] ||
			[ "$(stat -c '%u:%g:%a' "$path" 2>/dev/null || true)" != '0:0:600' ] ||
			[ "$(sha256sum "$path" | awk '{print $1}')" != "$expected" ]; then
			return 1
		fi
	done
}

if [ "$(id -u)" -ne 0 ]; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=root_required'
	exit 1
fi
if [ "$#" -ne 2 ] || [ -z "$request_file" ] || [ -z "$receipt_output" ]; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=arguments_invalid'
	exit 2
fi
if [ ! -f "$env_file" ] || [ -L "$env_file" ] || [ "$(stat -c '%u:%g:%a' "$env_file" 2>/dev/null || true)" != '0:0:600' ]; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=host_environment_invalid'
	exit 1
fi
if [ ! -f "$request_file" ] || [ -L "$request_file" ] || [ "$(stat -c '%u:%g:%a' "$request_file" 2>/dev/null || true)" != '0:0:600' ]; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=request_file_invalid'
	exit 1
fi

set -a
. "$env_file"
set +a
if [ "${PRIVACY_PRODUCTION_OPERATIONS_ENABLED:-false}" != true ]; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=operations_disabled'
	exit 1
fi

if ! jq -e '
	type == "object" and
	(keys | sort) == ["contract","evidence_sha256","expected_image","expires_at","issued_at","operation","request_id","source_sha","workflow_run_attempt","workflow_run_id"] and
	.contract == "mycfc/privacy-production-operation-request/v2" and
	(.request_id | type == "string") and
	(.operation | type == "string") and
	(.source_sha | type == "string" and test("^[0-9a-f]{40}$")) and
	(.expected_image | type == "string") and
	(.evidence_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
	(.issued_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
	(.expires_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
	(.workflow_run_id | type == "number" and . > 0 and floor == .) and
	(.workflow_run_attempt | type == "number" and . > 0 and floor == .) and
	.request_id == ((.workflow_run_id | tostring) + "-" + (.workflow_run_attempt | tostring))
' "$request_file" >/dev/null 2>&1; then
	event 'event=privacy_operation_rejected request_id=untrusted operation=untrusted reason=request_contract_invalid'
	exit 1
fi

request_id=$(jq -r .request_id "$request_file")
operation=$(jq -r .operation "$request_file")
source_sha=$(jq -r .source_sha "$request_file")
expected_image=$(jq -r .expected_image "$request_file")
evidence_sha256=$(jq -r .evidence_sha256 "$request_file")
issued_at=$(jq -r .issued_at "$request_file")
expires_at=$(jq -r .expires_at "$request_file")
request_sha256=$(sha256sum "$request_file" | awk '{print $1}')

valid_request_id "$request_id" || reject request_id_invalid
valid_image "$expected_image" || reject expected_image_invalid
case "$operation" in
	preflight | status | infrastructure-observe | policy-import | activation-disable-provision | \
	retention-provision | retention-rotate | retention-revoke | retention-run | retention-enable | retention-disable | \
	acceptance-provision | acceptance-rotate | acceptance-revoke | acceptance-run | \
	acceptance-canary-retry | acceptance-canary-failure | acceptance-canary-aged | acceptance-canary-heartbeat | acceptance-canary-recovery | \
	legacy-inventory | legacy-purge | legacy-verify | legacy-credential-remove | \
	backup-run | backup-posture | backup-cleanup-inventory | restore-run | restore-verify | \
	activation-record | activation-courier-provision | activation-courier-rotate | activation-courier-revoke | activation-ceremony-open | activation-disable | \
	flags-enable | flags-disable | worker-enable | worker-disable) ;;
	*) reject operation_not_allowlisted ;;
esac

issued_epoch=$(date -u -d "$issued_at" +%s 2>/dev/null || true)
expires_epoch=$(date -u -d "$expires_at" +%s 2>/dev/null || true)
now_epoch=$(date -u +%s)
case "$issued_epoch:$expires_epoch" in
	*[!0-9:]*) reject request_time_invalid ;;
esac
if [ "$issued_epoch" -gt "$((now_epoch + 30))" ] || [ "$expires_epoch" -le "$now_epoch" ] ||
	[ "$expires_epoch" -le "$issued_epoch" ] || [ "$((expires_epoch - issued_epoch))" -gt 900 ]; then
	reject request_expired_or_overlong
fi

case "${GIT_SHA:-}" in
	"$source_sha") ;;
	*) reject source_sha_not_active ;;
esac
case "${MYCFC_IMAGE:-}" in
	"$expected_image") ;;
	*) reject image_not_active ;;
esac

install -d -m 0700 "$state_dir"
if [ -L "$state_dir" ] || [ "$(stat -c '%u:%g:%a' "$state_dir" 2>/dev/null || true)" != '0:0:700' ]; then
	event "event=privacy_operation_rejected request_id=$request_id operation=$operation reason=state_directory_invalid"
	exit 1
fi
case "$receipt_output" in
	"$state_dir"/receipts/"$request_id".json) ;;
	*) reject receipt_path_invalid ;;
esac
install -d -m 0700 "$state_dir/receipts"
if [ -L "$state_dir/receipts" ] || [ "$(stat -c '%u:%g:%a' "$state_dir/receipts" 2>/dev/null || true)" != '0:0:700' ]; then
	event "event=privacy_operation_rejected request_id=$request_id operation=$operation reason=receipt_directory_invalid"
	exit 1
fi
receipt_trusted=true
if [ -e "$receipt_output" ]; then
	if [ -L "$receipt_output" ] || [ "$(stat -c '%u:%g:%a' "$receipt_output" 2>/dev/null || true)" != '0:0:600' ]; then
		event "event=privacy_operation_rejected request_id=$request_id operation=$operation reason=receipt_collision_invalid"
		exit 1
	fi
	previous_result=$(jq -r '.result // "UNKNOWN"' "$receipt_output" 2>/dev/null || printf UNKNOWN)
	event "event=privacy_operation_replayed request_id=$request_id operation=$operation result=$previous_result"
	exit 0
fi

exec 8>"$release_lock_file"
if ! flock -n 8; then
	reject release_locked
fi
exec 9>"$lock_file"
if ! flock -n 9; then
	reject operation_locked
fi

if operation_is_credential_change "$operation" && [ "${PRIVACY_PRODUCTION_CREDENTIAL_OPERATIONS_ENABLED:-false}" != true ]; then
	reject credential_operations_disabled
fi
if operation_is_destructive "$operation" && [ "${PRIVACY_PRODUCTION_DESTRUCTIVE_OPERATIONS_ENABLED:-false}" != true ]; then
	reject destructive_operations_disabled
fi
if operation_is_activation_change "$operation" && [ "${PRIVACY_PRODUCTION_ACTIVATION_OPERATIONS_ENABLED:-false}" != true ]; then
	reject activation_operations_disabled
fi

event "event=privacy_operation_started request_id=$request_id operation=$operation"
case "$operation" in
	status)
		:
		;;
	preflight)
		if [ "$(service_active mycfc-privacy-worker.service)" = true ] ||
			[ "$(service_active mycfc-privacy-retention.service)" = true ] ||
			[ "$(service_active mycfc-postgres-restore-drill.service)" = true ] ||
			[ "$(service_active mycfc-postgres-backup-version-cleanup.service)" = true ] ||
			privacy_operation_container_active; then
			reject privacy_activity_not_quiescent
		fi
		;;
	infrastructure-observe)
		if ! require_evidence_file "$control_dir/infrastructure.json" mycfc/privacy-infrastructure-posture/v1; then
			reject infrastructure_evidence_invalid
		fi
		;;
	policy-import)
		if [ "$evidence_sha256" != 98d80915d8911b296768eddb278934cfb6c0f49c731bd50b14358756c07a163e ] ||
			! "$deployment_dir/privacy-policy-import.sh" >/dev/null 2>&1; then
			reject policy_import_failed
		fi
		;;
	activation-disable-provision)
		if ! "$deployment_dir/privacy-activation.sh" provision-disable >/dev/null 2>&1; then
			reject activation_disable_provision_failed
		fi
		;;
	activation-courier-provision | activation-courier-rotate | activation-courier-revoke)
		mode=${operation#activation-courier-}
		if ! "$deployment_dir/privacy-activation-courier-credentials.sh" "$mode"; then
			reject activation_courier_credential_failed
		fi
		;;
	retention-provision)
		if ! "$deployment_dir/privacy-retention.sh" provision >/dev/null 2>&1; then
			reject retention_provision_failed
		fi
		;;
	retention-rotate)
		if ! "$deployment_dir/privacy-retention.sh" rotate >/dev/null 2>&1; then
			reject retention_rotate_failed
		fi
		;;
	retention-revoke)
		if ! "$deployment_dir/privacy-retention.sh" revoke >/dev/null 2>&1; then
			reject retention_revoke_failed
		fi
		;;
	retention-run)
		if ! "$deployment_dir/privacy-retention.sh" run >/dev/null 2>&1; then reject retention_run_failed; fi
		;;
	retention-enable | retention-disable)
		if ! "$deployment_dir/privacy-production-config.sh" "$operation" >/dev/null 2>&1; then reject retention_config_failed; fi
		;;
	acceptance-provision | acceptance-rotate | acceptance-revoke)
		mode=${operation#acceptance-}
		if ! "$deployment_dir/privacy-acceptance.sh" "$mode" >/dev/null 2>&1; then reject acceptance_credential_failed; fi
		;;
	acceptance-run | acceptance-canary-retry | acceptance-canary-failure | acceptance-canary-aged | acceptance-canary-heartbeat | acceptance-canary-recovery)
		case "$operation" in
			acceptance-run) mode=run ;;
			acceptance-canary-*) mode=${operation#acceptance-} ;;
		esac
		output="$state_dir/acceptance/$request_id.json"
		if ! MYCFC_PRIVACY_OPERATION_STATE_DIR="$state_dir" MYCFC_PRIVACY_ACCEPTANCE_EVIDENCE_OUTPUT="$output" \
			"$deployment_dir/privacy-acceptance.sh" "$mode"; then
			reject acceptance_run_failed
		fi
		;;
	legacy-inventory)
		output=$legacy_evidence_dir/inventory-$request_id.json
		if ! "$deployment_dir/legacy-media-purge.sh" inventory "$expected_image" "$output" >/dev/null 2>&1; then reject legacy_inventory_failed; fi
		;;
	legacy-purge)
		if [ "$evidence_sha256" = "$(printf '0%.0s' $(seq 1 64))" ]; then reject legacy_inventory_approval_missing; fi
		output=$legacy_evidence_dir/purge-$request_id.json
		if ! "$deployment_dir/legacy-media-purge.sh" execute "$expected_image" "$evidence_sha256" "$output" >/dev/null 2>&1; then reject legacy_purge_failed; fi
		;;
	legacy-verify)
		output=$legacy_evidence_dir/verify-$request_id.json
		if ! "$deployment_dir/legacy-media-purge.sh" inventory "$expected_image" "$output" >/dev/null 2>&1 ||
			! jq -e '.versions == 0 and .delete_markers == 0' "$output" >/dev/null 2>&1; then reject legacy_absence_not_proven; fi
		;;
	legacy-credential-remove)
		teardown=$control_dir/legacy-purge-teardown.json
		if ! require_evidence_file "$teardown" mycfc/legacy-media-purge-teardown/v1 ||
			! jq -e '.identity_absent == true and .access_keys_absent == true and (.terraform_apply_sha256 | test("^[0-9a-f]{64}$"))' "$teardown" >/dev/null 2>&1; then
			reject legacy_teardown_evidence_invalid
		fi
		rm -f /etc/mycfc/legacy-media-purge/aws-credentials
		;;
	backup-run)
		if ! systemctl start mycfc-postgres-backup.service; then reject backup_run_failed; fi
		;;
	backup-posture)
		if ! systemctl start mycfc-hetzner-backup-posture.service; then reject backup_posture_failed; fi
		;;
	backup-cleanup-inventory)
		cleanup_env=/etc/mycfc/backup-cleanup.env
		if [ ! -f "$cleanup_env" ] || [ -L "$cleanup_env" ] ||
			[ "$(stat -c '%u:%g:%a' "$cleanup_env" 2>/dev/null || true)" != '0:0:600' ] ||
			! grep -Eq "^BACKUP_NONCURRENT_CLEANER_DRY_RUN='?true'?$" "$cleanup_env" ||
			! systemctl start mycfc-postgres-backup-version-cleanup.service; then reject backup_cleanup_inventory_failed; fi
		;;
	restore-run)
		if ! systemctl start mycfc-postgres-restore-drill.service; then reject restore_run_failed; fi
		;;
	restore-verify)
		if ! "$deployment_dir/verify-privacy-restore-attestation.sh" "$expected_image" >/dev/null 2>&1; then reject restore_attestation_invalid; fi
		;;
	activation-record)
		manifest=$control_dir/activation-evidence-set.json
		if ! require_evidence_file "$manifest" mycfc/privacy-activation-evidence-set/v1 ||
			! require_manifest_binding "$manifest" restore-attestation.json infrastructure.json provider-registry.json schema-inventory.json restore-attestation.key artifact-public.key ||
			! "$deployment_dir/privacy-activation.sh" record-evidence >/dev/null 2>&1; then reject activation_evidence_failed; fi
		;;
	activation-ceremony-open)
		manifest=$control_dir/activation-evidence-set.json
		if ! require_evidence_file "$manifest" mycfc/privacy-activation-evidence-set/v1 ||
			! require_manifest_binding "$manifest" restore-attestation.json infrastructure.json provider-registry.json schema-inventory.json restore-attestation.key artifact-public.key; then reject activation_evidence_invalid; fi
		open_event=$("$deployment_dir/privacy-activation-exchange.sh" open) || {
			reject activation_ceremony_open_failed
		}
		if ! printf '%s' "$open_event" | grep -Eq '^event=privacy_activation_ceremony_opened ceremony_id=[0-9a-f-]{36} material_sha256=[0-9a-f]{64} material_version_id=[-A-Za-z0-9._~+/=]{1,1024} expires_at=[0-9TZ:-]{20} source_sha=[0-9a-f]{40} image_digest=sha256:[0-9a-f]{64} schema_migration_digest=[0-9a-f]{64}$'; then
			reject activation_ceremony_open_receipt_invalid
		fi
		event "$open_event request_id=$request_id"
		;;
	activation-disable)
		if ! "$deployment_dir/privacy-activation.sh" disable >/dev/null 2>&1; then reject activation_disable_failed; fi
		;;
	flags-enable | flags-disable | worker-enable | worker-disable)
		if ! "$deployment_dir/privacy-production-config.sh" "$operation" >/dev/null 2>&1; then reject production_config_failed; fi
		;;
esac

write_receipt SUCCEEDED ''
event "event=privacy_operation_succeeded request_id=$request_id operation=$operation receipt_contract=mycfc/privacy-production-operation-receipt/v1"
