# Task: AWS Infrastructure as Code (Terraform)

Generate production Terraform with strict network isolation and zero-trust IAM policies.

1. **State & Secrets**:
   * Define S3/DynamoDB remote state backend.
   * Provision AWS SSM Parameter Store resources for sensitive variables.

2. **Network, Data & Compute**:
   * Provision a VPC (public/private subnets).
   * Provision an RDS PostgreSQL 16 instance (`db.t3.micro`) inside **private subnets**.
   * Provision an App Runner service (with ECR repository).
   * Provision an `aws_apprunner_vpc_connector` attached to the private subnets so App Runner can reach RDS.

3. **DNS (`mycfc.pt`)**:
   * Use `aws_apprunner_custom_domain_association` (no standalone ACM cert). Map outputted CNAMEs via `aws_route53_record`.

4. **CI/CD IAM & Migrations**:
   * Provision `aws_iam_openid_connect_provider` for GitHub. 
   * **Security:** The OIDC IAM Role Trust Policy MUST restrict the `StringLike` condition `sub` claim explicitly to `repo:<YOUR_GITHUB_ORG>/<YOUR_REPO_NAME>:*`. Do not leave this open.
   * Provision an AWS Lambda function inside the private VPC containing the `goose` binary. Grant the Lambda execution role permissions to read the DB credentials from SSM. The GitHub Actions IAM Role will invoke this Lambda to run migrations.
