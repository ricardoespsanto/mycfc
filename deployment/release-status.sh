#!/bin/sh
set -eu

case "${1:-}" in
	--json)
		[ "$#" -eq 1 ] || { printf 'usage: %s [--json]\n' "$0" >&2; exit 2; }
		status_output=$(mktemp)
		trap 'rm -f "$status_output"' EXIT HUP INT TERM
		set +e
		MYCFC_RELEASE_STATUS_NESTED=1 sh "$0" >"$status_output"
		status=$?
		set -e
		jq -Rn '
			[inputs | capture("^(?<key>[^=]+)=(?<value>.*)$")] |
			reduce .[] as $item ({}; .[$item.key] = $item.value)
		' <"$status_output"
		rm -f "$status_output"
		trap - EXIT HUP INT TERM
		exit "$status"
		;;
	'') ;;
	*) printf 'usage: %s [--json]\n' "$0" >&2; exit 2 ;;
esac

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
state_dir=${MYCFC_DEPLOYMENT_STATE_DIR:-/etc/mycfc/deployment}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
pickup_window_seconds=${MYCFC_RELEASE_PICKUP_WINDOW_SECONDS:-60}
deployment_receipt_file="$state_dir/deployment-receipt.json"
publication_manifest_file="$state_dir/release-publication.json"
forwarding_status_file="$state_dir/cloudwatch-forwarding-status"

fail() {
	printf 'error=%s\n' "$1" >&2
	exit 1
}

read_timeline_value() {
	name=$1
	cat "$state_dir/release-$name-at" 2>/dev/null || printf 'unknown'
}

duration_between() {
	start=$1
	finish=$2
	case "$start:$finish" in
		*unknown*|*none*) printf 'unknown\n'; return ;;
	esac
	start_epoch=$(date -u -d "$start" +%s 2>/dev/null || true)
	finish_epoch=$(date -u -d "$finish" +%s 2>/dev/null || true)
	if [ -z "$start_epoch" ] || [ -z "$finish_epoch" ] || [ "$finish_epoch" -lt "$start_epoch" ]; then
		printf 'unknown\n'
	else
		printf '%s\n' "$((finish_epoch - start_epoch))"
	fi
}

[ "$(id -u)" -eq 0 ] || fail 'must run as root'
[ -f "$env_file" ] && [ "$(stat -c '%u:%a' "$env_file")" = '0:600' ] || fail 'missing or insecure environment file'
[ -f "$release_credentials_file" ] && [ "$(stat -c '%u:%a' "$release_credentials_file")" = '0:600' ] || fail 'missing or insecure release-agent AWS credentials file'

set -a
. "$env_file"
set +a
: "${AWS_REGION:?}"
: "${ECR_REPOSITORY_URL:?}"

unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_SHARED_CREDENTIALS_FILE="$release_credentials_file"
export AWS_PROFILE="$release_aws_profile"

case "$pickup_window_seconds" in
	''|*[!0-9]*) fail 'pickup window must be a positive number of seconds' ;;
	0) fail 'pickup window must be a positive number of seconds' ;;
esac

repository_name=${ECR_REPOSITORY_URL#*/}
tags=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --query 'imageDetails[].imageTags[]' --output text)
latest_tag=$(printf '%s\n' "$tags" | tr '\t' '\n' | awk '/^release-/' | sort | tail -n 1)
case "$latest_tag" in
	release-??????????????-????????????????????????????????????????) ;;
	*) fail 'ECR has no valid release tag' ;;
esac

latest_digest=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$repository_name" --image-ids imageTag="$latest_tag" --query 'imageDetails[0].imageDigest' --output text)
case "$latest_digest" in
	sha256:*) ;;
	*) fail 'latest release has no valid digest' ;;
esac
latest_sha=${latest_tag##*-}
release_stamp=${latest_tag#release-}
release_stamp=${release_stamp%%-*}
release_published_at="$(printf '%s-%s-%sT%s:%s:%sZ' "$(printf '%s' "$release_stamp" | cut -c1-4)" "$(printf '%s' "$release_stamp" | cut -c5-6)" "$(printf '%s' "$release_stamp" | cut -c7-8)" "$(printf '%s' "$release_stamp" | cut -c9-10)" "$(printf '%s' "$release_stamp" | cut -c11-12)" "$(printf '%s' "$release_stamp" | cut -c13-14)")"

active_slot=$(cat "$state_dir/active-slot" 2>/dev/null || printf 'unknown')
case "$active_slot" in
	blue|green) active_container="mycfc-production-app-$active_slot-1" ;;
	legacy) active_container='mycfc-production-app-1' ;;
	*) active_container='' ;;
