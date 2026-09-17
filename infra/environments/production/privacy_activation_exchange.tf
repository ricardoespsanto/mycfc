locals {
  privacy_activation_exchange_name        = "${local.name}-privacy-activation-exchange"
  privacy_activation_exchange_bucket_name = "${var.project_name}-${var.environment}-${data.aws_caller_identity.current.account_id}-activation-xchg"
  privacy_activation_audit_bucket_name    = "${var.project_name}-${var.environment}-${data.aws_caller_identity.current.account_id}-activation-audit"
  privacy_activation_executor_environment = "privacy-activation-executor"
  privacy_activation_admin_environment    = "privacy-activation-administrator"
  privacy_activation_state_bucket_name    = coalesce(var.privacy_activation_terraform_state_bucket_name, "disabled-terraform-state")
  privacy_activation_material_arn         = "${try(aws_s3_bucket.privacy_activation_exchange[0].arn, "arn:aws:s3:::disabled")}/ceremonies/*/material.json"
  privacy_activation_executor_arn         = "${try(aws_s3_bucket.privacy_activation_exchange[0].arn, "arn:aws:s3:::disabled")}/ceremonies/*/approvals/executor.json"
  privacy_activation_admin_arn            = "${try(aws_s3_bucket.privacy_activation_exchange[0].arn, "arn:aws:s3:::disabled")}/ceremonies/*/approvals/administrator.json"
  privacy_activation_receipt_arn          = "${try(aws_s3_bucket.privacy_activation_exchange[0].arn, "arn:aws:s3:::disabled")}/ceremonies/*/receipt.json"
  privacy_activation_exchange_key_arn     = try(aws_kms_key.privacy_activation_exchange[0].arn, "arn:aws:kms:disabled:000000000000:key/disabled")
  privacy_activation_executor_key_arn     = try(aws_kms_key.privacy_activation_executor_signing[0].arn, "arn:aws:kms:disabled:000000000000:key/disabled-executor")
  privacy_activation_admin_key_arn        = try(aws_kms_key.privacy_activation_admin_signing[0].arn, "arn:aws:kms:disabled:000000000000:key/disabled-administrator")

  privacy_activation_common_denies = [
    {
      Sid      = "DenySecretAndIdentityControlPlanes"
      Effect   = "Deny"
      Action   = ["iam:*", "secretsmanager:*", "ssm:*"]
      Resource = "*"
    },
    {
      Sid      = "DenyRoleChaining"
      Effect   = "Deny"
      Action   = ["sts:AssumeRole"]
      Resource = "*"
    },
    {
      Sid      = "DenyObjectDeletionAndBucketListing"
      Effect   = "Deny"
      Action   = ["s3:DeleteObject", "s3:DeleteObjectVersion", "s3:ListBucket", "s3:ListBucketVersions"]
      Resource = "*"
    },
    {
      Sid    = "DenyTerraformState"
      Effect = "Deny"
      Action = ["s3:*"]
      Resource = [
        "arn:aws:s3:::${local.privacy_activation_state_bucket_name}",
        "arn:aws:s3:::${local.privacy_activation_state_bucket_name}/*",
      ]
    },
  ]
}

resource "aws_kms_key" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  description             = "Encrypt short-lived MyCFC privacy activation ceremony objects"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  key_usage               = "ENCRYPT_DECRYPT"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DelegateOnlyThroughAccountIAM"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
      Action    = "kms:*"
      Resource  = "*"
    }]
  })
  tags = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_kms_alias" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name          = "alias/${local.privacy_activation_exchange_name}"
  target_key_id = aws_kms_key.privacy_activation_exchange[0].key_id
}

resource "aws_kms_key" "privacy_activation_executor_signing" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  description              = "Non-exportable executor approval key for MyCFC privacy activation"
  deletion_window_in_days  = 30
  key_usage                = "SIGN_VERIFY"
  customer_master_key_spec = "ECC_NIST_P256"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DelegateOnlyThroughAccountIAM"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
      Action    = "kms:*"
      Resource  = "*"
    }]
  })
  tags = merge(local.tags, { PrivacyActivationRole = "EXECUTOR" })

  lifecycle { prevent_destroy = true }
}

resource "aws_kms_alias" "privacy_activation_executor_signing" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name          = "alias/${local.name}-privacy-activation-executor"
  target_key_id = aws_kms_key.privacy_activation_executor_signing[0].key_id
}

resource "aws_kms_key" "privacy_activation_admin_signing" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  description              = "Non-exportable administrator approval key for MyCFC privacy activation"
  deletion_window_in_days  = 30
  key_usage                = "SIGN_VERIFY"
  customer_master_key_spec = "ECC_NIST_P256"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DelegateOnlyThroughAccountIAM"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
      Action    = "kms:*"
      Resource  = "*"
    }]
  })
  tags = merge(local.tags, { PrivacyActivationRole = "ADMINISTRATOR" })

  lifecycle { prevent_destroy = true }
}

