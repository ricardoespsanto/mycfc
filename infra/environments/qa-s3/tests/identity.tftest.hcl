provider "aws" {
  region                      = "eu-west-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
}

override_data {
  target = data.aws_caller_identity.current
  values = { account_id = "123456789012" }
}

override_data {
  target = data.aws_partition.current
  values = { partition = "aws" }
}

override_resource {
  target          = aws_iam_policy.runner
  override_during = plan
  values          = { arn = "arn:aws:iam::123456789012:policy/mycfc-qa-s3-runner-boundary" }
}

override_resource {
  target          = aws_iam_policy.janitor
  override_during = plan
  values          = { arn = "arn:aws:iam::123456789012:policy/mycfc-qa-s3-janitor-boundary" }
}

variables {
  aws_region               = "eu-west-1"
  github_oidc_provider_arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
}

run "identities_are_qa_scoped" {
  command = plan

  assert {
    condition = (
      jsondecode(aws_iam_role.runner.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:environment:qa-s3" &&
      jsondecode(aws_iam_role.janitor.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:ricardoespsanto/mycfc:ref:refs/heads/main" &&
      jsondecode(aws_iam_role.runner.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:aud"] == "sts.amazonaws.com" &&
      jsondecode(aws_iam_role.janitor.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:aud"] == "sts.amazonaws.com"
    )
    error_message = "Runner and janitor trust must use their exact GitHub subjects and audience."
  }

  assert {
    condition = (
      aws_iam_role.runner.permissions_boundary == aws_iam_policy.runner.arn &&
      aws_iam_role.janitor.permissions_boundary == aws_iam_policy.janitor.arn &&
      aws_iam_role_policy_attachment.runner.policy_arn == aws_iam_policy.runner.arn &&
      aws_iam_role_policy_attachment.janitor.policy_arn == aws_iam_policy.janitor.arn
    )
    error_message = "Both roles must retain their matching QA permissions boundaries and attached policies."
  }

  assert {
    condition = (
      alltrue([for resource in flatten([for statement in jsondecode(aws_iam_policy.runner.policy).Statement : statement.Resource]) : startswith(resource, "arn:aws:s3:::mycfc-qa-s3-123456789012-eu-west-1-")]) &&
      length([for action in flatten([for statement in jsondecode(aws_iam_policy.runner.policy).Statement : statement.Action]) : action if action == "s3:ListAllMyBuckets" || startswith(action, "iam:") || startswith(action, "sts:") || startswith(action, "kms:")]) == 0
    )
    error_message = "The runner must have only QA-prefixed S3 resources and no account-wide listing or role chaining."
  }

  assert {
    condition = (
      length([for statement in jsondecode(aws_iam_policy.runner.policy).Statement : statement if statement.Sid == "CreateQaBucketsInChosenRegion" && statement.Action == "s3:CreateBucket" && statement.Condition.StringEquals["s3:LocationConstraint"] == "eu-west-1"]) == 1 &&
      length([for statement in jsondecode(aws_iam_policy.runner.policy).Statement : statement if statement.Sid != "CreateQaBucketsInChosenRegion" && contains(flatten([statement.Action]), "s3:CreateBucket")]) == 0
    )
    error_message = "Bucket creation must be limited to the selected AWS region."
  }

  assert {
    condition = (
      length([for statement in jsondecode(aws_iam_policy.janitor.policy).Statement : statement if statement.Resource == "*"]) == 1 &&
      length([for statement in jsondecode(aws_iam_policy.janitor.policy).Statement : statement if statement.Resource == "*" && statement.Action != "s3:ListAllMyBuckets"]) == 0 &&
      alltrue([for resource in flatten([for statement in jsondecode(aws_iam_policy.janitor.policy).Statement : statement.Resource]) : resource == "*" || startswith(resource, "arn:aws:s3:::mycfc-qa-s3-123456789012-eu-west-1-")]) &&
      length([for action in flatten([for statement in jsondecode(aws_iam_policy.janitor.policy).Statement : statement.Action]) : action if action == "s3:CreateBucket" || startswith(action, "iam:") || startswith(action, "sts:") || startswith(action, "kms:")]) == 0
    )
    error_message = "The janitor's only account-wide grant must be bucket-name discovery; it cannot create buckets."
  }
}

run "foreign_oidc_provider_is_rejected" {
  command = plan
  variables {
    github_oidc_provider_arn = "arn:aws:iam::999999999999:oidc-provider/token.actions.githubusercontent.com"
  }
  expect_failures = [aws_iam_role.runner]
}
