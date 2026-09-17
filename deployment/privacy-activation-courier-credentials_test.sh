#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/credentials"
courier_arn=arn:aws:iam::123456789012:user/mycfc-production-privacy-activation-exchange-courier
admin_arn=arn:aws:iam::123456789012:user/mycfc-production-release-agent
user_name=${courier_arn##*/}
cat >"$test_dir/exchange.env" <<EOF
AWS_REGION=eu-west-1
PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN=$courier_arn
PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN=$admin_arn
EOF
chmod 0600 "$test_dir/exchange.env"

write_admin() {
	cat >"$test_dir/release-credentials" <<'EOF'
[mycfc-release]
aws_access_key_id = AKIACCCCCCCCCCCCCCCC
aws_secret_access_key = standing-release-secret-01234567890123456789
EOF
	chmod 0600 "$test_dir/release-credentials"
}

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
for argument in "$@"; do path=$argument; done
if [ -d "$path" ]; then printf '%s\n' '0:0:700'; else printf '%s\n' '0:0:600'; fi
EOF
cat >"$test_dir/bin/install" <<'EOF'
#!/bin/sh
for argument in "$@"; do case "$argument" in -*) ;; root | 0700 | 0600) ;; *) mkdir -p "$argument"; chmod 0700 "$argument" ;; esac; done
EOF
cat >"$test_dir/bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$test_dir/bin/id" "$test_dir/bin/stat" "$test_dir/bin/install" "$test_dir/bin/chown"

cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
set -eu
printf '%s %s\n' "$AWS_PROFILE" "$*" >>"$AWS_CALLS"
service=$1
operation=$2
shift 2
case "$service:$operation" in
	sts:get-caller-identity)
		if [ "$AWS_PROFILE" = mycfc-privacy-activation-courier ]; then arn=$COURIER_ARN; else arn=$ADMIN_ARN; fi
		printf '{"Arn":"%s"}\n' "$arn"
		;;
	iam:list-access-keys)
		jq -Rn --arg user "$COURIER_USER" '[inputs | select(length > 0) | {UserName:$user,AccessKeyId:.,Status:"Active"}] | {AccessKeyMetadata:.}' <"$KEY_STATE"
		;;
	iam:create-access-key)
		printf '%s\n' "$NEW_KEY_ID" >>"$KEY_STATE"
		printf '{"AccessKey":{"UserName":"%s","AccessKeyId":"%s","Status":"Active","SecretAccessKey":"%s"}}\n' "$COURIER_USER" "$NEW_KEY_ID" "$NEW_SECRET"
		;;
	iam:update-access-key) ;;
	iam:delete-access-key)
		key=
		while [ "$#" -gt 0 ]; do case "$1" in --access-key-id) key=$2; shift 2 ;; *) shift ;; esac; done
		grep -vx "$key" "$KEY_STATE" >"$KEY_STATE.tmp" || true
		mv "$KEY_STATE.tmp" "$KEY_STATE"
		;;
	*) exit 90 ;;
esac
EOF
chmod 0755 "$test_dir/bin/aws"

export AWS_CALLS="$test_dir/aws.calls" KEY_STATE="$test_dir/key-state" COURIER_ARN="$courier_arn" ADMIN_ARN="$admin_arn" COURIER_USER="$user_name"
: >"$KEY_STATE"

run_lifecycle() {
	PATH="$test_dir/bin:$PATH" NEW_KEY_ID="$NEW_KEY_ID" NEW_SECRET="$NEW_SECRET" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$test_dir/exchange.env" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_CREDENTIAL_DIR="$test_dir/credentials" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$test_dir/release-credentials" \
		MYCFC_RUNTIME_DIR="$test_dir" \
		sh "$script_dir/privacy-activation-courier-credentials.sh" "$1"
}

write_admin
NEW_KEY_ID=AKIAAAAAAAAAAAAAAAAA NEW_SECRET=first-secret-not-real-012345678901234567890123456789
export NEW_KEY_ID NEW_SECRET
output=$(run_lifecycle provision)
[ "$output" = 'event=privacy_activation_courier_credential_succeeded operation=provision active_keys=1' ]
[ -e "$test_dir/release-credentials" ]
grep -q '^aws_access_key_id = AKIAAAAAAAAAAAAAAAAA$' "$test_dir/credentials/aws-credentials"
[ "$(cat "$KEY_STATE")" = AKIAAAAAAAAAAAAAAAAA ]

write_admin
NEW_KEY_ID=AKIABBBBBBBBBBBBBBBB NEW_SECRET=second-secret-not-real-01234567890123456789012345678
export NEW_KEY_ID NEW_SECRET
output=$(run_lifecycle rotate)
[ "$output" = 'event=privacy_activation_courier_credential_succeeded operation=rotate active_keys=1' ]
grep -q '^aws_access_key_id = AKIABBBBBBBBBBBBBBBB$' "$test_dir/credentials/aws-credentials"
[ "$(cat "$KEY_STATE")" = AKIABBBBBBBBBBBBBBBB ]
grep -q 'iam update-access-key .*AKIAAAAAAAAAAAAAAAAA .*Inactive' "$AWS_CALLS"
grep -q 'iam delete-access-key .*AKIAAAAAAAAAAAAAAAAA' "$AWS_CALLS"

write_admin
output=$(run_lifecycle revoke)
[ "$output" = 'event=privacy_activation_courier_credential_succeeded operation=revoke active_keys=0' ]
[ ! -e "$test_dir/credentials/aws-credentials" ]
[ ! -s "$KEY_STATE" ]
[ -e "$test_dir/release-credentials" ]

cat >"$test_dir/release-credentials" <<'EOF'
[mycfc-release]
aws_access_key_id = ASIACCCCCCCCCCCCCCCC
aws_secret_access_key = forbidden-temporary-key
aws_session_token = forbidden-session-token
EOF
chmod 0600 "$test_dir/release-credentials"
: >"$AWS_CALLS"
if run_lifecycle provision >"$test_dir/standing.out" 2>&1; then
	printf '%s\n' 'standing administrator key unexpectedly entered credential lifecycle' >&2
	exit 1
fi
grep -q 'reason=credential_admin_session_invalid' "$test_dir/standing.out"
[ ! -s "$AWS_CALLS" ]
[ -e "$test_dir/release-credentials" ]

if grep -Eq 'first-secret|second-secret|standing-release-secret|forbidden-session-token' "$AWS_CALLS"; then
	printf '%s\n' 'credential secret leaked into command logs' >&2
	exit 1
fi
printf '%s\n' 'privacy activation courier provision, rotation, revocation and fixed release-agent administrator tests passed'
