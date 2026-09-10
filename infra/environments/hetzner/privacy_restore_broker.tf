locals {
  privacy_restore_broker_object_actions = [
    "s3:GetObject",
    "s3:GetObjectVersion",
    "s3:PutObject",
  ]
  privacy_restore_broker_retention_actions = [
    "s3:GetObjectRetention",
    "s3:PutObjectRetention",
  ]
}

data "archive_file" "privacy_restore_broker" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  type        = "zip"
  source_file = "${path.module}/privacy_ledger_broker/handler.py"
  output_path = "${path.module}/.terraform/privacy-ledger-broker.zip"
}

data "aws_iam_policy_document" "privacy_restore_broker_boundary" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  statement {
    effect  = "Allow"
    actions = local.privacy_restore_broker_object_actions
    resources = [
      "${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}intent/*",
      "${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_prefix}closure/*",
    ]
  }

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_broker_retention_actions
    resources = ["${aws_s3_bucket.privacy_restore_ledger[0].arn}/${local.privacy_restore_closure_prefix}*"]
  }

  statement {
    effect    = "Allow"
    actions   = local.privacy_restore_broker_kms_actions
    resources = [aws_kms_key.privacy_restore_ledger[0].arn]
  }

  statement {
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.privacy_restore_broker[0].arn}:*"]
  }
}

resource "aws_iam_policy" "privacy_restore_broker_boundary" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  name        = "${local.privacy_restore_broker_name}-boundary"
  description = "Maximum one-shot ledger append and privacy-safe logging permissions"
  policy      = data.aws_iam_policy_document.privacy_restore_broker_boundary[0].json

  lifecycle { prevent_destroy = true }
}

resource "aws_iam_role" "privacy_restore_broker" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  name = local.privacy_restore_broker_name
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
  permissions_boundary = aws_iam_policy.privacy_restore_broker_boundary[0].arn
}

resource "aws_iam_role_policy" "privacy_restore_broker" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  name   = "privacy-ledger-append"
  role   = aws_iam_role.privacy_restore_broker[0].id
  policy = data.aws_iam_policy_document.privacy_restore_broker_boundary[0].json
}

resource "aws_cloudwatch_log_group" "privacy_restore_broker" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  name              = "/aws/lambda/${local.privacy_restore_broker_name}"
  retention_in_days = 90
}

resource "aws_lambda_function" "privacy_restore_broker" {
  count = var.privacy_restore_infrastructure_enabled ? 1 : 0

  function_name                  = local.privacy_restore_broker_name
  description                    = "Validates, conditionally appends and exactly verifies privacy restore ledger ciphertext"
  filename                       = data.archive_file.privacy_restore_broker[0].output_path
  source_code_hash               = data.archive_file.privacy_restore_broker[0].output_base64sha256
  role                           = aws_iam_role.privacy_restore_broker[0].arn
  handler                        = "handler.handler"
  runtime                        = "python3.13"
  timeout                        = 15
  memory_size                    = 128
  reserved_concurrent_executions = 1

  environment {
    variables = {
      LEDGER_BUCKET      = aws_s3_bucket.privacy_restore_ledger[0].bucket
      LEDGER_KMS_KEY_ARN = aws_kms_key.privacy_restore_ledger[0].arn
      LEDGER_PREFIX      = local.privacy_restore_prefix
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.privacy_restore_broker,
    aws_iam_role_policy.privacy_restore_broker,
    aws_s3_bucket_policy.privacy_restore_ledger,
  ]
}