resource "aws_kms_alias" "privacy_activation_admin_signing" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name          = "alias/${local.name}-privacy-activation-administrator"
  target_key_id = aws_kms_key.privacy_activation_admin_signing[0].key_id
}

resource "aws_s3_bucket" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket              = local.privacy_activation_exchange_bucket_name
  object_lock_enabled = true
  tags                = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket_public_access_block" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket                  = aws_s3_bucket.privacy_activation_exchange[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  rule { object_ownership = "BucketOwnerEnforced" }
}

resource "aws_s3_bucket_versioning" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  rule {
    bucket_key_enabled = true
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.privacy_activation_exchange[0].arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_object_lock_configuration" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 1
    }
  }

  depends_on = [aws_s3_bucket_versioning.privacy_activation_exchange]
}

resource "aws_s3_bucket_lifecycle_configuration" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  rule {
    id     = "expire-short-lived-ceremonies"
    status = "Enabled"
    filter { prefix = "ceremonies/" }
    expiration { days = 2 }
    noncurrent_version_expiration { noncurrent_days = 2 }
    abort_incomplete_multipart_upload { days_after_initiation = 1 }
  }

  depends_on = [aws_s3_bucket_versioning.privacy_activation_exchange]
}

data "aws_iam_policy_document" "privacy_activation_exchange_bucket" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.privacy_activation_exchange[0].arn,
      "${aws_s3_bucket.privacy_activation_exchange[0].arn}/*",
    ]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }

  statement {
    sid       = "DenyIncorrectEncryption"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_activation_exchange[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption"
      values   = ["aws:kms"]
    }
  }

  statement {
    sid       = "DenyIncorrectEncryptionKey"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_activation_exchange[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption-aws-kms-key-id"
      values   = [aws_kms_key.privacy_activation_exchange[0].arn]
    }
  }

  statement {
    sid       = "DenyPutWithoutSHA256Checksum"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_activation_exchange[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Null"
      variable = "s3:x-amz-checksum-sha256"
      values   = ["true"]
    }
  }

  statement {
    sid       = "DenyOverwriteCapablePut"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_activation_exchange[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Null"
      variable = "s3:if-none-match"
      values   = ["true"]
    }
  }
}

resource "aws_s3_bucket_policy" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_exchange[0].id
  policy = data.aws_iam_policy_document.privacy_activation_exchange_bucket[0].json
}

# CloudTrail requires an S3 destination. This audit bucket is separate from
# activation payloads so its 90-day evidence lifetime cannot extend their
# deliberately short two-day exchange lifetime.
resource "aws_s3_bucket" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = local.privacy_activation_audit_bucket_name
  tags   = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket_public_access_block" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket                  = aws_s3_bucket.privacy_activation_audit[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_audit[0].id
  rule { object_ownership = "BucketOwnerEnforced" }
}

resource "aws_s3_bucket_versioning" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_audit[0].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_audit[0].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_audit[0].id
  rule {
    id     = "bounded-cloudtrail-evidence"
    status = "Enabled"
    filter { prefix = "AWSLogs/" }
    expiration { days = 90 }
    noncurrent_version_expiration { noncurrent_days = 30 }
    abort_incomplete_multipart_upload { days_after_initiation = 7 }
  }

  depends_on = [aws_s3_bucket_versioning.privacy_activation_audit]
}

data "aws_iam_policy_document" "privacy_activation_audit_bucket" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.privacy_activation_audit[0].arn,
      "${aws_s3_bucket.privacy_activation_audit[0].arn}/*",
    ]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }

  statement {
    sid       = "CloudTrailBucketAcl"
    effect    = "Allow"
    actions   = ["s3:GetBucketAcl"]
    resources = [aws_s3_bucket.privacy_activation_audit[0].arn]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceArn"
      values   = ["arn:aws:cloudtrail:${var.aws_region}:${data.aws_caller_identity.current.account_id}:trail/${local.privacy_activation_exchange_name}"]
    }
  }

  statement {
    sid       = "CloudTrailWrite"
    effect    = "Allow"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_activation_audit[0].arn}/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceArn"
      values   = ["arn:aws:cloudtrail:${var.aws_region}:${data.aws_caller_identity.current.account_id}:trail/${local.privacy_activation_exchange_name}"]
    }
  }
}

resource "aws_s3_bucket_policy" "privacy_activation_audit" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_activation_audit[0].id
  policy = data.aws_iam_policy_document.privacy_activation_audit_bucket[0].json
}

