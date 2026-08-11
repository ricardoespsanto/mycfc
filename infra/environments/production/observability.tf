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

output "deployment_log_group_name" {
  value = aws_cloudwatch_log_group.deployment.name
}

output "deployment_alert_topic_arn" {
  value = aws_sns_topic.deployment_alerts.arn
}
