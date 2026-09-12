mock_provider "aws" {
  mock_data "aws_caller_identity" { defaults = { account_id = "123456789012" } }
  mock_data "aws_region" { defaults = { region = "eu-west-1" } }
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

run "observer_is_inert_by_default" {
  command = plan
  plan_options { target = [aws_iam_role.operations_observer, aws_iam_policy.operations_observer_boundary, aws_iam_role_policy.operations_observer] }
  assert {
    condition     = length(aws_iam_role.operations_observer) == 0 && length(aws_iam_role_policy.operations_observer) == 0
    error_message = "The observer role must not exist until explicitly enabled."
  }
}

run "observer_requires_an_exact_trust_source" {
  command = plan
  variables { operations_observer_enabled = true }
  plan_options { target = [aws_iam_role.operations_observer] }
  expect_failures = [var.operations_observer_enabled]
}

run "observer_rejects_a_generic_iam_role_for_human_access" {
  command = plan
  variables {
    operations_observer_enabled        = true
    operations_observer_principal_arns = ["arn:aws:iam::123456789012:role/long-lived-operator"]
  }
  plan_options { target = [aws_iam_role.operations_observer] }
  expect_failures = [var.operations_observer_principal_arns]
}

run "observer_is_short_lived_and_denies_sensitive_reads_and_mutation" {
  command = plan
  variables {
    operations_observer_enabled                  = true
    operations_observer_principal_arns           = ["arn:aws:iam::123456789012:role/aws-reserved/sso.amazonaws.com/eu-west-1/AWSReservedSSO_MyCFCObserver_aaaaaaaaaaaaaaaa"]
    operations_observer_github_oidc_provider_arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
  }
  plan_options { target = [aws_iam_role.operations_observer, aws_iam_policy.operations_observer_boundary, aws_iam_role_policy.operations_observer] }

  assert {
    condition     = aws_iam_role.operations_observer[0].max_session_duration == 3600
    error_message = "Observer sessions must be limited to one hour."
  }
  assert {
    condition = (
      jsondecode(aws_iam_role.operations_observer[0].assume_role_policy).Statement[0].Action == "sts:AssumeRole" &&
      contains(jsondecode(aws_iam_role.operations_observer[0].assume_role_policy).Statement[0].Principal.AWS, var.operations_observer_principal_arns[0]) &&
      jsondecode(aws_iam_role.operations_observer[0].assume_role_policy).Statement[1].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:environment:production"
    )
    error_message = "Observer trust must bind the exact Identity Center role and protected GitHub environment subject."
  }
  assert {
    condition     = length(aws_iam_policy.operations_observer_boundary) == 1
    error_message = "The observer must have a separately managed permissions boundary."
  }
  assert {
    condition = alltrue([
      for action in ["s3:GetObject*", "s3:ListBucket*", "secretsmanager:GetSecretValue", "ssm:GetParameter*", "logs:PutLogEvents", "ecr:PutImage", "sts:AssumeRole"] :
      contains(local.operations_observer_deny_actions, action)
    ])
    error_message = "Observer policy must explicitly deny state, secret, write, and role-chaining capability."
  }
  assert {
    condition = alltrue([
      for action in ["logs:FilterLogEvents", "logs:GetLogEvents", "ecr:DescribeImages", "cloudwatch:DescribeAlarms"] :
      contains(concat(local.operations_observer_log_actions, local.operations_observer_ecr_actions, local.operations_observer_alarm_actions), action)
    ])
    error_message = "Observer policy must contain only the required operational read paths."
  }
}
