#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/key"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' 0
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
case "$3" in */key|*/pending|*/provisioning) printf '%s\n' '0:0:700' ;; *) printf '%s\n' '0:0:600' ;; esac
EOF
cat >"$test_dir/bin/chown" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$test_dir/bin/install" <<'EOF'
#!/bin/sh
for arg in "$@"; do directory=$arg; done
mkdir -p "$directory"
chmod 0700 "$directory"
EOF
cat >"$test_dir/bin/aws" <<'EOF'
#!/bin/sh
body=
previous=
for arg in "$@"; do
	[ "$previous" != --body ] || body=$arg
	previous=$arg
done
[ -n "$body" ] && [ -f "$body" ] || exit 1
checksum=$(openssl dgst -sha256 -binary "$body" | base64 -w0)
jq -cn --arg checksum "$checksum" --arg kms "$PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN" \
	'{VersionId:"test-version",ChecksumSHA256:$checksum,ServerSideEncryption:"aws:kms",SSEKMSKeyId:$kms}'
printf '%s\n' "$*" >>"$AWS_CALLS"
EOF
chmod 0755 "$test_dir/bin/"*

env_file=$test_dir/mycfc.env
credentials=$test_dir/credentials
key_dir=$test_dir/key
cat >"$env_file" <<'EOF'
AWS_REGION=eu-west-1
PRIVACY_OPERATION_RECEIPT_BUCKET=test-receipts
PRIVACY_OPERATION_RECEIPT_KMS_KEY_ARN=arn:aws:kms:eu-west-1:123456789012:key/test
EOF
: >"$credentials"
chmod 0600 "$env_file" "$credentials"
export AWS_CALLS="$test_dir/aws.calls"

run_key() {
	PATH="$test_dir/bin:$PATH" \
		MYCFC_ENV_FILE="$env_file" \
		MYCFC_PRIVACY_OPERATION_RECEIPT_KEY_DIR="$key_dir" \
		MYCFC_RELEASE_AWS_CREDENTIALS_FILE="$credentials" \
		sh "$script_dir/privacy-operation-receipt-key.sh" "$@"
}

provision=$(run_key provision 301-1)
printf '%s' "$provision" | grep -Eq '^event=privacy_operation_receipt_key_provisioned public_key_sha256=[0-9a-f]{64} request_id=301-1$'
test -s "$key_dir/private.pem"
test -s "$key_dir/public.pem"
old_digest=$(openssl pkey -pubin -in "$key_dir/public.pem" -outform DER | sha256sum | awk '{print $1}')
grep -q -- '--key public-keys/301-1.pem' "$AWS_CALLS"
grep -q -- '--if-none-match \*' "$AWS_CALLS"

prepared=$(run_key rotate-prepare 302-1)
new_digest=$(printf '%s' "$prepared" | sed -n 's/^.*public_key_sha256=\([0-9a-f]\{64\}\).*$/\1/p')
test -n "$new_digest"
test "$new_digest" != "$old_digest"
test "$new_digest" = "$(openssl pkey -pubin -in "$key_dir/pending/public.pem" -outform DER | sha256sum | awk '{print $1}')"

if run_key rotate-activate 303-1 "$(printf 'f%.0s' $(seq 1 64))" >/dev/null 2>&1; then
	printf '%s\n' 'rotation accepted the wrong pinned public-key digest' >&2
	exit 1
fi
run_key rotate-activate 303-1 "$new_digest" | grep -q '^event=privacy_operation_receipt_key_rotation_staged '
test "$new_digest" = "$(openssl pkey -pubin -in "$key_dir/public.pem" -outform DER | sha256sum | awk '{print $1}')"
test ! -e "$key_dir/pending"
test "$old_digest" = "$(openssl pkey -pubin -in "$key_dir/previous/public.pem" -outform DER | sha256sum | awk '{print $1}')"
run_key rollback-rotate 303-1 | grep -q '^event=privacy_operation_receipt_key_rotation_rolled_back '
test "$old_digest" = "$(openssl pkey -pubin -in "$key_dir/public.pem" -outform DER | sha256sum | awk '{print $1}')"
run_key rotate-activate 303-2 "$new_digest" | grep -q '^event=privacy_operation_receipt_key_rotation_staged '
run_key finalize-rotate 303-2 | grep -q '^event=privacy_operation_receipt_key_rotation_activated '
test "$new_digest" = "$(openssl pkey -pubin -in "$key_dir/public.pem" -outform DER | sha256sum | awk '{print $1}')"
test ! -e "$key_dir/previous"

run_key stage-revoke 304-1
test -f "$key_dir/revoke-after-304-1"
run_key finalize-revoke 304-1 | grep -q '^event=privacy_operation_receipt_key_revoked request_id=304-1$'
test ! -e "$key_dir/private.pem"
test ! -e "$key_dir/public.pem"

printf '%s\n' 'privacy operation receipt key lifecycle tests passed'
