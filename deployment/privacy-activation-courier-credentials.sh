#!/bin/sh
set -eu
umask 077

mode=${1:-}
exchange_env_file=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE:-/etc/mycfc/privacy-activation-exchange.env}
credential_dir=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIAL_DIR:-/etc/mycfc/privacy-activation-exchange}
admin_credentials=${MYCFC_RELEASE_AWS_CREDENTIALS_FILE:-/etc/mycfc/release-aws/credentials}
courier_credentials=${MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIALS_FILE:-$credential_dir/aws-credentials}
runtime_dir=${MYCFC_RUNTIME_DIR:-/run}
admin_profile=${MYCFC_RELEASE_AWS_PROFILE:-mycfc-release}
courier_profile=mycfc-privacy-activation-courier
work_dir=

event() { printf '%s\n' "$1"; }
fail() { event "event=privacy_activation_courier_credential_failed operation=${mode:-invalid} reason=$1"; exit 1; }

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	[ -z "$work_dir" ] || rm -rf -- "$work_dir"
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

protected_file() {
	[ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c '%u:%g:%a' "$1" 2>/dev/null || true)" = '0:0:600' ]
}

validate_profile() {
	file=$1
	profile=$2
	access_prefix=$3
	require_token=$4
	awk -v section_name="$profile" -v prefix="$access_prefix" -v token_required="$require_token" '
		/^[[:space:]]*(#|;|$)/ { next }
		$0 ~ "^\\[" section_name "\\][[:space:]]*$" { section = 1; next }
		/^\[/ { invalid = 1; next }
		!section { invalid = 1; next }
		$0 ~ "^[[:space:]]*aws_access_key_id[[:space:]]*=[[:space:]]*" prefix "[A-Z0-9]{16}[[:space:]]*$" { access++; next }
		/^[[:space:]]*aws_secret_access_key[[:space:]]*=[[:space:]]*[^[:space:]]+[[:space:]]*$/ { secret++; next }
		/^[[:space:]]*aws_session_token[[:space:]]*=[[:space:]]*[^[:space:]]+[[:space:]]*$/ { token++; next }
		{ invalid = 1 }
		END { exit !(access == 1 && secret == 1 && !invalid && ((token_required == "yes" && token == 1) || (token_required == "no" && token == 0))) }
	' "$file"
}

aws_admin() {
	env -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN -u AWS_PROFILE -u AWS_DEFAULT_PROFILE \
		AWS_SHARED_CREDENTIALS_FILE="$admin_credentials" AWS_PROFILE="$admin_profile" AWS_PAGER='' aws "$@"
}

aws_courier() {
	file=$1
	shift
	env -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN -u AWS_PROFILE -u AWS_DEFAULT_PROFILE \
		AWS_SHARED_CREDENTIALS_FILE="$file" AWS_PROFILE="$courier_profile" AWS_PAGER='' aws "$@"
}

case "$mode" in provision | rotate | revoke) ;; *) fail mode_invalid ;; esac
[ "$#" -eq 1 ] || fail arguments_invalid
[ "$(id -u)" -eq 0 ] || fail root_required
protected_file "$exchange_env_file" || fail exchange_environment_invalid
expected_arn=$(sed -n 's/^PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN=//p' "$exchange_env_file")
admin_expected_arn=$(sed -n 's/^PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN=//p' "$exchange_env_file")
region=$(sed -n 's/^AWS_REGION=//p' "$exchange_env_file")
printf '%s' "$expected_arn" | grep -Eq '^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:user/[A-Za-z0-9+=,.@_-]+$' || fail courier_arn_invalid
printf '%s' "$admin_expected_arn" | grep -Eq '^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:user/[A-Za-z0-9+=,.@_-]+$' || fail credential_admin_arn_invalid
printf '%s' "$region" | grep -Eq '^[a-z]{2}(-gov)?-[a-z]+-[0-9]+$' || fail region_invalid
user_name=${expected_arn##*/}

protected_file "$admin_credentials" || fail credential_admin_session_invalid
validate_profile "$admin_credentials" "$admin_profile" AKIA no || fail credential_admin_session_invalid
for command in aws jq mktemp stat; do command -v "$command" >/dev/null 2>&1 || fail runtime_dependency_missing; done
install -d -o root -g root -m 0700 "$credential_dir"
[ ! -L "$credential_dir" ] && [ "$(stat -c '%u:%g:%a' "$credential_dir")" = '0:0:700' ] || fail credential_directory_invalid
case "$runtime_dir" in /*) ;; *) fail runtime_directory_invalid ;; esac
work_dir=$(mktemp -d "$runtime_dir/mycfc-privacy-activation-courier.XXXXXX")
chmod 0700 "$work_dir"

admin_identity=$(aws_admin sts get-caller-identity --region "$region" --output json 2>/dev/null) || fail credential_admin_identity_unavailable
[ "$(printf '%s' "$admin_identity" | jq -r '.Arn // empty')" = "$admin_expected_arn" ] || fail credential_admin_identity_invalid
aws_admin iam list-access-keys --user-name "$user_name" --output json >"$work_dir/keys.json" 2>/dev/null || fail access_key_inventory_failed
jq -e --arg user "$user_name" '
	(.AccessKeyMetadata | type == "array" and length <= 2) and all(.AccessKeyMetadata[]; .UserName == $user and (.AccessKeyId | test("^AKIA[A-Z0-9]{16}$")) and (.Status == "Active" or .Status == "Inactive"))
' "$work_dir/keys.json" >/dev/null || fail access_key_inventory_invalid
key_count=$(jq '.AccessKeyMetadata | length' "$work_dir/keys.json")

case "$mode" in
	provision)
		[ "$key_count" -eq 0 ] || fail existing_access_key
		;;
	rotate)
		[ "$key_count" -eq 1 ] || fail rotation_requires_one_existing_key
		if ! protected_file "$courier_credentials" || ! validate_profile "$courier_credentials" "$courier_profile" AKIA no; then
			fail current_courier_credential_invalid
		fi
		old_id=$(sed -n 's/^[[:space:]]*aws_access_key_id[[:space:]]*=[[:space:]]*//p' "$courier_credentials" | tr -d '[:space:]')
		[ "$old_id" = "$(jq -r '.AccessKeyMetadata[0].AccessKeyId' "$work_dir/keys.json")" ] || fail current_courier_key_mismatch
		;;
	revoke) ;;
