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
	*exchange/prepare | *exchange/active) printf '%s\n' '0:0:700' ;;
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
cat >"$test_dir/main.env" <<'EOF'
MYCFC_IMAGE=registry.example/mycfc@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
GIT_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
EOF
touch "$test_dir/activation.env"
cat >"$test_dir/privacy-activation-disable.env" <<'EOF'
PRIVACY_ACTIVATION_DISABLE_DATABASE_URL=postgres://mycfc_privacy_activation_disable:secret@postgres:5432/mycfc?sslmode=disable
PRIVACY_ACTIVATION_DISABLE_EXPECTED_DATABASE=mycfc
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

PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" \
	MYCFC_PRIVACY_ACTIVATION_DISABLE_ENV_FILE="$test_dir/privacy-activation-disable.env" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	sh "$script_dir/privacy-activation.sh" provision-disable >/dev/null
grep -q -- '--profile privacy-activation-disable-bootstrap run --rm privacy-activation-disable-bootstrap' "$DOCKER_CALLS" || { printf '%s\n' 'disable credential provisioning is not an explicit isolated operation' >&2; exit 1; }

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

routine_database_services=$(sed -n '/^  db-bootstrap:/,/^  privacy-activation-disable-bootstrap:/p' "$script_dir/compose.yaml")
if printf '%s' "$routine_database_services" | grep -q 'PRIVACY_ACTIVATION_DISABLE'; then
	printf '%s\n' 'routine database jobs inherited the break-glass credential' >&2
	exit 1
fi
disable_bootstrap_service=$(awk '/^  privacy-activation-disable-bootstrap:/{copy=1} copy{print} copy && /^  [a-z][a-z-]*:/{if (++services > 1) exit}' "$script_dir/compose.yaml")
printf '%s' "$disable_bootstrap_service" | grep -q 'privacy-activation-disable.env' || { printf '%s\n' 'disable bootstrap service is missing its dedicated credential file' >&2; exit 1; }
if printf '%s' "$disable_bootstrap_service" | grep -Eq 'PRIVACY_ACTIVATION_BROKER|privacy-activation/evidence'; then
	printf '%s\n' 'disable bootstrap service inherited activation authority' >&2
	exit 1
fi

mkdir -p "$test_dir/exchange/prepare" "$test_dir/exchange/active" "$test_dir/signers"
printf '%s' '{"contract":"mycfc/privacy-activation-signer-registry/v1"}' >"$test_dir/signers/signer-registry.json"
registry_sha=$(sha256sum "$test_dir/signers/signer-registry.json" | awk '{print $1}')
cat >"$test_dir/exchange.env" <<EOF
PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256=$registry_sha
PRIVACY_ACTIVATION_POLICY_VERSION=club-2026-09-15-v1
EOF
chmod 0600 "$test_dir/exchange.env" "$test_dir/signers/signer-registry.json"
PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_ENV_FILE="$test_dir/activation.env" \
	MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$test_dir/exchange.env" MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$test_dir/exchange" \
	MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$test_dir/signers/signer-registry.json" MYCFC_DEPLOYMENT_DIR="$script_dir" \
	sh "$script_dir/privacy-activation.sh" prepare-exchange >/dev/null
grep -q -- '--profile privacy-activation-prepare run --rm --no-deps privacy-activation-prepare' "$DOCKER_CALLS"

cat >"$test_dir/exchange/active/material.json" <<'EOF'
{"ceremony_id":"11111111-1111-4111-8111-111111111111","source_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","image_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","schema_migration_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
EOF
printf '%s' '{}' >"$test_dir/exchange/active/executor.json"
printf '%s' '{}' >"$test_dir/exchange/active/administrator.json"
printf '%s' 'executor-key' >"$test_dir/signers/executor.der"
printf '%s' 'administrator-key' >"$test_dir/signers/administrator.der"
chmod 0600 "$test_dir/exchange/active/"*.json "$test_dir/signers/"*.der
for exchange_mode in verify-exchange activate-exchange; do
	PATH="$test_dir/bin:$PATH" MYCFC_ENV_FILE="$test_dir/main.env" MYCFC_PRIVACY_ACTIVATION_ENV_FILE="$test_dir/activation.env" \
		MYCFC_PRIVACY_ACTIVATION_EXCHANGE_ENV_FILE="$test_dir/exchange.env" MYCFC_PRIVACY_ACTIVATION_EXCHANGE_STATE_DIR="$test_dir/exchange" \
		MYCFC_PRIVACY_ACTIVATION_SIGNER_REGISTRY_FILE="$test_dir/signers/signer-registry.json" \
		MYCFC_PRIVACY_ACTIVATION_EXECUTOR_PUBLIC_KEY_FILE="$test_dir/signers/executor.der" \
		MYCFC_PRIVACY_ACTIVATION_ADMINISTRATOR_PUBLIC_KEY_FILE="$test_dir/signers/administrator.der" MYCFC_DEPLOYMENT_DIR="$script_dir" \
		sh "$script_dir/privacy-activation.sh" "$exchange_mode" >/dev/null
done
grep -q -- '--profile privacy-activation-approval run --rm --no-deps privacy-activation-approval bundle-verify' "$DOCKER_CALLS"
grep -q -- '--profile privacy-activation-exchange run --rm --no-deps privacy-activation-exchange' "$DOCKER_CALLS"

approval_service=$(awk '/^  privacy-activation-approval:/{copy=1} copy{print} copy && /^  [a-z][a-z-]*:/{if (++services > 1) exit}' "$script_dir/compose.yaml")
printf '%s' "$approval_service" | grep -q 'entrypoint: \["/usr/local/bin/mycfc-privacy-activation-approval"\]'
printf '%s' "$approval_service" | grep -q 'network_mode: none'
if printf '%s' "$approval_service" | grep -Eq 'env_file:|PRIVACY_ACTIVATION_BROKER_DATABASE_URL'; then
	printf '%s\n' 'signing-only verifier inherited database or environment credentials' >&2
	exit 1
fi

printf '%s\n' 'privacy activation deployment tests passed'
