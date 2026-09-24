data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

locals {
  qa_bucket_prefix = "mycfc-qa-s3-${data.aws_caller_identity.current.account_id}-${var.aws_region}-"
  qa_bucket_arn    = "arn:${data.aws_partition.current.partition}:s3:::${local.qa_bucket_prefix}*"
  oidc_issuer      = "token.actions.githubusercontent.com"
}

data "aws_iam_policy_document" "runner_trust" {
  statement {
    sid     = "ProtectedQaEnvironmentOnly"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [var.github_oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:sub"
      values   = ["repo:ricardoespsanto/mycfc:environment:qa-s3"]
    }
  }
}

data "aws_iam_policy_document" "janitor_trust" {
  statement {
    sid     = "MainBranchOnly"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [var.github_oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:sub"
      values   = ["repo:ricardoespsanto/mycfc:ref:refs/heads/main"]
    }
  }
}

data "aws_iam_policy_document" "runner" {
  statement {
    sid       = "CreateQaBucketsInChosenRegion"
    actions   = ["s3:CreateBucket"]
    resources = [local.qa_bucket_arn]
    condition {
      test     = "StringEquals"
      variable = "s3:LocationConstraint"
      values   = [var.aws_region]
    }
  }

  statement {
    sid = "ConfigureQaBuckets"
    actions = [
      "s3:DeleteBucket", "s3:GetBucketLocation",
      "s3:GetBucketVersioning", "s3:PutBucketVersioning",
      "s3:GetBucketPublicAccessBlock", "s3:PutBucketPublicAccessBlock",
      "s3:GetBucketOwnershipControls", "s3:PutBucketOwnershipControls",
      "s3:GetEncryptionConfiguration", "s3:PutEncryptionConfiguration",
      "s3:GetBucketTagging", "s3:PutBucketTagging",
      "s3:GetLifecycleConfiguration", "s3:PutLifecycleConfiguration",
      "s3:ListBucket", "s3:ListBucketVersions",
      "s3:ListBucketMultipartUploads",
    ]
    resources = [local.qa_bucket_arn]
  }

  statement {
    sid = "UseOnlyOwnQaObjects"
    actions = [
      "s3:GetObject", "s3:GetObjectVersion", "s3:PutObject",
      "s3:DeleteObject", "s3:DeleteObjectVersion",
      "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts",
    ]
    resources = ["${local.qa_bucket_arn}/*"]
  }

}

data "aws_iam_policy_document" "janitor" {
  statement {
    sid       = "DiscoverBucketNamesOnly"
    actions   = ["s3:ListAllMyBuckets"]
    resources = ["*"]
  }

  statement {
    sid = "InspectAndRemoveOnlyQaBuckets"
    actions = [
      "s3:GetBucketLocation", "s3:GetBucketTagging", "s3:GetBucketVersioning",
      "s3:ListBucket", "s3:ListBucketVersions", "s3:ListBucketMultipartUploads",
      "s3:DeleteBucket",
    ]
    resources = [local.qa_bucket_arn]
  }

  statement {
    sid = "RemoveQaObjectVersionsAndMultipartUploads"
    actions = [
      "s3:DeleteObject", "s3:DeleteObjectVersion", "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${local.qa_bucket_arn}/*"]
  }
}

resource "aws_iam_policy" "runner" {
  name   = "mycfc-qa-s3-runner-boundary"
  policy = data.aws_iam_policy_document.runner.json
}

resource "aws_iam_policy" "janitor" {
  name   = "mycfc-qa-s3-janitor-boundary"
  policy = data.aws_iam_policy_document.janitor.json
}

resource "aws_iam_role" "runner" {
  name                 = "github-qa-s3-runner"
  assume_role_policy   = data.aws_iam_policy_document.runner_trust.json
  permissions_boundary = aws_iam_policy.runner.arn

  lifecycle {
    precondition {
      condition     = var.github_oidc_provider_arn == "arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/${local.oidc_issuer}"
      error_message = "GitHub OIDC provider must belong to the current AWS account."
    }
  }
}

resource "aws_iam_role" "janitor" {
  name                 = "github-qa-s3-janitor"
  assume_role_policy   = data.aws_iam_policy_document.janitor_trust.json
  permissions_boundary = aws_iam_policy.janitor.arn
}

resource "aws_iam_role_policy_attachment" "runner" {
  role       = aws_iam_role.runner.name
  policy_arn = aws_iam_policy.runner.arn
}

resource "aws_iam_role_policy_attachment" "janitor" {
  role       = aws_iam_role.janitor.name
  policy_arn = aws_iam_policy.janitor.arn
}

output "qa_bucket_prefix" { value = local.qa_bucket_prefix }
output "qa_runner_role_arn" { value = aws_iam_role.runner.arn }
output "qa_janitor_role_arn" { value = aws_iam_role.janitor.arn }
