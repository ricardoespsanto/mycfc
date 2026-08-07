#!/bin/sh
set -eu

env_file=${1:-/etc/mycfc/mycfc.env}

if [ ! -f "$env_file" ] || [ "$(stat -c '%u:%a' "$env_file")" != '0:600' ]; then
	printf '%s\n' "$env_file must be owned by root and have mode 0600." >&2
	exit 1
fi

set -a
. "$env_file"
set +a

: "${AWS_REGION:?set AWS_REGION in $env_file}"

set_env_value() {
	key=$1
	value=$2
	tmp=$(mktemp "${env_file}.edit.XXXXXX")
	awk -v key="$key" -v value="$value" '
		BEGIN { found = 0 }
		$0 ~ "^" key "=" {
			print key "=" value
			found = 1
			next
		}
		{ print }
		END {
			if (!found) {
				print key "=" value
			}
		}
	' "$env_file" >"$tmp"
	chmod 600 "$tmp"
	chown root:root "$tmp"
	mv "$tmp" "$env_file"
}

parameter_value() {
	aws ssm get-parameter \
		--region "$AWS_REGION" \
		--name "$1" \
		--with-decryption \
		--query Parameter.Value \
		--output text
}

if [ -n "${TURNSTILE_SITE_KEY_PARAMETER_NAME:-}" ]; then
	set_env_value TURNSTILE_SITE_KEY "$(parameter_value "$TURNSTILE_SITE_KEY_PARAMETER_NAME")"
fi

if [ -n "${TURNSTILE_SECRET_KEY_PARAMETER_NAME:-}" ]; then
	set_env_value TURNSTILE_SECRET_KEY "$(parameter_value "$TURNSTILE_SECRET_KEY_PARAMETER_NAME")"
fi
