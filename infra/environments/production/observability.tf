resource "aws_cloudwatch_log_group" "deployment" {
  name              = "/${var.project_name}/${var.environment}/deployment"
  retention_in_days = 30

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_cloudwatch_log_metric_filter" "release_agent_failure" {
  name           = "${local.name}-release-agent-failure"
  pattern        = "%exit_status=[1-9][0-9]*%"
  log_group_name = aws_cloudwatch_log_group.deployment.name

  metric_transformation {
    name          = "ReleaseAgentFailure"
    namespace     = "MyCFC/Deployment"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_sns_topic" "deployment_alerts" {
  name = "${local.name}-deployment-alerts"
}

resource "aws_sns_topic_subscription" "deployment_alert_email" {
  count = var.alarm_email == null ? 0 : 1

  topic_arn = aws_sns_topic.deployment_alerts.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

resource "aws_cloudwatch_metric_alarm" "repeated_release_agent_failures" {
  alarm_name          = "${local.name}-repeated-release-agent-failures"
  alarm_description   = "The Hetzner release agent failed during at least two of the last three five-minute periods."
  namespace           = "MyCFC/Deployment"
  metric_name         = "ReleaseAgentFailure"
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 3
  datapoints_to_alarm = 2
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.deployment_alerts.arn]
  ok_actions          = [aws_sns_topic.deployment_alerts.arn]

  depends_on = [aws_cloudwatch_log_metric_filter.release_agent_failure]
}

resource "aws_cloudwatch_log_metric_filter" "backup_noncurrent_cleanup_failure" {
  name           = "${local.name}-backup-noncurrent-cleanup-failure"
  pattern        = "%backup_noncurrent_cleanup_delete_failed|backup_noncurrent_cleanup_verification_failed|backup_noncurrent_cleanup_sla_breached|backup_noncurrent_cleanup_failed%"
  log_group_name = aws_cloudwatch_log_group.deployment.name

  metric_transformation {
    name          = "BackupNoncurrentCleanupFailure"
    namespace     = "MyCFC/Privacy"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "backup_noncurrent_cleanup_failure" {
  alarm_name          = "${local.name}-backup-noncurrent-cleanup-failure"
  alarm_description   = "Exact-version PostgreSQL backup cleanup failed verification or exceeded the approved 24-hour maximum."
  namespace           = "MyCFC/Privacy"
  metric_name         = "BackupNoncurrentCleanupFailure"
  statistic           = "Sum"
  period              = 60
  evaluation_periods  = 1
  datapoints_to_alarm = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.deployment_alerts.arn]
  ok_actions          = [aws_sns_topic.deployment_alerts.arn]

  depends_on = [aws_cloudwatch_log_metric_filter.backup_noncurrent_cleanup_failure]
}

resource "aws_cloudwatch_log_metric_filter" "privacy_restore_drill_failure" {
  name           = "${local.name}-privacy-restore-drill-failure"
  pattern        = "%privacy_restore_drill_failed|privacy_restore_promotion_gate_failed%"
  log_group_name = aws_cloudwatch_log_group.deployment.name

  metric_transformation {
    name          = "PrivacyRestoreDrillFailure"
    namespace     = "MyCFC/Privacy"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "privacy_restore_drill_failure" {
  alarm_name          = "${local.name}-privacy-restore-drill-failure"
  alarm_description   = "The isolated privacy restore drill or its authenticated promotion evidence failed."
  namespace           = "MyCFC/Privacy"
  metric_name         = "PrivacyRestoreDrillFailure"
  statistic           = "Sum"
  period              = 60
  evaluation_periods  = 1
  datapoints_to_alarm = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.deployment_alerts.arn]
  ok_actions          = [aws_sns_topic.deployment_alerts.arn]

  depends_on = [aws_cloudwatch_log_metric_filter.privacy_restore_drill_failure]
}

resource "aws_cloudwatch_log_metric_filter" "privacy_retention_failure" {
  name           = "${local.name}-privacy-retention-failure"
  pattern        = "%privacy_retention_sla_breach|privacy_retention_backlog_breach|privacy_retention_failed%"
  log_group_name = aws_cloudwatch_log_group.deployment.name

  metric_transformation {
    name          = "PrivacyRetentionFailure"
    namespace     = "MyCFC/Privacy"
    value         = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "privacy_retention_failure" {
  alarm_name          = "${local.name}-privacy-retention-failure"
  alarm_description   = "Bounded privacy retention maintenance failed, exceeded its backlog limit, or missed exact repair-object absence by day 30."
  namespace           = "MyCFC/Privacy"
  metric_name         = "PrivacyRetentionFailure"
  statistic           = "Sum"
  period              = 60
  evaluation_periods  = 1
  datapoints_to_alarm = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.deployment_alerts.arn]
  ok_actions          = [aws_sns_topic.deployment_alerts.arn]

  depends_on = [aws_cloudwatch_log_metric_filter.privacy_retention_failure]
}

output "deployment_log_group_name" {
  value = aws_cloudwatch_log_group.deployment.name
}

output "deployment_alert_topic_arn" {
  value = aws_sns_topic.deployment_alerts.arn
}
