data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  name          = "${var.project_name}-${var.environment}"
  repair_bucket = "${local.name}-${data.aws_caller_identity.current.account_id}-${data.aws_region.current.region}-repairs"
  fargate_task_memory_by_cpu = {
    "256"  = [512, 1024, 2048]
    "512"  = [1024, 2048, 3072, 4096]
    "1024" = [2048, 3072, 4096, 5120, 6144, 7168, 8192]
    "2048" = [for memory in range(4096, 16385, 1024) : memory]
    "4096" = [for memory in range(8192, 30721, 1024) : memory]
  }
  tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "terraform"
    Repository  = "ricardoespsanto/mycfc"
  }
}

resource "aws_s3_bucket" "repairs" {
  bucket = local.repair_bucket

  lifecycle { prevent_destroy = true }
}

resource "aws_s3_bucket_public_access_block" "repairs" {
  bucket                  = aws_s3_bucket.repairs.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "repairs" {
  bucket = aws_s3_bucket.repairs.id
  rule { object_ownership = "BucketOwnerEnforced" }
}

resource "aws_s3_bucket_versioning" "repairs" {
  bucket = aws_s3_bucket.repairs.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "repairs" {
  bucket = aws_s3_bucket.repairs.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "repairs" {
  bucket = aws_s3_bucket.repairs.id
  rule {
    id     = "expire-noncurrent-repair-images"
    status = "Enabled"
    abort_incomplete_multipart_upload { days_after_initiation = 7 }
    noncurrent_version_expiration { noncurrent_days = 90 }
  }
}

data "aws_iam_policy_document" "repairs_bucket" {
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.repairs.arn, "${aws_s3_bucket.repairs.arn}/*"]
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

resource "aws_s3_bucket_policy" "repairs" {
  bucket = aws_s3_bucket.repairs.id
  policy = data.aws_iam_policy_document.repairs_bucket.json
}

resource "aws_ecr_repository" "app" {
  name                 = local.name
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
  encryption_configuration { encryption_type = "AES256" }
}

resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name
  policy = jsonencode({ rules = [
    { rulePriority = 1, description = "Keep 30 release images", selection = { tagStatus = "tagged", tagPrefixList = ["release-"], countType = "imageCountMoreThan", countNumber = 30 }, action = { type = "expire" } },
    { rulePriority = 2, description = "Expire untagged images after seven days", selection = { tagStatus = "untagged", countType = "sinceImagePushed", countUnit = "days", countNumber = 7 }, action = { type = "expire" } }
  ] })
}

output "repair_bucket_name" { value = aws_s3_bucket.repairs.bucket }
output "ecr_repository_url" { value = aws_ecr_repository.app.repository_url }