esac

running_image=
running_digest=
running_sha=
running_version=
running_schema_migration_digest=
if [ -n "$active_container" ]; then
	running_image=$(docker inspect --format '{{.Config.Image}}' "$active_container" 2>/dev/null || true)
	running_sha=$(docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$active_container" 2>/dev/null || true)
	running_version=$(docker inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$active_container" 2>/dev/null || true)
	running_schema_migration_digest=$(docker inspect --format '{{index .Config.Labels "org.mycfc.schema-migration-digest"}}' "$active_container" 2>/dev/null || true)
	case "$running_image" in
		*@sha256:*) running_digest=${running_image##*@} ;;
	esac
fi

quarantined_digest=$(cat "$state_dir/failed-release-digest" 2>/dev/null || true)
last_attempt_digest=
last_attempt_result=
last_attempt_at=
if [ -f "$state_dir/last-attempt" ]; then
	IFS='	' read -r last_attempt_digest last_attempt_result last_attempt_at last_attempt_extra <"$state_dir/last-attempt" || true
	case "$last_attempt_digest:$last_attempt_result:$last_attempt_at:$last_attempt_extra" in
		sha256:*:checking:*:|sha256:*:succeeded:*:|sha256:*:failed:*:|sha256:*:quarantined:*:) ;;
		*) last_attempt_digest=; last_attempt_result=; last_attempt_at= ;;
	esac
else
	# Compatibility for hosts that have not yet written the atomic record.
	last_attempt_digest=$(cat "$state_dir/last-attempt-digest" 2>/dev/null || true)
	last_attempt_result=$(cat "$state_dir/last-attempt-result" 2>/dev/null || true)
	last_attempt_at=$(cat "$state_dir/last-attempt-at" 2>/dev/null || true)
fi
timeline_digest=$(cat "$state_dir/release-timeline-digest" 2>/dev/null || true)
timeline_tag=$(cat "$state_dir/release-timeline-tag" 2>/dev/null || true)
agent_started_at=unknown
release_detected_at=unknown
image_pulled_at=unknown
migration_completed_at=unknown
candidate_ready_at=unknown
traffic_switched_at=unknown
deployment_completed_at=unknown
if [ "$timeline_digest" = "$latest_digest" ] && [ "$timeline_tag" = "$latest_tag" ]; then
	agent_started_at=$(read_timeline_value agent-started)
	release_detected_at=$(read_timeline_value detected)
	image_pulled_at=$(read_timeline_value image-pulled)
	migration_completed_at=$(read_timeline_value migration-completed)
	candidate_ready_at=$(read_timeline_value candidate-ready)
	traffic_switched_at=$(read_timeline_value traffic-switched)
	deployment_completed_at=$(read_timeline_value deployment-completed)
	# A new release can reset the multi-file timeline while this command reads
	# it. Accept the snapshot only when the tag and digest are stable afterward.
	timeline_digest_after=$(cat "$state_dir/release-timeline-digest" 2>/dev/null || true)
	timeline_tag_after=$(cat "$state_dir/release-timeline-tag" 2>/dev/null || true)
	if [ "$timeline_digest_after" != "$timeline_digest" ] || [ "$timeline_tag_after" != "$timeline_tag" ]; then
		agent_started_at=unknown
		release_detected_at=unknown
		image_pulled_at=unknown
		migration_completed_at=unknown
		candidate_ready_at=unknown
		traffic_switched_at=unknown
		deployment_completed_at=unknown
	fi
fi
last_agent_result=$(systemctl show mycfc-pull-release.service --property=Result --value 2>/dev/null || printf 'unknown')
last_agent_exit_status=$(systemctl show mycfc-pull-release.service --property=ExecMainStatus --value 2>/dev/null || printf 'unknown')
last_agent_finished_at=$(systemctl show mycfc-pull-release.service --property=ExecMainExitTimestamp --value 2>/dev/null || printf 'unknown')
release_timer_state=$(systemctl is-active mycfc-pull-release.timer 2>/dev/null || printf 'unknown')
privacy_worker_state=$(systemctl is-active mycfc-privacy-worker.service 2>/dev/null || printf 'unknown')

