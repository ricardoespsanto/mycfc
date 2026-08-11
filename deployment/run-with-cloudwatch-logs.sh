#!/bin/sh
set -eu

env_file=/etc/mycfc/mycfc.env
log_group=${CLOUDWATCH_LOG_GROUP:-/mycfc/production/deployment}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
release_credentials_file=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
release_aws_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
log_file=$(mktemp "$runtime_dir/mycfc-cloudwatch.XXXXXX")
upload_file=$(mktemp "$runtime_dir/mycfc-cloudwatch-upload.XXXXXX")
payload_file=$(mktemp "$runtime_dir/mycfc-cloudwatch-payload.XXXXXX")
trap 'rm -f "$log_file" "$upload_file" "$payload_file"' EXIT HUP INT TERM

if [ "$#" -eq 0 ]; then
	printf '%s\n' 'Missing command to run.' >&2
	exit 2
fi

set +e
"$@" >"$log_file" 2>&1
status=$?
set -e

cat "$log_file"

if [ ! -f "$env_file" ]; then
	printf '%s\n' "CloudWatch upload skipped: missing $env_file" >&2
	exit "$status"
fi

set -a
. "$env_file"
set +a

if [ ! -f "$release_credentials_file" ] || [ "$(stat -c '%u:%a' "$release_credentials_file")" != '0:600' ]; then
	printf '%s\n' 'CloudWatch upload skipped: missing or insecure release-agent AWS credentials file' >&2
	exit "$status"
fi

unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
export AWS_SHARED_CREDENTIALS_FILE="$release_credentials_file"
export AWS_PROFILE="$release_aws_profile"

stream=$(hostname | tr -d '\n' | tr -c 'A-Za-z0-9_.#-/' '-')
timestamp=$(($(date +%s) * 1000))

# Keep the event below CloudWatch Logs' 1 MB event limit while preserving the
# failure detail normally found at the end of command output.
tail -c 900000 "$log_file" >"$upload_file"
jq -n \
	--arg stream "$stream" \
	--arg status "$status" \
	--argjson timestamp "$timestamp" \
	--rawfile output "$upload_file" \
	'{logEvents: [{timestamp: $timestamp, message: ("host=" + $stream + " exit_status=" + $status + "\n" + $output)}]}' \
	>"$payload_file"

aws logs create-log-stream \
	--region "${AWS_REGION:-eu-west-1}" \
	--log-group-name "$log_group" \
	--log-stream-name "$stream" \
	>/dev/null 2>&1 || true

if ! aws logs put-log-events \
	--region "${AWS_REGION:-eu-west-1}" \
	--log-group-name "$log_group" \
	--log-stream-name "$stream" \
	--cli-input-json "file://$payload_file" \
	>/dev/null; then
	printf '%s\n' 'CloudWatch upload failed; local journal output remains available.' >&2
fi

exit "$status"
