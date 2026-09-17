mock_provider "aws" {
  mock_data "aws_caller_identity" { defaults = { account_id = "123456789012" } }
  mock_data "aws_region" { defaults = { region = "eu-west-1" } }
  mock_data "aws_iam_policy_document" { defaults = { json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}" } }
  mock_resource "aws_iam_policy" { defaults = { arn = "arn:aws:iam::123456789012:policy/mock-boundary" } }
  mock_resource "aws_kms_key" {
    defaults = {
      arn    = "arn:aws:kms:eu-west-1:123456789012:key/00000000-0000-4000-8000-000000000001"
      key_id = "00000000-0000-4000-8000-000000000001"
    }
  }
  mock_resource "aws_s3_bucket" {
    defaults = {
      arn = "arn:aws:s3:::mock-privacy-activation"
      id  = "mock-privacy-activation"
    }
  }
}
mock_provider "cloudflare" {}
mock_provider "random" {}

variables {
  route53_zone_id         = "ZAAAAAAAAAAAAA"
  github_org              = "ricardoespsanto"
  github_repo             = "mycfc"
  calendar_competition_id = "competition@example.com"
  calendar_training_id    = "training@example.com"
  calendar_social_id      = "social@example.com"
  calendar_cleanups_id    = "cleanups@example.com"
  google_calendar_api_key = "test-key"
  gallery_url             = "https://mycfcoimbra.com/gallery"
  consent_terms_version   = "test"
  consent_terms_sha256    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  consent_image_version   = "test"
  consent_image_sha256    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  consent_minor_version   = "test"
  consent_minor_sha256    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
  image_digest            = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
  consent_terms_url       = "https://mycfcoimbra.com/legal/terms"
  consent_image_url       = "https://mycfcoimbra.com/legal/image"
  consent_minor_url       = "https://mycfcoimbra.com/legal/minor"
  privacy_notice_url      = "https://mycfcoimbra.com/legal/privacy"
  cookie_notice_url       = "https://mycfcoimbra.com/legal/cookies"
  data_rights_contact     = "privacy@example.com"
  image_git_sha           = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
  postgres_password       = "test-bootstrap"
  app_db_username         = "mycfc_app"
  app_db_password         = "test-app"
  migration_db_username   = "mycfc_migrator"
  migration_db_password   = "test-migrator"
  turnstile_site_key      = "test-site-key"
  turnstile_secret_key    = "test-secret-key"
}

run "exchange_is_inert_by_default" {
  command = plan
  plan_options {
    target = [
      aws_s3_bucket.privacy_activation_exchange,
      aws_kms_key.privacy_activation_exchange,
      aws_kms_key.privacy_activation_executor_signing,
      aws_kms_key.privacy_activation_admin_signing,
      aws_iam_user.privacy_activation_courier,
      aws_iam_role.privacy_activation_executor,
      aws_iam_role.privacy_activation_admin,
      aws_iam_role.privacy_activation_coordinator,
      aws_cloudtrail.privacy_activation_exchange,
    ]
  }
  assert {
    condition = (
      length(aws_s3_bucket.privacy_activation_exchange) == 0 &&
      length(aws_kms_key.privacy_activation_exchange) == 0 &&
      length(aws_kms_key.privacy_activation_executor_signing) == 0 &&
      length(aws_kms_key.privacy_activation_admin_signing) == 0 &&
      length(aws_iam_user.privacy_activation_courier) == 0 &&
      length(aws_iam_role.privacy_activation_executor) == 0 &&
      length(aws_iam_role.privacy_activation_admin) == 0 &&
      length(aws_iam_role.privacy_activation_coordinator) == 0
    )
    error_message = "The dual-signer exchange must provision nothing at its default gate."
  }
}

run "exchange_requires_exact_bootstrap_inputs" {
  command = plan
  variables { privacy_activation_exchange_enabled = true }
  plan_options { target = [aws_s3_bucket.privacy_activation_exchange] }
  expect_failures = [var.privacy_activation_exchange_enabled]
}

run "exchange_bucket_is_private_immutable_and_short_lived" {
  command = apply
  variables {
    privacy_activation_exchange_enabled            = true
    privacy_activation_github_oidc_provider_arn    = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    privacy_activation_terraform_state_bucket_name = "mycfc-test-terraform-state"
  }
  plan_options {
    target = [
      aws_s3_bucket.privacy_activation_exchange,
      aws_s3_bucket_public_access_block.privacy_activation_exchange,
      aws_s3_bucket_ownership_controls.privacy_activation_exchange,
      aws_s3_bucket_versioning.privacy_activation_exchange,
      aws_s3_bucket_server_side_encryption_configuration.privacy_activation_exchange,
      aws_s3_bucket_object_lock_configuration.privacy_activation_exchange,
      aws_s3_bucket_lifecycle_configuration.privacy_activation_exchange,
      aws_s3_bucket_policy.privacy_activation_exchange,
      aws_cloudtrail.privacy_activation_exchange,
    ]
  }

  assert {
    condition = (
      aws_s3_bucket.privacy_activation_exchange[0].object_lock_enabled &&
      aws_s3_bucket_public_access_block.privacy_activation_exchange[0].block_public_acls &&
      aws_s3_bucket_public_access_block.privacy_activation_exchange[0].block_public_policy &&
      aws_s3_bucket_public_access_block.privacy_activation_exchange[0].ignore_public_acls &&
      aws_s3_bucket_public_access_block.privacy_activation_exchange[0].restrict_public_buckets &&
      aws_s3_bucket_ownership_controls.privacy_activation_exchange[0].rule[0].object_ownership == "BucketOwnerEnforced" &&
      aws_s3_bucket_versioning.privacy_activation_exchange[0].versioning_configuration[0].status == "Enabled"
    )
    error_message = "The exchange bucket must use Object Lock, blocked public access, owner-enforced ownership, and versioning."
  }

  assert {
    condition = (
      one(aws_s3_bucket_server_side_encryption_configuration.privacy_activation_exchange[0].rule).apply_server_side_encryption_by_default[0].sse_algorithm == "aws:kms" &&
      aws_s3_bucket_object_lock_configuration.privacy_activation_exchange[0].rule[0].default_retention[0].mode == "COMPLIANCE" &&
      aws_s3_bucket_object_lock_configuration.privacy_activation_exchange[0].rule[0].default_retention[0].days == 1 &&
      aws_s3_bucket_lifecycle_configuration.privacy_activation_exchange[0].rule[0].filter[0].prefix == "ceremonies/" &&
      aws_s3_bucket_lifecycle_configuration.privacy_activation_exchange[0].rule[0].expiration[0].days == 2 &&
      aws_s3_bucket_lifecycle_configuration.privacy_activation_exchange[0].rule[0].noncurrent_version_expiration[0].noncurrent_days == 2
    )
    error_message = "Exchange payloads require the exact KMS key, one-day compliance retention, and two-day current/noncurrent expiration."
  }

  assert {
    condition = (
      !aws_cloudtrail.privacy_activation_exchange[0].include_global_service_events &&
      !aws_cloudtrail.privacy_activation_exchange[0].is_multi_region_trail &&
      aws_cloudtrail.privacy_activation_exchange[0].enable_log_file_validation &&
      aws_cloudtrail.privacy_activation_exchange[0].event_selector[0].include_management_events == false &&
      aws_cloudtrail.privacy_activation_exchange[0].event_selector[0].read_write_type == "All" &&
      aws_cloudtrail.privacy_activation_exchange[0].event_selector[0].data_resource[0].type == "AWS::S3::Object" &&
      endswith(aws_cloudtrail.privacy_activation_exchange[0].event_selector[0].data_resource[0].values[0], "/ceremonies/")
    )
    error_message = "CloudTrail must record only read/write data events for the ceremony object prefix with log validation."
  }

}

run "signing_keys_and_oidc_trust_are_separate" {
  command = apply
  variables {
    privacy_activation_exchange_enabled            = true
    privacy_activation_github_oidc_provider_arn    = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    privacy_activation_terraform_state_bucket_name = "mycfc-test-terraform-state"
  }
  plan_options {
    target = [
      aws_kms_key.privacy_activation_executor_signing,
      aws_kms_key.privacy_activation_admin_signing,
      aws_kms_alias.privacy_activation_executor_signing,
      aws_kms_alias.privacy_activation_admin_signing,
      aws_iam_role.privacy_activation_executor,
      aws_iam_role.privacy_activation_admin,
      aws_iam_role.privacy_activation_coordinator,
    ]
  }
  assert {
    condition = (
      aws_kms_key.privacy_activation_executor_signing[0].key_usage == "SIGN_VERIFY" &&
      aws_kms_key.privacy_activation_executor_signing[0].customer_master_key_spec == "ECC_NIST_P256" &&
      aws_kms_key.privacy_activation_admin_signing[0].key_usage == "SIGN_VERIFY" &&
      aws_kms_key.privacy_activation_admin_signing[0].customer_master_key_spec == "ECC_NIST_P256" &&
      aws_kms_alias.privacy_activation_executor_signing[0].name != aws_kms_alias.privacy_activation_admin_signing[0].name
    )
    error_message = "Executor and administrator require distinct non-exportable P-256 KMS signing keys."
  }
  assert {
    condition = (
      jsondecode(aws_iam_role.privacy_activation_executor[0].assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:environment:privacy-activation-executor" &&
      jsondecode(aws_iam_role.privacy_activation_admin[0].assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:environment:privacy-activation-administrator" &&
      jsondecode(aws_iam_role.privacy_activation_coordinator[0].assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:environment:production"
    )
    error_message = "Each role trust must bind one exact, fixed GitHub environment subject."
  }
}

run "each_identity_has_matching_boundary_and_closed_permissions" {
  command = apply
  variables {
    privacy_activation_exchange_enabled            = true
    privacy_activation_github_oidc_provider_arn    = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    privacy_activation_terraform_state_bucket_name = "mycfc-test-terraform-state"
  }
  plan_options {
    target = [
      aws_iam_policy.privacy_activation_courier_boundary,
      aws_iam_user.privacy_activation_courier,
      aws_iam_user_policy.privacy_activation_courier,
      aws_iam_policy.privacy_activation_executor_boundary,
      aws_iam_role.privacy_activation_executor,
      aws_iam_role_policy.privacy_activation_executor,
      aws_iam_policy.privacy_activation_admin_boundary,
      aws_iam_role.privacy_activation_admin,
      aws_iam_role_policy.privacy_activation_admin,
      aws_iam_policy.privacy_activation_coordinator_boundary,
      aws_iam_role.privacy_activation_coordinator,
      aws_iam_role_policy.privacy_activation_coordinator,
    ]
  }

  assert {
    condition = (
      jsondecode(aws_iam_policy.privacy_activation_courier_boundary[0].policy) == jsondecode(aws_iam_user_policy.privacy_activation_courier[0].policy) &&
      jsondecode(aws_iam_policy.privacy_activation_executor_boundary[0].policy) == jsondecode(aws_iam_role_policy.privacy_activation_executor[0].policy) &&
      jsondecode(aws_iam_policy.privacy_activation_admin_boundary[0].policy) == jsondecode(aws_iam_role_policy.privacy_activation_admin[0].policy) &&
      jsondecode(aws_iam_policy.privacy_activation_coordinator_boundary[0].policy) == jsondecode(aws_iam_role_policy.privacy_activation_coordinator[0].policy)
    )
    error_message = "Every exchange identity must have a permissions boundary identical to its attached policy."
  }

  assert {
    condition = alltrue([
      for policy in [local.privacy_activation_courier_policy, local.privacy_activation_executor_policy, local.privacy_activation_admin_policy, local.privacy_activation_coordinator_policy] :
      alltrue([
        for sid in ["DenySecretAndIdentityControlPlanes", "DenyRoleChaining", "DenyObjectDeletionAndBucketListing", "DenyTerraformState"] :
        contains([for statement in policy.Statement : statement.Sid], sid)
      ])
    ])
    error_message = "Every identity must explicitly deny secret/identity control planes, role chaining, listing/deletion, and Terraform state."
  }

  assert {
    condition = (
      contains([for statement in local.privacy_activation_courier_policy.Statement : statement.Sid], "DenySigning") &&
      contains([for statement in local.privacy_activation_executor_policy.Statement : statement.Sid], "DenyAdministratorPathAndKey") &&
      contains([for statement in local.privacy_activation_admin_policy.Statement : statement.Sid], "DenyExecutorPathAndKey") &&
      contains([for statement in local.privacy_activation_coordinator_policy.Statement : statement.Sid], "DenyApprovalWritesAndSigning")
    )
    error_message = "Courier, signers, and coordinator require explicit role-specific cross-boundary denies."
  }

  assert {
    condition = (
      alltrue([for statement in local.privacy_activation_courier_policy.Statement : statement.Sid != "ReadOnlyExactApprovalVersions" || toset(statement.Action) == toset(["s3:GetObjectVersion", "s3:GetObjectAttributes"])]) &&
      alltrue([for statement in local.privacy_activation_executor_policy.Statement : statement.Sid != "WriteOnlyExecutorApproval" || statement.Resource == local.privacy_activation_executor_arn]) &&
      alltrue([for statement in local.privacy_activation_admin_policy.Statement : statement.Sid != "WriteOnlyAdministratorApproval" || statement.Resource == local.privacy_activation_admin_arn])
    )
    error_message = "Courier reads only exact approval versions and each signer writes only its own fixed approval path."
  }
}

run "release_agent_manages_only_courier_access_keys" {
  command = apply
  variables {
    privacy_activation_exchange_enabled            = true
    privacy_activation_github_oidc_provider_arn    = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    privacy_activation_terraform_state_bucket_name = "mycfc-test-terraform-state"
  }
  plan_options {
    target = [
      aws_iam_user.privacy_activation_courier,
      aws_iam_user_policy.privacy_activation_courier_credential_admin,
    ]
  }

  assert {
    condition = alltrue([
      for statement in jsondecode(aws_iam_user_policy.privacy_activation_courier_credential_admin[0].policy).Statement :
      statement.Sid != "ManageOnlyPrivacyActivationCourierAccessKeys" || (
        statement.Effect == "Allow" &&
        toset(statement.Action) == toset(["iam:CreateAccessKey", "iam:DeleteAccessKey", "iam:ListAccessKeys", "iam:UpdateAccessKey"]) &&
        statement.Resource == aws_iam_user.privacy_activation_courier[0].arn
      )
    ])
    error_message = "The release agent may manage access keys only for the exact courier user."
  }

  assert {
    condition = (
      alltrue([for statement in jsondecode(aws_iam_user_policy.privacy_activation_courier_credential_admin[0].policy).Statement : statement.Sid != "DenyAccessKeyManagementForEveryOtherIdentity" || statement.NotResource == aws_iam_user.privacy_activation_courier[0].arn]) &&
      contains([for statement in jsondecode(aws_iam_user_policy.privacy_activation_courier_credential_admin[0].policy).Statement : statement.Sid], "DenyRoleChaining")
    )
    error_message = "The release agent must deny access-key management for every other identity and explicitly deny role chaining."
  }
}