guardian_activation_configuration=absent
[ -f /etc/mycfc/guardian-activation.env ] && [ -f /etc/mycfc/guardian-activation/approval.json ] && guardian_activation_configuration=present
privacy_activation_configuration=absent
[ -f /etc/mycfc/privacy-activation.env ] && [ -d /etc/mycfc/privacy-activation/evidence ] && privacy_activation_configuration=present
privacy_worker_configuration=absent
[ -f /etc/mycfc/privacy-worker.env ] && privacy_worker_configuration=present

receipt_contract=none
receipt_version=unknown
receipt_schema_migration_digest=unknown
receipt_result=none
receipt_finished_at=unknown
receipt_sha=unknown
receipt_digest=unknown
receipt_release_tag=unknown
receipt_manifest_sha256=unknown
receipt_traffic_switched=unknown
receipt_rollback_performed=unknown
receipt_guardian_intake=unknown
receipt_privacy_worker=unknown
receipt_privacy_worker_activation_required=unknown
if [ -f "$deployment_receipt_file" ] && jq -e '
	(keys | sort) == ["actual_gates","contract","failure_phase","finished_at","git_sha","image","privacy_worker_activation_required","publication_manifest_sha256","release_tag","result","rollback_performed","schema_migration_digest","slot","started_at","traffic_switched","version"] and
	.contract == "mycfc/deployment-receipt/v1" and (.image | keys | sort) == ["digest","repository"]
' "$deployment_receipt_file" >/dev/null 2>&1; then
	receipt_contract=$(jq -r .contract "$deployment_receipt_file")
	receipt_version=$(jq -r .version "$deployment_receipt_file")
	receipt_schema_migration_digest=$(jq -r .schema_migration_digest "$deployment_receipt_file")
	receipt_result=$(jq -r .result "$deployment_receipt_file")
	receipt_finished_at=$(jq -r .finished_at "$deployment_receipt_file")
	receipt_sha=$(jq -r .git_sha "$deployment_receipt_file")
	receipt_digest=$(jq -r .image.digest "$deployment_receipt_file")
	receipt_release_tag=$(jq -r .release_tag "$deployment_receipt_file")
	receipt_manifest_sha256=$(jq -r .publication_manifest_sha256 "$deployment_receipt_file")
	receipt_traffic_switched=$(jq -r .traffic_switched "$deployment_receipt_file")
	receipt_rollback_performed=$(jq -r .rollback_performed "$deployment_receipt_file")
	receipt_guardian_intake=$(jq -r .actual_gates.guardian_intake "$deployment_receipt_file")
	receipt_privacy_worker=$(jq -r .actual_gates.privacy_worker "$deployment_receipt_file")
	receipt_privacy_worker_activation_required=$(jq -r .privacy_worker_activation_required "$deployment_receipt_file")
fi

manifest_contract=none
manifest_version=unknown
manifest_schema_migration_digest=unknown
manifest_migration_inventory=unknown
manifest_sha256=unknown
manifest_guardian_intake=unknown
manifest_privacy_worker=unknown
if [ -f "$publication_manifest_file" ] && jq -e '
	(keys | sort) == ["ci_run_id","contract","expected_gates","git_sha","git_tree_sha","image","issues","published_at","release_tag","schema","version"] and
	.contract == "mycfc/release-publication/v1"
' "$publication_manifest_file" >/dev/null 2>&1; then
	manifest_contract=$(jq -r .contract "$publication_manifest_file")
	manifest_version=$(jq -r .version "$publication_manifest_file")
	manifest_schema_migration_digest=$(jq -r .schema.migration_digest "$publication_manifest_file")
	manifest_migration_inventory=$(jq -r '.schema.ordered_migrations | join(",")' "$publication_manifest_file")
	manifest_guardian_intake=$(jq -r .expected_gates.guardian_intake "$publication_manifest_file")
	manifest_privacy_worker=$(jq -r .expected_gates.privacy_worker "$publication_manifest_file")
	manifest_sha256=$(sha256sum "$publication_manifest_file" | awk '{print $1}')
