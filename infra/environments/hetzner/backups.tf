data "aws_caller_identity" "current" {}

data "aws_region" "current" {}

locals {
  backup_bucket = "${local.name}-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.region}-postgres-backups"
}

resource "aws_kms_key" "postgres_backups" {
  description             = "Envelope encryption for ${local.name} PostgreSQL backups"
  enable_key_rotation     = true
  deletion_window_in_days = 30
}

resource "aws_kms_alias" "postgres_backups" {
  name          = "alias/${local.name}-postgres-backups"
  target_key_id = aws_kms_key.postgres_backups.key_id
}

resource "aws_s3_bucket" "postgres_backups" {
  bucket = local.backup_bucket

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "postgres_backups" {
  bucket                  = aws_s3_bucket.postgres_backups.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_versioning" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.postgres_backups.arn
      sse_algorithm     = "aws:kms"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id

  rule {
    id     = "retain-daily-recovery-points"
    status = "Enabled"

    filter { prefix = "daily/" }

    expiration { days = 30 }
  }

  rule {
    id     = "retain-monthly-recovery-points"
    status = "Enabled"

    filter { prefix = "monthly/" }

    expiration { days = 365 }
  }
}

data "aws_iam_policy_document" "postgres_backups_bucket" {
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.postgres_backups.arn, "${aws_s3_bucket.postgres_backups.arn}/*"]

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
}

resource "aws_s3_bucket_policy" "postgres_backups" {
  bucket = aws_s3_bucket.postgres_backups.id
  policy = data.aws_iam_policy_document.postgres_backups_bucket.json
}

resource "aws_iam_user" "postgres_backups" {
  name = "${local.name}-postgres-backups"
}

data "aws_iam_policy_document" "postgres_backups" {
  statement {
    sid       = "ListBackupObjects"
    effect    = "Allow"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.postgres_backups.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["daily/*", "monthly/*"]
    }
  }

  statement {
    sid       = "ReadAndWriteBackupObjects"
    effect    = "Allow"
    actions   = ["s3:GetObject", "s3:PutObject"]
    resources = ["${aws_s3_bucket.postgres_backups.arn}/daily/*", "${aws_s3_bucket.postgres_backups.arn}/monthly/*"]
  }

  statement {
    sid       = "EnvelopeEncryption"
    effect    = "Allow"
    actions   = ["kms:Decrypt", "kms:GenerateDataKey"]
    resources = [aws_kms_key.postgres_backups.arn]
  }
}

resource "aws_iam_user_policy" "postgres_backups" {
  name   = "postgres-backups"
  user   = aws_iam_user.postgres_backups.name
  policy = data.aws_iam_policy_document.postgres_backups.json
}

output "postgres_backup_bucket" {
  value = aws_s3_bucket.postgres_backups.bucket
}

output "postgres_backup_kms_key_arn" {
  value = aws_kms_key.postgres_backups.arn
}
