#!/bin/sh
set -eu

env_file=${1:-/etc/mycfc/mycfc.env}
recipient=${SES_SMOKE_RECIPIENT:-success@simulator.amazonses.com}

if [ ! -f "$env_file" ]; then
	printf '%s\n' "Missing SES environment file: $env_file" >&2
	exit 1
fi

set -a
. "$env_file"
set +a

: "${SMTP_HOST:?set SMTP_HOST in $env_file}"
: "${SMTP_PORT:?set SMTP_PORT in $env_file}"
: "${SMTP_USERNAME:?set SMTP_USERNAME in $env_file}"
: "${SMTP_PASSWORD:?set SMTP_PASSWORD in $env_file}"
: "${SMTP_FROM_ADDRESS:?set SMTP_FROM_ADDRESS in $env_file}"
: "${SMTP_TLS_MODE:?set SMTP_TLS_MODE in $env_file}"

if [ "$SMTP_TLS_MODE" != "starttls" ]; then
	printf '%s\n' 'The SES smoke test requires SMTP_TLS_MODE=starttls.' >&2
	exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
	printf '%s\n' 'curl is required for the SES smoke test.' >&2
	exit 1
fi

{
	printf 'From: MyCFC <%s>\r\n' "$SMTP_FROM_ADDRESS"
	printf 'To: %s\r\n' "$recipient"
	printf 'Subject: MyCFC SES smoke test\r\n'
	printf 'Date: %s\r\n' "$(LC_ALL=C date -R)"
	printf '\r\n'
	printf 'Transactional email delivery is configured.\r\n'
} | curl --silent --show-error --fail \
	--connect-timeout 10 \
	--max-time 30 \
	--url "smtp://${SMTP_HOST}:${SMTP_PORT}" \
	--ssl-reqd \
	--user "${SMTP_USERNAME}:${SMTP_PASSWORD}" \
	--mail-from "$SMTP_FROM_ADDRESS" \
	--mail-rcpt "$recipient" \
	--upload-file -

printf '%s\n' "SES accepted the smoke-test message for $recipient."