resource "aws_cloudtrail" "privacy_activation_exchange" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name                          = local.privacy_activation_exchange_name
  s3_bucket_name                = aws_s3_bucket.privacy_activation_audit[0].id
  include_global_service_events = false
  is_multi_region_trail         = false
  enable_log_file_validation    = true
  enable_logging                = true

  event_selector {
    include_management_events = false
    read_write_type           = "All"
    data_resource {
      type   = "AWS::S3::Object"
      values = ["${aws_s3_bucket.privacy_activation_exchange[0].arn}/ceremonies/"]
    }
  }

  depends_on = [aws_s3_bucket_policy.privacy_activation_audit]
}

locals {
  privacy_activation_courier_policy = {
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "WriteOnlyMaterialAndReceipt"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = [local.privacy_activation_material_arn, local.privacy_activation_receipt_arn]
      },
      {
        Sid      = "ReadOnlyExactApprovalVersions"
        Effect   = "Allow"
        Action   = ["s3:GetObjectVersion", "s3:GetObjectAttributes"]
        Resource = [local.privacy_activation_executor_arn, local.privacy_activation_admin_arn]
      },
      {
        Sid      = "UseOnlyExchangeEncryptionKey"
        Effect   = "Allow"
        Action   = ["kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey"]
        Resource = local.privacy_activation_exchange_key_arn
      },
      {
        Sid      = "DenySigning"
        Effect   = "Deny"
        Action   = ["kms:Sign"]
        Resource = "*"
      },
    ], local.privacy_activation_common_denies)
  }

  privacy_activation_executor_policy = {
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "ReadOnlyExactMaterialVersions"
        Effect   = "Allow"
        Action   = ["s3:GetObjectVersion", "s3:GetObjectAttributes"]
        Resource = local.privacy_activation_material_arn
      },
      {
        Sid      = "WriteOnlyExecutorApproval"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = local.privacy_activation_executor_arn
      },
      {
        Sid      = "UseOnlyExchangeEncryptionKey"
        Effect   = "Allow"
        Action   = ["kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey"]
        Resource = local.privacy_activation_exchange_key_arn
      },
      {
        Sid      = "SignOnlyWithExecutorKey"
        Effect   = "Allow"
        Action   = ["kms:GetPublicKey", "kms:Sign"]
        Resource = local.privacy_activation_executor_key_arn
      },
      {
        Sid      = "DenyAdministratorPathAndKey"
        Effect   = "Deny"
        Action   = ["s3:GetObject*", "s3:PutObject", "kms:GetPublicKey", "kms:Sign"]
        Resource = [local.privacy_activation_admin_arn, local.privacy_activation_admin_key_arn]
      },
    ], local.privacy_activation_common_denies)
  }

  privacy_activation_admin_policy = {
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "ReadOnlyExactMaterialVersions"
        Effect   = "Allow"
        Action   = ["s3:GetObjectVersion", "s3:GetObjectAttributes"]
        Resource = local.privacy_activation_material_arn
      },
      {
        Sid      = "WriteOnlyAdministratorApproval"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = local.privacy_activation_admin_arn
      },
      {
        Sid      = "UseOnlyExchangeEncryptionKey"
        Effect   = "Allow"
        Action   = ["kms:Decrypt", "kms:Encrypt", "kms:GenerateDataKey"]
        Resource = local.privacy_activation_exchange_key_arn
      },
      {
        Sid      = "SignOnlyWithAdministratorKey"
        Effect   = "Allow"
        Action   = ["kms:GetPublicKey", "kms:Sign"]
        Resource = local.privacy_activation_admin_key_arn
      },
      {
        Sid      = "DenyExecutorPathAndKey"
        Effect   = "Deny"
        Action   = ["s3:GetObject*", "s3:PutObject", "kms:GetPublicKey", "kms:Sign"]
        Resource = [local.privacy_activation_executor_arn, local.privacy_activation_executor_key_arn]
      },
    ], local.privacy_activation_common_denies)
  }

  privacy_activation_coordinator_policy = {
    Version = "2012-10-17"
    Statement = concat([
      {
        Sid      = "ReadOnlyMaterialAndReceiptVersions"
        Effect   = "Allow"
        Action   = ["s3:GetObjectVersion", "s3:GetObjectAttributes"]
        Resource = [local.privacy_activation_material_arn, local.privacy_activation_receipt_arn]
      },
      {
        Sid      = "DecryptOnlyExchangePayloads"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = local.privacy_activation_exchange_key_arn
      },
      {
        Sid      = "DenyApprovalWritesAndSigning"
        Effect   = "Deny"
        Action   = ["s3:PutObject", "kms:Sign"]
        Resource = "*"
      },
    ], local.privacy_activation_common_denies)
  }
}

