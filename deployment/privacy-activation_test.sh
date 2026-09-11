#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/evidence"

cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
case "$*" in
	*evidence) printf '%s\n' '0:0:700' ;;
	*activation.env) printf '%s\n' '0:600' ;;
	*) printf '%s\n' '0:0:600' ;;
esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_CALLS"
printf '%s\n' 'privacy_activation_evidence_recorded restore=1 infrastructure=1 provider=1 schema=1'
EOF
chmod 0755 "$test_dir/bin/stat" "$test_dir/bin/docker"
printf '%s\n' 'MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_dir/main.env"
touch "$test_dir/activation.env"
for file in restore-attestation.json restore-attestation.key infrastructure.json provider-registry.json schema-inventory.json artifact-public.key; do
	touch "$test_dir/evidence/$file"
done
export DOCKER_CALLS="$test_dir/docker.calls"

output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_ENV_FILE="$test_dir/activation.env" \
	MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR="$test_dir/evidence" MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-activation.sh")
printf '%s' "$output" | grep -q '^privacy_activation_evidence_recorded ' || { printf '%s\n' 'activation evidence output was not preserved' >&2; exit 1; }
grep -q -- '--profile privacy-activation run --rm --no-deps privacy-activation record-evidence' "$DOCKER_CALLS" || { printf '%s\n' 'activation compose command is not exact' >&2; exit 1; }
if grep -q 'PRIVACY_ACTIVATION_BROKER_DATABASE_URL' "$DOCKER_CALLS"; then
	printf '%s\n' 'activation runner exposed the database credential' >&2
	exit 1
fi

printf '%s\n' 'privacy activation deployment tests passed'
