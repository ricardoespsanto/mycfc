#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
mkdir -p "$test_dir/bin" "$test_dir/evidence"

cat >"$test_dir/bin/id" <<'EOF'
#!/bin/sh
printf '%s\n' "${TEST_EFFECTIVE_UID:-0}"
EOF
cat >"$test_dir/bin/stat" <<'EOF'
#!/bin/sh
case "$*" in
	*evidence) printf '%s\n' '0:0:700' ;;
	*privacy-activation-disable.env) printf '%s\n' "${DISABLE_ENV_STAT:-0:0:600}" ;;
	*activation.env) printf '%s\n' '0:600' ;;
	*) printf '%s\n' '0:0:600' ;;
esac
EOF
cat >"$test_dir/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_CALLS"
case "$*" in
	*privacy-activation-disable*) printf '%s\n' 'privacy_activation_disabled kill_switch=engaged readiness=blocked' ;;
	*) printf '%s\n' 'privacy_activation_evidence_recorded restore=1 infrastructure=1 provider=1 schema=1' ;;
esac
EOF
chmod 0755 "$test_dir/bin/id" "$test_dir/bin/stat" "$test_dir/bin/docker"
printf '%s\n' 'MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_dir/main.env"
touch "$test_dir/activation.env"
cat >"$test_dir/privacy-activation-disable.env" <<'EOF'
PRIVACY_ACTIVATION_DISABLE_DATABASE_URL=postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc?sslmode=disable
PRIVACY_ACTIVATION_DISABLE_ACTOR_REF=7f40fdc4-1653-4aa6-8cd5-e44f25c2fd85
EOF
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

output=$(PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_ENV_FILE="$test_dir/not-present" \
	MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" MYCFC_PRIVACY_ACTIVATION_EVIDENCE_DIR="$test_dir/not-present" \
	MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-activation.sh" disable)
[ "$output" = 'privacy_activation_disabled kill_switch=engaged readiness=blocked' ] || { printf '%s\n' 'activation disable output was not preserved' >&2; exit 1; }
grep -q -- '--profile privacy-activation-disable run --rm --no-deps privacy-activation-disable' "$DOCKER_CALLS" || { printf '%s\n' 'activation disable compose command is not isolated' >&2; exit 1; }

if TEST_EFFECTIVE_UID=1000 PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" \
	MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	sh "$script_dir/privacy-activation.sh" disable >/dev/null 2>&1; then
	printf '%s\n' 'non-root activation disable unexpectedly succeeded' >&2
	exit 1
fi
if PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" \
	MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-activation.sh" disable unexpected >/dev/null 2>&1; then
	printf '%s\n' 'activation disable accepted extra command input' >&2
	exit 1
fi
if DISABLE_ENV_STAT=0:0:640 PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" \
	MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	sh "$script_dir/privacy-activation.sh" disable >/dev/null 2>&1; then
	printf '%s\n' 'group-readable activation disable credential unexpectedly succeeded' >&2
	exit 1
fi
ln -s "$test_dir/privacy-activation-disable.env" "$test_dir/privacy-activation-disable-link.env"
if PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable-link.env" \
	MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-activation.sh" disable >/dev/null 2>&1; then
	printf '%s\n' 'symlinked activation disable credential unexpectedly succeeded' >&2
	exit 1
fi
printf '%s\n' 'PRIVACY_ACTIVATION_BROKER_DATABASE_URL=postgres://forbidden' >>"$test_dir/privacy-activation-disable.env"
if PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" \
	MYCFC_DEPLOYMENT_DIR="$script_dir" sh "$script_dir/privacy-activation.sh" disable >/dev/null 2>&1; then
	printf '%s\n' 'activation disable accepted broker credentials' >&2
	exit 1
fi

disable_service=$(awk '/^  privacy-activation-disable:/{copy=1} copy{print} copy && /^  [a-z][a-z-]*:/{if (++services > 1) exit}' "$script_dir/compose.yaml")
printf '%s' "$disable_service" | grep -q 'privacy-activation-disable.env' || { printf '%s\n' 'disable service is missing its dedicated environment' >&2; exit 1; }
if printf '%s' "$disable_service" | grep -Eq 'privacy-activation\.env|privacy-activation/evidence|PRIVACY_ACTIVATION_BROKER'; then
	printf '%s\n' 'disable service inherited broker or evidence inputs' >&2
	exit 1
fi

printf '%s\n' 'privacy activation deployment tests passed'