resource "aws_iam_policy" "privacy_activation_courier_boundary" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name        = "${local.privacy_activation_exchange_name}-courier-boundary"
  description = "Maximum fixed-key exchange permissions for the host ceremony courier"
  policy      = jsonencode(local.privacy_activation_courier_policy)
  tags        = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_user" "privacy_activation_courier" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name                 = "${local.privacy_activation_exchange_name}-courier"
  permissions_boundary = aws_iam_policy.privacy_activation_courier_boundary[0].arn
  tags                 = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_user_policy" "privacy_activation_courier" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "privacy-activation-courier"
  user   = aws_iam_user.privacy_activation_courier[0].name
  policy = jsonencode(local.privacy_activation_courier_policy)
}

resource "aws_iam_policy" "privacy_activation_executor_boundary" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "${local.privacy_activation_exchange_name}-executor-boundary"
  policy = jsonencode(local.privacy_activation_executor_policy)
  tags   = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role" "privacy_activation_executor" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name                 = "${local.privacy_activation_exchange_name}-executor"
  permissions_boundary = aws_iam_policy.privacy_activation_executor_boundary[0].arn
  max_session_duration = 3600
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "ExactExecutorEnvironment"
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.privacy_activation_github_oidc_provider_arn }
      Condition = { StringEquals = {
        "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        "token.actions.githubusercontent.com:sub" = "repo:${var.github_org}/${var.github_repo}:environment:${local.privacy_activation_executor_environment}"
      } }
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "privacy_activation_executor" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "privacy-activation-executor"
  role   = aws_iam_role.privacy_activation_executor[0].id
  policy = jsonencode(local.privacy_activation_executor_policy)
}

resource "aws_iam_policy" "privacy_activation_admin_boundary" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "${local.privacy_activation_exchange_name}-administrator-boundary"
  policy = jsonencode(local.privacy_activation_admin_policy)
  tags   = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role" "privacy_activation_admin" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name                 = "${local.privacy_activation_exchange_name}-administrator"
  permissions_boundary = aws_iam_policy.privacy_activation_admin_boundary[0].arn
  max_session_duration = 3600
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "ExactAdministratorEnvironment"
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.privacy_activation_github_oidc_provider_arn }
      Condition = { StringEquals = {
        "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        "token.actions.githubusercontent.com:sub" = "repo:${var.github_org}/${var.github_repo}:environment:${local.privacy_activation_admin_environment}"
      } }
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "privacy_activation_admin" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "privacy-activation-administrator"
  role   = aws_iam_role.privacy_activation_admin[0].id
  policy = jsonencode(local.privacy_activation_admin_policy)
}

resource "aws_iam_policy" "privacy_activation_coordinator_boundary" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "${local.privacy_activation_exchange_name}-coordinator-boundary"
  policy = jsonencode(local.privacy_activation_coordinator_policy)
  tags   = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role" "privacy_activation_coordinator" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name                 = "${local.privacy_activation_exchange_name}-coordinator"
  permissions_boundary = aws_iam_policy.privacy_activation_coordinator_boundary[0].arn
  max_session_duration = 3600
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "ExactProductionEnvironment"
      Effect    = "Allow"
      Action    = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = var.privacy_activation_github_oidc_provider_arn }
      Condition = { StringEquals = {
        "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        "token.actions.githubusercontent.com:sub" = "repo:${var.github_org}/${var.github_repo}:environment:${var.github_environment}"
      } }
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "privacy_activation_coordinator" {
  count = var.privacy_activation_exchange_enabled ? 1 : 0

  name   = "privacy-activation-coordinator"
  role   = aws_iam_role.privacy_activation_coordinator[0].id
  policy = jsonencode(local.privacy_activation_coordinator_policy)
}

output "privacy_activation_exchange_bucket_name" {
  description = "Private, Object-Locked, two-day approval exchange bucket."
  value       = try(aws_s3_bucket.privacy_activation_exchange[0].bucket, null)
}

output "privacy_activation_exchange_kms_key_arn" {
  value = try(aws_kms_key.privacy_activation_exchange[0].arn, null)
}

output "privacy_activation_executor_signing_key_arn" {
  value = try(aws_kms_key.privacy_activation_executor_signing[0].arn, null)
}

output "privacy_activation_administrator_signing_key_arn" {
  value = try(aws_kms_key.privacy_activation_admin_signing[0].arn, null)
}

output "privacy_activation_courier_user_name" {
  description = "Host courier identity. Terraform deliberately creates no access key."
  value       = try(aws_iam_user.privacy_activation_courier[0].name, null)
}

output "privacy_activation_executor_role_arn" {
  value = try(aws_iam_role.privacy_activation_executor[0].arn, null)
}

output "privacy_activation_administrator_role_arn" {
  value = try(aws_iam_role.privacy_activation_admin[0].arn, null)
}

output "privacy_activation_coordinator_role_arn" {
  value = try(aws_iam_role.privacy_activation_coordinator[0].arn, null)
}
