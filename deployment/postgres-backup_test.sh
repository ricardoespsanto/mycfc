#!/bin/sh
set -eu

deployment_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin"

cat >"$work_dir/mycfc.env" <<'EOF'
AWS_REGION=eu-west-1
AWS_ACCESS_KEY_ID=application-key
AWS_SECRET_ACCESS_KEY=application-secret
BACKUP_S3_BUCKET=test-backups
BACKUP_KMS_KEY_ID=arn:aws:kms:eu-west-1:123456789012:key/backup
BACKUP_MANIFEST_AUTH_ENABLED=true
POSTGRES_USER=mycfc
POSTGRES_DB=mycfc
EOF
chmod 0600 "$work_dir/mycfc.env"
: >"$work_dir/credentials"
chmod 0600 "$work_dir/credentials"
auth_key=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
printf '%s\n' "$auth_key" >"$work_dir/manifest.key"
chmod 0600 "$work_dir/manifest.key"

cat >"$work_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '%s\n' '0:600'
EOF
cat >"$work_dir/bin/date" <<'EOF'
#!/bin/sh
case "$*" in
	*%Y-%m-%dT%H-%M-%SZ*) printf '%s\n' '2026-09-02T02-15-00Z' ;;
	*%Y-%m-%dT%H:%M:%SZ*) printf '%s\n' '2026-09-02T02:15:00Z' ;;
	*%d*) printf '%s\n' '02' ;;
	*) exit 1 ;;
esac
EOF
cat >"$work_dir/bin/docker" <<'EOF'
#!/bin/sh
case "$*" in
	*pg_dump*) printf '%s' 'synthetic-database-dump' ;;
	*) printf '%s\n' 'unexpected docker invocation' >&2; exit 1 ;;
esac
EOF
cat >"$work_dir/bin/openssl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_OPENSSL_LOG"
exec "$REAL_OPENSSL" "$@"
EOF
cat >"$work_dir/bin/aws" <<'EOF'
#!/bin/sh
if [ -n "${AWS_ACCESS_KEY_ID:-}" ] || [ -n "${AWS_SECRET_ACCESS_KEY:-}" ] || [ -n "${AWS_SESSION_TOKEN:-}" ]; then
	printf '%s\n' 'application AWS credentials reached backup command' >&2
	exit 1
fi
[ "$AWS_SHARED_CREDENTIALS_FILE" = "$TEST_CREDENTIALS" ] && [ "$AWS_PROFILE" = mycfc-backup ] || exit 1
case "$*" in
	*kms\ generate-data-key*)
		printf '{"Plaintext":"%s","CiphertextBlob":"%s"}\n' \
			"$(printf '00000000000000000000000000000000' | base64 | tr -d '\n')" \
			"$(printf 'encrypted-data-key' | base64 | tr -d '\n')"
		;;
	*s3api\ put-object*)
		body=
		key=
		previous=
		for argument in "$@"; do
			case "$previous" in
				--body) body=$argument ;;
				--key) key=$argument ;;
			esac
			previous=$argument
		done
		printf '%s\n' "$*" >>"$TEST_AWS_LOG"
		case "$key" in
			*.dump.enc) printf '%s\n' '{"VersionId":"dump-version"}' ;;
			*.json) cp "$body" "$TEST_MANIFEST"; printf '%s\n' '{"VersionId":"manifest-version"}' ;;
			*) exit 1 ;;
		esac
		;;
	*) printf 'unexpected aws invocation: %s\n' "$*" >&2; exit 1 ;;
esac
EOF
chmod +x "$work_dir/bin"/*
: >"$work_dir/aws.log"
: >"$work_dir/openssl.log"
REAL_OPENSSL=$(command -v openssl)
export REAL_OPENSSL

env PATH="$work_dir/bin:$PATH" \
	MYCFC_ENV_FILE="$work_dir/mycfc.env" \
	MYCFC_COMPOSE_FILE="$work_dir/compose.yaml" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/credentials" \
	MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	TEST_AWS_LOG="$work_dir/aws.log" \
	TEST_OPENSSL_LOG="$work_dir/openssl.log" \
	TEST_CREDENTIALS="$work_dir/credentials" \
	TEST_MANIFEST="$work_dir/uploaded-manifest.json" \
	sh -x "$deployment_dir/postgres-backup.sh" 2>"$work_dir/trace.log"

jq -e '
	.contract == "mycfc/postgres-backup/v3"
	and .cipher == "AES-256-CBC"
	and .kdf == "PBKDF2-HMAC-SHA256"
	and .kdf_iterations == 600000
	and .created_at == "2026-09-02T02:15:00Z"
	and .dump_key == "daily/2026-09-02T02-15-00Z.dump.enc"
	and .dump_version == "dump-version"
	and (.sha256 | test("^[0-9a-f]{64}$"))
	and (.auth_hmac_sha256 | test("^[0-9a-f]{64}$"))
' "$work_dir/uploaded-manifest.json" >/dev/null
canonical=$(jq -Sc 'del(.auth_hmac_sha256)' "$work_dir/uploaded-manifest.json")
expected_hmac=$(printf '%s' "$canonical" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$auth_key" -binary | od -An -v -tx1 | tr -d ' \n')
test "$expected_hmac" = "$(jq -r .auth_hmac_sha256 "$work_dir/uploaded-manifest.json")"
test "$(grep -c 's3api put-object' "$work_dir/aws.log")" -eq 2
test "$(grep -c -- "--if-none-match \*" "$work_dir/aws.log")" -eq 2
if grep -q 'monthly/' "$work_dir/aws.log"; then
	printf '%s\n' 'Non-monthly backup unexpectedly wrote a monthly recovery point.' >&2
	exit 1
fi
data_key_hex=3030303030303030303030303030303030303030303030303030303030303030
if grep -Fq -- '-K ' "$work_dir/openssl.log" || grep -Fq "$data_key_hex" "$work_dir/openssl.log" "$work_dir/trace.log"; then
	printf '%s\n' 'KMS plaintext data key reached OpenSSL argv or shell tracing' >&2
	exit 1
fi
grep -Eq 'enc -aes-256-cbc .* -pass file:/var/tmp/mycfc-backup\.' "$work_dir/openssl.log"

printf '%s\n' 'postgres backup authentication tests passed'
