data "aws_iam_policy_document" "ecs_tasks_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "random_password" "app_db" {
  length  = 32
  special = false
}

resource "random_bytes" "csrf_auth_key" {
  length = 32
}

resource "aws_secretsmanager_secret" "app_db_password" {
  name = "${local.name}/app-db-password"
}

resource "aws_secretsmanager_secret_version" "app_db_password" {
  secret_id     = aws_secretsmanager_secret.app_db_password.id
  secret_string = random_password.app_db.result
}

resource "aws_secretsmanager_secret" "csrf_auth_key" {
  name = "${local.name}/csrf-auth-key"
}

resource "aws_secretsmanager_secret_version" "csrf_auth_key" {
  secret_id     = aws_secretsmanager_secret.csrf_auth_key.id
  secret_string = random_bytes.csrf_auth_key.base64
}

resource "aws_iam_role" "app_execution" {
  name               = "${local.name}-app-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

resource "aws_iam_role" "migrate_execution" {
  name               = "${local.name}-migrate-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

resource "aws_iam_role" "app_task" {
  name               = "${local.name}-app-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

resource "aws_iam_role" "migrate_task" {
  name               = "${local.name}-migrate-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

data "aws_iam_policy_document" "app_execution" {
  statement {
    sid       = "GetECRAuthorizationToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid       = "PullApplicationImage"
    effect    = "Allow"
    actions   = ["ecr:BatchCheckLayerAvailability", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"]
    resources = [aws_ecr_repository.app.arn]
  }

  statement {
    sid       = "WriteApplicationLogs"
    effect    = "Allow"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.app.arn}:*"]
  }

  statement {
    sid       = "ReadApplicationSecrets"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.app_db_password.arn, aws_secretsmanager_secret.csrf_auth_key.arn]
  }
}

resource "aws_iam_role_policy" "app_execution" {
  name   = "runtime"
  role   = aws_iam_role.app_execution.name
  policy = data.aws_iam_policy_document.app_execution.json
}

data "aws_iam_policy_document" "migrate_execution" {
  statement {
    sid       = "GetECRAuthorizationToken"
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid       = "PullApplicationImage"
    effect    = "Allow"
    actions   = ["ecr:BatchCheckLayerAvailability", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"]
    resources = [aws_ecr_repository.app.arn]
  }

  statement {
    sid       = "WriteMigrationLogs"
    effect    = "Allow"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.app.arn}:*"]
  }

  statement {
    sid       = "ReadMigrationSecret"
    effect    = "Allow"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [var.migration_db_password_secret_arn]
  }
}

resource "aws_iam_role_policy" "migrate_execution" {
  name   = "runtime"
  role   = aws_iam_role.migrate_execution.name
  policy = data.aws_iam_policy_document.migrate_execution.json
}

data "aws_iam_policy_document" "app_task" {
  statement {
    sid       = "ListRepairObjects"
    effect    = "Allow"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.repairs.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["repairs/*"]
    }
  }

  statement {
    sid       = "ManageRepairObjects"
    effect    = "Allow"
    actions   = ["s3:DeleteObject", "s3:GetObject", "s3:PutObject"]
    resources = ["${aws_s3_bucket.repairs.arn}/repairs/*"]
  }
}

resource "aws_iam_role_policy" "app_task" {
  name   = "repair-objects"
  role   = aws_iam_role.app_task.name
  policy = data.aws_iam_policy_document.app_task.json
}