esac

if [ "$mode" = revoke ]; then
	jq -r '.AccessKeyMetadata[].AccessKeyId' "$work_dir/keys.json" | while IFS= read -r key_id; do
		aws_admin iam update-access-key --user-name "$user_name" --access-key-id "$key_id" --status Inactive >/dev/null 2>&1 || fail access_key_deactivation_failed
		aws_admin iam delete-access-key --user-name "$user_name" --access-key-id "$key_id" >/dev/null 2>&1 || fail access_key_deletion_failed
	done
	rm -f -- "$courier_credentials"
	event 'event=privacy_activation_courier_credential_succeeded operation=revoke active_keys=0'
	exit 0
fi

aws_admin iam create-access-key --user-name "$user_name" --output json >"$work_dir/new-key.json" 2>/dev/null || fail access_key_creation_failed
jq -e --arg user "$user_name" '
	.AccessKey.UserName == $user and .AccessKey.Status == "Active" and
	(.AccessKey.AccessKeyId | test("^AKIA[A-Z0-9]{16}$")) and
	(.AccessKey.SecretAccessKey | type == "string" and length >= 40)
' "$work_dir/new-key.json" >/dev/null || fail access_key_response_invalid
new_id=$(jq -r .AccessKey.AccessKeyId "$work_dir/new-key.json")
new_credentials=$work_dir/aws-credentials
{
	printf '[%s]\n' "$courier_profile"
	printf 'aws_access_key_id = %s\n' "$new_id"
	printf 'aws_secret_access_key = %s\n' "$(jq -r .AccessKey.SecretAccessKey "$work_dir/new-key.json")"
} >"$new_credentials"
chmod 0600 "$new_credentials"
new_identity=$(aws_courier "$new_credentials" sts get-caller-identity --region "$region" --output json 2>/dev/null) || {
	aws_admin iam delete-access-key --user-name "$user_name" --access-key-id "$new_id" >/dev/null 2>&1 || true
	fail new_courier_credential_invalid
}
if [ "$(printf '%s' "$new_identity" | jq -r '.Arn // empty')" != "$expected_arn" ]; then
	aws_admin iam delete-access-key --user-name "$user_name" --access-key-id "$new_id" >/dev/null 2>&1 || true
	fail new_courier_identity_mismatch
fi
temporary=$credential_dir/.aws-credentials.new
cp "$new_credentials" "$temporary"
chown root:root "$temporary"
chmod 0600 "$temporary"
mv -f "$temporary" "$courier_credentials"

if [ "$mode" = rotate ]; then
	aws_admin iam update-access-key --user-name "$user_name" --access-key-id "$old_id" --status Inactive >/dev/null 2>&1 || fail old_access_key_deactivation_failed
	aws_admin iam delete-access-key --user-name "$user_name" --access-key-id "$old_id" >/dev/null 2>&1 || fail old_access_key_deletion_failed
fi
event "event=privacy_activation_courier_credential_succeeded operation=$mode active_keys=1"
