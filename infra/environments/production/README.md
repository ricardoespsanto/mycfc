# Retained AWS Terraform

The ECS/RDS/ALB runtime was retired. This module manages only the private repair-photo bucket and immutable ECR repository. PostgreSQL backup storage and the Hetzner host are managed by `../hetzner`.

Use the existing remote backend and run `terraform plan` before applying a retained-resource change. Do not restore retired runtime resources from this module.