fi

public_health=unknown
if [ -n "${MYCFC_DOMAIN:-}" ]; then
	if curl --fail --silent --show-error --max-time 10 "https://$MYCFC_DOMAIN/health/ready" | grep -Fxq ok; then public_health=healthy; else public_health=unhealthy; fi
fi

cloudwatch_forwarding_result=unknown
cloudwatch_forwarding_at=unknown
if [ -f "$forwarding_status_file" ]; then
	IFS='	' read -r cloudwatch_forwarding_result cloudwatch_forwarding_at forwarding_extra <"$forwarding_status_file" || true
	case "$cloudwatch_forwarding_result:$cloudwatch_forwarding_at:$forwarding_extra" in
		succeeded:*Z:|failed:*Z:) ;;
		*) cloudwatch_forwarding_result=unknown; cloudwatch_forwarding_at=unknown ;;
	esac
fi

released_epoch=$(date -u -d "$(printf '%s-%s-%s %s:%s:%s UTC' "$(printf '%s' "$release_stamp" | cut -c1-4)" "$(printf '%s' "$release_stamp" | cut -c5-6)" "$(printf '%s' "$release_stamp" | cut -c7-8)" "$(printf '%s' "$release_stamp" | cut -c9-10)" "$(printf '%s' "$release_stamp" | cut -c11-12)" "$(printf '%s' "$release_stamp" | cut -c13-14)")" +%s)
now_epoch=$(date -u +%s)
release_age_seconds=$((now_epoch - released_epoch))

if [ "$running_digest" = "$latest_digest" ]; then
	state=current
elif [ -n "$quarantined_digest" ] && [ "$quarantined_digest" = "$latest_digest" ]; then
	state=quarantined
elif [ "$last_attempt_digest" = "$latest_digest" ] && [ "$last_attempt_result" = failed ]; then
	state=failed
elif [ "$last_attempt_digest" = "$latest_digest" ] && [ "$last_attempt_result" = checking ] && { [ "$last_agent_result" = failed ] || { [ "$last_agent_exit_status" != unknown ] && [ "$last_agent_exit_status" != 0 ]; }; }; then
	state=failed
elif [ -z "$last_attempt_digest" ] && { [ "$last_agent_result" = failed ] || { [ "$last_agent_exit_status" != unknown ] && [ "$last_agent_exit_status" != 0 ]; }; }; then
	state=failed
elif [ "$agent_started_at" = unknown ] && [ "$release_age_seconds" -gt "$pickup_window_seconds" ]; then
	state=delayed
else
	state=pending
fi

