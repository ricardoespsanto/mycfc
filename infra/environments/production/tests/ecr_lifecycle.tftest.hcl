mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "eu-west-1"
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

run "privacy_operation_requests_expire_without_changing_existing_rules" {
  command = plan

  plan_options {
    target = [aws_ecr_lifecycle_policy.app]
  }

  assert {
    condition     = length(jsondecode(aws_ecr_lifecycle_policy.app.policy).rules) == 4
    error_message = "The ECR lifecycle must contain exactly the three existing rules plus the privacy-operation request rule."
  }

  assert {
    condition = (
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].rulePriority == 1 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].description == "Keep 30 release images" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].selection.tagStatus == "tagged" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].selection.tagPrefixList == ["release-"] &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].selection.countType == "imageCountMoreThan" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].selection.countNumber == 30 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[0].action.type == "expire"
    )
    error_message = "The existing release-image retention rule must remain exact."
  }

  assert {
    condition = (
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].rulePriority == 2 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].description == "Expire one-time purge images after seven days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].selection.tagStatus == "tagged" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].selection.tagPrefixList == ["purge-"] &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].selection.countType == "sinceImagePushed" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].selection.countUnit == "days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].selection.countNumber == 7 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[1].action.type == "expire"
    )
    error_message = "The existing purge-image expiry rule must remain exact."
  }

  assert {
    condition = (
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].rulePriority == 3 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].description == "Expire privacy operation requests after seven days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].selection.tagStatus == "tagged" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].selection.tagPrefixList == ["privacy-op-"] &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].selection.countType == "sinceImagePushed" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].selection.countUnit == "days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].selection.countNumber == 7 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[2].action.type == "expire"
    )
    error_message = "Privacy-operation request images must expire after exactly seven days."
  }

  assert {
    condition = (
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].rulePriority == 4 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].description == "Expire untagged images after seven days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].selection.tagStatus == "untagged" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].selection.countType == "sinceImagePushed" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].selection.countUnit == "days" &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].selection.countNumber == 7 &&
      jsondecode(aws_ecr_lifecycle_policy.app.policy).rules[3].action.type == "expire"
    )
    error_message = "Operation requests must expire after seven days without widening or changing the existing release, purge, or untagged rules."
  }
}
