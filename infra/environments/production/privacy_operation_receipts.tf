locals {
  privacy_operation_receipt_name        = "${local.name}-privacy-operation-receipts"
  privacy_operation_receipt_bucket_name = "${var.project_name}-${var.environment}-${data.aws_caller_identity.current.account_id}-operation-receipts"
  privacy_operation_receipt_bucket_arn  = try(aws_s3_bucket.privacy_operation_receipts[0].arn, "arn:aws:s3:::disabled")
  privacy_operation_receipt_key_arn     = try(aws_kms_key.privacy_operation_receipts[0].arn, "arn:aws:kms:disabled:000000000000:key/disabled")
}

resource "aws_kms_key" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  description             = "Encrypt signed MyCFC privacy production-operation receipts"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  key_usage               = "ENCRYPT_DECRYPT"
  tags                    = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_kms_alias" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  name          = "alias/${local.privacy_operation_receipt_name}"
  target_key_id = aws_kms_key.privacy_operation_receipts[0].key_id
}

resource "aws_s3_bucket" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket              = local.privacy_operation_receipt_bucket_name
  object_lock_enabled = true
  tags                = local.tags

  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket_public_access_block" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket                  = aws_s3_bucket.privacy_operation_receipts[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  rule { object_ownership = "BucketOwnerEnforced" }
}

resource "aws_s3_bucket_versioning" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  rule {
    bucket_key_enabled = true
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.privacy_operation_receipts[0].arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_object_lock_configuration" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  rule {
    default_retention {
      mode = "COMPLIANCE"
      days = 7
    }
  }

  depends_on = [aws_s3_bucket_versioning.privacy_operation_receipts]
}

resource "aws_s3_bucket_lifecycle_configuration" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  rule {
    id     = "expire-non-personal-operation-receipts"
    status = "Enabled"
    filter { prefix = "receipts/" }
    expiration { days = 90 }
    noncurrent_version_expiration { noncurrent_days = 30 }
    abort_incomplete_multipart_upload { days_after_initiation = 1 }
  }

  rule {
    id     = "expire-public-key-publications"
    status = "Enabled"
    filter { prefix = "public-keys/" }
    expiration { days = 90 }
    noncurrent_version_expiration { noncurrent_days = 30 }
    abort_incomplete_multipart_upload { days_after_initiation = 1 }
  }

  depends_on = [aws_s3_bucket_versioning.privacy_operation_receipts]
}

data "aws_iam_policy_document" "privacy_operation_receipts_bucket" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.privacy_operation_receipts[0].arn,
      "${aws_s3_bucket.privacy_operation_receipts[0].arn}/*",
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
    resources = ["${aws_s3_bucket.privacy_operation_receipts[0].arn}/*"]
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
    resources = ["${aws_s3_bucket.privacy_operation_receipts[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption-aws-kms-key-id"
      values   = [aws_kms_key.privacy_operation_receipts[0].arn]
    }
  }

  statement {
    sid       = "DenyOverwriteCapablePut"
    effect    = "Deny"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.privacy_operation_receipts[0].arn}/*"]
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

  statement {
    sid       = "DenyDeletion"
    effect    = "Deny"
    actions   = ["s3:DeleteObject", "s3:DeleteObjectVersion"]
    resources = ["${aws_s3_bucket.privacy_operation_receipts[0].arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
  }
}

resource "aws_s3_bucket_policy" "privacy_operation_receipts" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  bucket = aws_s3_bucket.privacy_operation_receipts[0].id
  policy = data.aws_iam_policy_document.privacy_operation_receipts_bucket[0].json
}

resource "aws_iam_user_policy" "privacy_operation_receipt_writer" {
  count = var.privacy_operation_receipts_enabled ? 1 : 0

  name = "privacy-operation-receipt-writer"
  user = aws_iam_user.release_agent.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CreateOnlySignedReceiptAndPublicKeyObjects"
        Effect = "Allow"
        Action = ["s3:PutObject"]
        Resource = [
          "${local.privacy_operation_receipt_bucket_arn}/receipts/*",
          "${local.privacy_operation_receipt_bucket_arn}/public-keys/*",
        ]
      },
      {
        Sid      = "UseOnlyReceiptEncryptionKey"
        Effect   = "Allow"
        Action   = ["kms:Encrypt", "kms:GenerateDataKey"]
        Resource = local.privacy_operation_receipt_key_arn
      },
      {
        Sid      = "DenyReceiptMutationAndReadback"
        Effect   = "Deny"
        Action   = ["s3:DeleteObject", "s3:DeleteObjectVersion", "s3:GetObject*", "s3:ListBucket*", "kms:Decrypt"]
        Resource = "*"
      },
    ]
  })
}

output "privacy_operation_receipt_bucket_name" {
  value = var.privacy_operation_receipts_enabled ? aws_s3_bucket.privacy_operation_receipts[0].bucket : null
}

output "privacy_operation_receipt_kms_key_arn" {
  value = var.privacy_operation_receipts_enabled ? aws_kms_key.privacy_operation_receipts[0].arn : null
}