printf 'state=%s\n' "$state"
printf 'latest_release_tag=%s\n' "$latest_tag"
printf 'latest_release_sha=%s\n' "$latest_sha"
printf 'latest_release_digest=%s\n' "$latest_digest"
printf 'release_age_seconds=%s\n' "$release_age_seconds"
printf 'pickup_window_seconds=%s\n' "$pickup_window_seconds"
printf 'release_published_at=%s\n' "$release_published_at"
printf 'agent_started_at=%s\n' "$agent_started_at"
printf 'release_detected_at=%s\n' "$release_detected_at"
printf 'image_pulled_at=%s\n' "$image_pulled_at"
printf 'migration_completed_at=%s\n' "$migration_completed_at"
printf 'candidate_ready_at=%s\n' "$candidate_ready_at"
printf 'traffic_switched_at=%s\n' "$traffic_switched_at"
printf 'deployment_completed_at=%s\n' "$deployment_completed_at"
printf 'publication_to_agent_start_seconds=%s\n' "$(duration_between "$release_published_at" "$agent_started_at")"
printf 'publication_to_detection_seconds=%s\n' "$(duration_between "$release_published_at" "$release_detected_at")"
printf 'publication_to_traffic_switch_seconds=%s\n' "$(duration_between "$release_published_at" "$traffic_switched_at")"
printf 'publication_to_deployment_seconds=%s\n' "$(duration_between "$release_published_at" "$deployment_completed_at")"
printf 'active_slot=%s\n' "$active_slot"
printf 'running_release_sha=%s\n' "${running_sha:-unknown}"
printf 'running_release_digest=%s\n' "${running_digest:-unknown}"
printf 'running_release_version=%s\n' "${running_version:-unknown}"
printf 'running_schema_migration_digest=%s\n' "${running_schema_migration_digest:-unknown}"
printf 'public_health=%s\n' "$public_health"
printf 'cloudwatch_forwarding_result=%s\n' "$cloudwatch_forwarding_result"
printf 'cloudwatch_forwarding_at=%s\n' "$cloudwatch_forwarding_at"
printf 'last_agent_result=%s\n' "$last_agent_result"
printf 'last_agent_exit_status=%s\n' "$last_agent_exit_status"
printf 'last_agent_finished_at=%s\n' "$last_agent_finished_at"
printf 'last_attempt_digest=%s\n' "${last_attempt_digest:-none}"
printf 'last_attempt_result=%s\n' "${last_attempt_result:-none}"
printf 'last_attempt_at=%s\n' "${last_attempt_at:-unknown}"
printf 'quarantined_digest=%s\n' "${quarantined_digest:-none}"
printf 'release_timer_state=%s\n' "$release_timer_state"
printf 'privacy_worker_state=%s\n' "$privacy_worker_state"
printf 'guardian_activation_configuration=%s\n' "$guardian_activation_configuration"
printf 'privacy_activation_configuration=%s\n' "$privacy_activation_configuration"
printf 'privacy_worker_configuration=%s\n' "$privacy_worker_configuration"
printf 'receipt_contract=%s\n' "$receipt_contract"
printf 'receipt_version=%s\n' "$receipt_version"
printf 'receipt_schema_migration_digest=%s\n' "$receipt_schema_migration_digest"
printf 'receipt_result=%s\n' "$receipt_result"
printf 'receipt_finished_at=%s\n' "$receipt_finished_at"
printf 'receipt_manifest_sha256=%s\n' "$receipt_manifest_sha256"
printf 'receipt_traffic_switched=%s\n' "$receipt_traffic_switched"
printf 'receipt_rollback_performed=%s\n' "$receipt_rollback_performed"
printf 'receipt_guardian_intake=%s\n' "$receipt_guardian_intake"
printf 'receipt_privacy_worker=%s\n' "$receipt_privacy_worker"
printf 'receipt_privacy_worker_activation_required=%s\n' "$receipt_privacy_worker_activation_required"
printf 'manifest_contract=%s\n' "$manifest_contract"
printf 'manifest_sha256=%s\n' "$manifest_sha256"
printf 'manifest_version=%s\n' "$manifest_version"
printf 'manifest_schema_migration_digest=%s\n' "$manifest_schema_migration_digest"
printf 'manifest_migration_inventory=%s\n' "$manifest_migration_inventory"
printf 'manifest_guardian_intake=%s\n' "$manifest_guardian_intake"
printf 'manifest_privacy_worker=%s\n' "$manifest_privacy_worker"

identity_match=unknown
if [ "$state" = current ]; then
	identity_match=false
	privacy_gate_match=false
	if [ "$receipt_privacy_worker" = "$manifest_privacy_worker" ] && [ "$receipt_privacy_worker_activation_required" = false ]; then
		privacy_gate_match=true
	elif [ "$manifest_privacy_worker:$receipt_privacy_worker:$receipt_privacy_worker_activation_required" = true:false:true ]; then
		privacy_gate_match=true
	fi
	if [ "$latest_sha" = "$running_sha" ] && [ "$latest_digest" = "$running_digest" ] && \
		[ "$receipt_sha" = "$latest_sha" ] && [ "$receipt_digest" = "$latest_digest" ] && [ "$receipt_release_tag" = "$latest_tag" ] && \
		[ "$receipt_result" = succeeded ] && [ "$receipt_manifest_sha256" = "$manifest_sha256" ] && \
		[ "$receipt_schema_migration_digest" = "$manifest_schema_migration_digest" ] && \
		[ "$receipt_guardian_intake" = "$manifest_guardian_intake" ] && [ "$privacy_gate_match" = true ] && \
		[ "$running_version" = "$manifest_version" ] && [ "$running_schema_migration_digest" = "$manifest_schema_migration_digest" ]; then
		identity_match=true
	fi
fi
printf 'identity_match=%s\n' "$identity_match"
[ "$identity_match" != false ] || exit 1
