output "vpc_id" {
  value = aws_vpc.this.id
}

output "repair_bucket_name" {
  value = aws_s3_bucket.repairs.bucket
}

output "ecr_repository_url" {
  value = aws_ecr_repository.app.repository_url
}

output "rds_endpoint" {
  value = aws_db_instance.postgres.address
}

output "alb_dns_name" {
  value = aws_lb.app.dns_name
}

output "domain_name" {
  value = var.domain_name
}

output "app_private_subnet_ids" {
  value = values(aws_subnet.app)[*].id
}

output "task_security_group_id" {
  value = aws_security_group.task.id
}

output "alb_target_group_arn" {
  value = aws_lb_target_group.app.arn
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.app.name
}

output "ecs_service_name" {
  value = aws_ecs_service.app.name
}

output "app_task_definition_arn" {
  value = aws_ecs_task_definition.app.arn
}

output "migration_task_definition_arn" {
  value = aws_ecs_task_definition.migrate.arn
}

output "bootstrap_task_definition_arn" {
  value = aws_ecs_task_definition.bootstrap.arn
}

output "github_infra_plan_role_arn" {
  value = aws_iam_role.github_infra_plan.arn
}

output "github_infra_apply_role_arn" {
  value = aws_iam_role.github_infra_apply.arn
}

output "github_deploy_role_arn" {
  value = aws_iam_role.github_deploy.arn
}
