resource "aws_cloudwatch_log_group" "deployment" {
  name              = "/${var.project_name}/${var.environment}/deployment"
  retention_in_days = 30

  lifecycle {
    prevent_destroy = true
  }
}

output "deployment_log_group_name" {
  value = aws_cloudwatch_log_group.deployment.name
}
