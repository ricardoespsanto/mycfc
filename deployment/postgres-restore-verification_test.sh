#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
mkdir -p "$work_dir/bin"
cat >"$work_dir/bin/id" <<'EOF'
#!/bin/sh
printf '0\n'
EOF
cat >"$work_dir/bin/stat" <<'EOF'
#!/bin/sh
printf '0:600\n'
EOF
chmod +x "$work_dir/bin/id" "$work_dir/bin/stat"
cat >"$work_dir/runtime.env" <<'EOF'
BACKUP_S3_BUCKET=test-backups
BACKUP_KMS_KEY_ID=arn:aws:kms:eu-west-1:111122223333:key/test
POSTGRES_DB=mycfc
BACKUP_MANIFEST_AUTH_ENABLED=true
POSTGRES_RESTORE_VERIFICATION_ENABLED=false
EOF
printf '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n' >"$work_dir/manifest.key"
: >"$work_dir/credentials"
chmod 0600 "$work_dir/runtime.env" "$work_dir/manifest.key" "$work_dir/credentials"

if PATH="$work_dir/bin:$PATH" MYCFC_ENV_FILE="$work_dir/runtime.env" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/credentials" MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	sh "$script_dir/postgres-restore-verification.sh" >"$work_dir/out" 2>"$work_dir/err"; then
	printf '%s\n' 'disabled restore verification unexpectedly ran' >&2
	exit 1
fi
grep -q 'postgres_restore_verification_disabled' "$work_dir/err"

sed 's/POSTGRES_RESTORE_VERIFICATION_ENABLED=false/POSTGRES_RESTORE_VERIFICATION_ENABLED=true/' "$work_dir/runtime.env" >"$work_dir/runtime.next"
mv "$work_dir/runtime.next" "$work_dir/runtime.env"
chmod 0600 "$work_dir/runtime.env"
sed 's/BACKUP_MANIFEST_AUTH_ENABLED=true/BACKUP_MANIFEST_AUTH_ENABLED=false/' "$work_dir/runtime.env" >"$work_dir/runtime.unauthenticated"
chmod 0600 "$work_dir/runtime.unauthenticated"
if PATH="$work_dir/bin:$PATH" MYCFC_ENV_FILE="$work_dir/runtime.unauthenticated" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/credentials" MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	sh "$script_dir/postgres-restore-verification.sh" \
	"registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
	>"$work_dir/out" 2>"$work_dir/err"; then
	printf '%s\n' 'unauthenticated restore verification unexpectedly ran' >&2
	exit 1
fi
grep -q 'requires_authenticated_backups' "$work_dir/err"
if PATH="$work_dir/bin:$PATH" MYCFC_ENV_FILE="$work_dir/runtime.env" \
	MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/credentials" MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	sh "$script_dir/postgres-restore-verification.sh" mutable-image >"$work_dir/out" 2>"$work_dir/err"; then
	printf '%s\n' 'mutable restore image unexpectedly passed validation' >&2
	exit 1
fi
grep -q 'immutable image digest' "$work_dir/err"

cat >"$work_dir/bin/flock" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$work_dir/bin/python3" <<'EOF'
#!/bin/sh
exit 0
EOF
cat >"$work_dir/bin/sha256sum" <<'EOF'
#!/bin/sh
printf '%s  %s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa "$1"
EOF
cat >"$work_dir/bin/jq" <<'EOF'
#!/bin/sh
args="$*"
case "$args" in
	*Versions*LastModified*) printf '2026-09-22T00:00:00Z\tdaily/manifest.json\tmanifest-version\n' ;;
	*'.dump_key'*) printf 'daily/database.dump.enc\n' ;;
	*'.dump_version'*) printf 'dump-version\n' ;;
	*'.sha256'*) printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n' ;;
	*'.ciphertext'*) printf 'AA==\n' ;;
	*'.Plaintext'*) printf 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n' ;;
	*) exit 0 ;;
esac
EOF
cat >"$work_dir/bin/aws" <<'EOF'
#!/bin/sh
case "$*" in
	*s3api\ list-object-versions*) printf '{}\n' ;;
	*s3api\ get-object*)
		for last do :; done
		case "$last" in
			*manifest.json) printf '{}\n' >"$last" ;;
			*) printf 'encrypted-dump\n' >"$last" ;;
		esac
		printf '{}\n'
		;;
	*kms\ decrypt*) printf '{}\n' ;;
	*) exit 1 ;;
esac
EOF
cat >"$work_dir/bin/openssl" <<'EOF'
#!/bin/sh
case "$1" in
	rand) printf '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n' ;;
	enc)
		output=
		while [ "$#" -gt 0 ]; do
			[ "$1" != -out ] || { shift; output=$1; }
			shift
		done
		printf 'restored-dump\n' >"$output"
		;;
	*) exit 1 ;;
esac
EOF
cat >"$work_dir/bin/docker" <<'EOF'
#!/bin/sh
printf 'docker %s\n' "$*" >>"$MYCFC_TEST_DOCKER_LOG"
case "$*" in
	run\ -d*) printf 'restore-container-id\n' ;;
	*) ;;
esac
EOF
chmod +x "$work_dir/bin/flock" "$work_dir/bin/python3" "$work_dir/bin/sha256sum" "$work_dir/bin/jq" \
	"$work_dir/bin/aws" "$work_dir/bin/openssl" "$work_dir/bin/docker"
: >"$work_dir/docker.log"
if ! PATH="$work_dir/bin:$PATH" MYCFC_ENV_FILE="$work_dir/runtime.env" MYCFC_RUNTIME_DIR="$work_dir" \
	MYCFC_TEST_DOCKER_LOG="$work_dir/docker.log" MYCFC_BACKUP_CREDENTIALS_FILE="$work_dir/credentials" \
	MYCFC_BACKUP_MANIFEST_AUTH_KEY_FILE="$work_dir/manifest.key" \
	sh "$script_dir/postgres-restore-verification.sh" \
	"registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" \
	>"$work_dir/out" 2>"$work_dir/err"; then
	cat "$work_dir/err" >&2
	exit 1
fi
grep -q '^postgres_restore_verification_succeeded$' "$work_dir/out"
grep -q 'pg_restore --exit-on-error --no-owner --no-acl' "$work_dir/docker.log"
grep -q ' migrate$' "$work_dir/docker.log"
grep -q "privacy_automation_retirement" "$work_dir/docker.log"
printf '%s\n' 'postgres restore verification tests passed'
