#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/.." && pwd)
terraform_file=$repo_dir/infra/environments/production/privacy_activation_exchange.tf

check_signer() {
	workflow=$1
	environment=$2
	role_arn=$3
	role_name=$4

	grep -q "^    environment: $environment$" "$workflow"
	grep -q '^      actions: read$' "$workflow"
	grep -q '^      contents: read$' "$workflow"
	grep -q '^      id-token: write$' "$workflow"
	grep -q "^          role-to-assume: $role_arn$" "$workflow"
	grep -q '^          aws-region: eu-west-1$' "$workflow"
	grep -q 'role-duration-seconds: 900' "$workflow"
	grep -q 'verification.verified == true and .commit.verification.reason == "valid"' "$workflow"
	grep -q 'actions/workflows/ci.yml/runs?branch=main&event=push&status=completed' "$workflow"
	grep -q 'persist-credentials: false' "$workflow"
	grep -q "PRIVACY_ACTIVATION_ROLE: $role_name" "$workflow"
	grep -q 'PRIVACY_ACTIVATION_SIGNER_REGISTRY_JSON' "$workflow"
	grep -q 'test "\$ACTUAL_GITHUB_ACTOR_ID" = "\$EXPECTED_GITHUB_ACTOR_ID"' "$workflow"

	actor_line=$(grep -n 'test "\$ACTUAL_GITHUB_ACTOR_ID" = "\$EXPECTED_GITHUB_ACTOR_ID"' "$workflow" | cut -d: -f1)
	source_line=$(grep -n 'verification.verified == true' "$workflow" | cut -d: -f1)
	oidc_line=$(grep -n 'aws-actions/configure-aws-credentials@' "$workflow" | cut -d: -f1)
	[ "$actor_line" -lt "$source_line" ] && [ "$source_line" -lt "$oidc_line" ] || {
		printf '%s\n' 'human/source checks must complete before OIDC' >&2
		exit 1
	}
	if grep -Eq 'uses:[[:space:]]+[^[:space:]]+@(main|master|v[0-9]+)$|actions/upload-artifact|GITHUB_STEP_SUMMARY.*(ACTOR|REGISTRY)|echo.*(ACTOR|REGISTRY).*GITHUB_STEP_SUMMARY' "$workflow"; then
		printf '%s\n' 'signer workflow has an unpinned action, raw artifact upload, or sensitive summary output' >&2
		exit 1
	fi
}

check_signer "$repo_dir/.github/workflows/privacy-activation-executor-approval.yml" \
	privacy-activation-executor \
	arn:aws:iam::334960985019:role/mycfc-production-privacy-activation-exchange-executor \
	EXECUTOR
check_signer "$repo_dir/.github/workflows/privacy-activation-administrator-approval.yml" \
	privacy-activation-administrator \
	arn:aws:iam::334960985019:role/mycfc-production-privacy-activation-exchange-administrator \
	ADMINISTRATOR

for sid in DenyInsecureTransport DenyIncorrectEncryption DenyIncorrectEncryptionKey DenyOverwriteCapablePut; do
	grep -q "sid.*= \"$sid\"" "$terraform_file"
done
grep -q 'NotResource = aws_iam_user.privacy_activation_courier\[0\].arn' "$repo_dir/infra/environments/production/runtime_config.tf"
if grep -E 'credential-admin-credentials|PRIVACY_ACTIVATION_(EXECUTOR|ADMINISTRATOR)_ROLE_ARN' \
	"$repo_dir/deployment/privacy-activation-courier-credentials.sh" \
	"$repo_dir/.github/workflows/privacy-activation-executor-approval.yml" \
	"$repo_dir/.github/workflows/privacy-activation-administrator-approval.yml" >/dev/null; then
	printf '%s\n' 'obsolete credential staging or configurable signer role remains' >&2
	exit 1
fi

printf '%s\n' 'privacy activation workflow ordering, pinning, identity and exchange policy tests passed'
