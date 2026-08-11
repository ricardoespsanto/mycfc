#!/bin/sh
set -eu

env_file=${MYCFC_ENV_FILE:-/etc/mycfc/mycfc.env}
state_dir=${MYCFC_DEPLOYMENT_STATE_DIR:-/etc/mycfc/deployment}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
pickup_window_seconds=${MYCFC_RELEASE_PICKUP_WINDOW_SECONDS:-300}

fail() {
	printf 'error=%s\n' "$1" >&2
	exit 1
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

active_slot=$(cat "$state_dir/active-slot" 2>/dev/null || printf 'unknown')
case "$active_slot" in
	blue|green) active_container="mycfc-production-app-$active_slot-1" ;;
	legacy) active_container='mycfc-production-app-1' ;;
	*) active_container='' ;;
esac

running_image=
running_digest=
running_sha=
if [ -n "$active_container" ]; then
	running_image=$(docker inspect --format '{{.Config.Image}}' "$active_container" 2>/dev/null || true)
	running_sha=$(docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$active_container" 2>/dev/null || true)
	case "$running_image" in
		*@sha256:*) running_digest=${running_image##*@} ;;
	esac
fi

quarantined_digest=$(cat "$state_dir/failed-release-digest" 2>/dev/null || true)
last_attempt_digest=$(cat "$state_dir/last-attempt-digest" 2>/dev/null || true)
last_attempt_result=$(cat "$state_dir/last-attempt-result" 2>/dev/null || true)
last_attempt_at=$(cat "$state_dir/last-attempt-at" 2>/dev/null || true)
last_agent_result=$(systemctl show mycfc-pull-release.service --property=Result --value 2>/dev/null || printf 'unknown')
last_agent_exit_status=$(systemctl show mycfc-pull-release.service --property=ExecMainStatus --value 2>/dev/null || printf 'unknown')
last_agent_finished_at=$(systemctl show mycfc-pull-release.service --property=ExecMainExitTimestamp --value 2>/dev/null || printf 'unknown')

release_stamp=${latest_tag#release-}
release_stamp=${release_stamp%%-*}
released_epoch=$(date -u -d "$(printf '%s-%s-%s %s:%s:%s UTC' "$(printf '%s' "$release_stamp" | cut -c1-4)" "$(printf '%s' "$release_stamp" | cut -c5-6)" "$(printf '%s' "$release_stamp" | cut -c7-8)" "$(printf '%s' "$release_stamp" | cut -c9-10)" "$(printf '%s' "$release_stamp" | cut -c11-12)" "$(printf '%s' "$release_stamp" | cut -c13-14)")" +%s)
now_epoch=$(date -u +%s)
release_age_seconds=$((now_epoch - released_epoch))

if [ "$running_digest" = "$latest_digest" ]; then
	state=current
elif [ -n "$quarantined_digest" ] && [ "$quarantined_digest" = "$latest_digest" ]; then
	state=quarantined
elif [ "$last_attempt_digest" = "$latest_digest" ] && [ "$last_attempt_result" = failed ]; then
	state=failed
elif [ -z "$last_attempt_digest" ] && { [ "$last_agent_result" = failed ] || { [ "$last_agent_exit_status" != unknown ] && [ "$last_agent_exit_status" != 0 ]; }; }; then
	state=failed
elif [ "$release_age_seconds" -gt "$pickup_window_seconds" ]; then
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
printf 'active_slot=%s\n' "$active_slot"
printf 'running_release_sha=%s\n' "${running_sha:-unknown}"
printf 'running_release_digest=%s\n' "${running_digest:-unknown}"
printf 'last_agent_result=%s\n' "$last_agent_result"
printf 'last_agent_exit_status=%s\n' "$last_agent_exit_status"
printf 'last_agent_finished_at=%s\n' "$last_agent_finished_at"
printf 'last_attempt_digest=%s\n' "${last_attempt_digest:-none}"
printf 'last_attempt_result=%s\n' "${last_attempt_result:-none}"
printf 'last_attempt_at=%s\n' "${last_attempt_at:-unknown}"
printf 'quarantined_digest=%s\n' "${quarantined_digest:-none}"
