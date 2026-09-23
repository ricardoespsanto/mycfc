mock_provider "aws" {
  mock_data "aws_caller_identity" { defaults = { account_id = "123456789012" } }
  mock_data "aws_region" { defaults = { region = "eu-west-1" } }
}
mock_provider "cloudflare" {}
mock_provider "random" {}

variables {
  route53_zone_id               = "ZAAAAAAAAAAAAA"
  github_org                    = "ricardoespsanto"
  github_repo                   = "mycfc"
  calendar_competition_id       = "competition@example.com"
  calendar_training_id          = "training@example.com"
  calendar_social_id            = "social@example.com"
  calendar_cleanups_id          = "cleanups@example.com"
  google_calendar_api_key       = "test-key"
  polar_client_id               = ""
  polar_client_secret           = ""
  activity_credential_key_id    = ""
  activity_credential_keys_json = ""
  gallery_url                   = "https://mycfcoimbra.com/gallery"
  consent_terms_version         = "test"
  consent_terms_sha256          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  consent_image_version         = "test"
  consent_image_sha256          = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  consent_minor_version         = "test"
  consent_minor_sha256          = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
  image_digest                  = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
  consent_terms_url             = "https://mycfcoimbra.com/legal/terms"
  consent_image_url             = "https://mycfcoimbra.com/legal/image"
  consent_minor_url             = "https://mycfcoimbra.com/legal/minor"
  privacy_notice_url            = "https://mycfcoimbra.com/legal/privacy"
  cookie_notice_url             = "https://mycfcoimbra.com/legal/cookies"
  data_rights_contact           = "privacy@example.com"
  image_git_sha                 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
  app_db_username               = "mycfc_app"
  app_db_password               = "test-app"
  migration_db_username         = "mycfc_migrator"
  turnstile_site_key            = "test-site-key"
  turnstile_secret_key          = "test-secret-key"
}

run "media_cleanup_is_prefix_limited_and_cannot_write_or_chain_roles" {
  command = plan

  override_resource {
    target          = aws_secretsmanager_secret.app_runtime
    override_during = plan
    values = {
      arn = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:mycfc/production/app-runtime-secrets-v2"
    }
  }

  override_resource {
    target          = aws_secretsmanager_secret.legacy_runtime
    override_during = plan
    values = {
      arn = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:mycfc/production/app-secrets"
    }
  }

  plan_options {
    target = [aws_iam_user.media_cleanup, aws_iam_access_key.media_cleanup, aws_iam_user_policy.media_cleanup, aws_secretsmanager_secret_version.app_runtime, aws_iam_user_policy.host_runtime]
  }

  assert {
    condition = (toset(local.media_cleanup_list_actions) == toset(["s3:ListBucketVersions"]) &&
      toset(local.media_cleanup_prefixes) == toset(["repairs/*", "equipment/*", "profiles/*"])
    )
    error_message = "Media cleanup version inventory must remain limited to the three application media prefixes."
  }

  assert {
    condition     = toset(local.media_cleanup_delete_actions) == toset(["s3:DeleteObjectVersion"])
    error_message = "Media cleanup deletion capability must remain exact and prefix limited."
  }

  assert {
    condition = toset(local.media_cleanup_deny_actions) == toset([
      "s3:DeleteObject", "s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:PutObjectAcl", "s3:PutObjectTagging", "sts:AssumeRole"
    ])
    error_message = "Media cleanup must explicitly deny object writes and role chaining."
  }

  assert {
    condition = (!contains(nonsensitive(keys(local.runtime_secret)), "POSTGRES_PASSWORD") &&
      !contains(nonsensitive(keys(local.runtime_secret)), "MIGRATION_DB_PASSWORD") &&
      !contains(nonsensitive(keys(local.runtime_secret)), "MEDIA_CLEANUP_DB_PASSWORD") &&
      !contains(nonsensitive(keys(local.runtime_secret)), "DATA_RETENTION_DB_PASSWORD")
    )
    error_message = "The web-readable application secret must not contain privileged or maintenance database credentials."
  }

  assert {
    condition = (local.runtime_secret_name == "/mycfc/production/app-runtime-secrets-v2" &&
      local.legacy_runtime_secret_name == "/mycfc/production/app-secrets" &&
      local.runtime_secret_name != local.legacy_runtime_secret_name
    )
    error_message = "The clean web-runtime secret must use a new name distinct from the quarantined legacy secret."
  }

  assert {
    condition = (local.host_runtime_secret_actions == ["secretsmanager:GetSecretValue"] &&
      local.host_runtime_secret_allow_resources == [aws_secretsmanager_secret.legacy_runtime.arn, aws_secretsmanager_secret.app_runtime.arn]
    )
    error_message = "The transitional host runtime identity must read exactly the legacy and clean v2 application secrets."
  }

  assert {
    condition = (
      length(regexall("sid[[:space:]]*=[[:space:]]*\"ReadRuntimeSecret\"[[:space:]]+effect[[:space:]]*=[[:space:]]*\"Allow\"", file("${path.module}/runtime_config.tf"))) == 1 &&
      length(regexall("effect[[:space:]]*=[[:space:]]*\"Deny\"", split("resource \"aws_iam_user_policy\" \"host_runtime\" {", split("data \"aws_iam_policy_document\" \"host_runtime\" {", file("${path.module}/runtime_config.tf"))[1])[0])) == 0
    )
    error_message = "The transitional host runtime policy must allow the secret read and contain no explicit deny before cutover."
  }
}
