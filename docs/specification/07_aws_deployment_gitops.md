# 07 — AWS production infrastructure as code

## 1. Objective

Provision a deployable, secure and observable AWS production environment using Terraform. The target is ECS Fargate behind ALB/WAF, with RDS PostgreSQL, private S3, private task networking, ECR, Route 53, ACM, VPC endpoints, least-privilege IAM and GitHub OIDC.

AWS App Runner MUST NOT be provisioned. It is unavailable to new AWS customers after 31 March 2026.

## 2. Terraform structure and state

```text
infra/bootstrap/                    # state bucket; initially local state
infra/environments/production/      # production stack using S3 backend
```

### Bootstrap

Create a globally unique S3 state bucket with:

- Versioning enabled.
- Default SSE-S3 or customer-managed KMS encryption.
- Public-access block and bucket-owner-enforced ownership.
- TLS-only bucket policy.
- Lifecycle retaining non-current state versions for at least 90 days.
- Deletion protection through `prevent_destroy = true`.

Do not create DynamoDB locking. Production backend uses S3 native lock file:

```hcl
backend "s3" {
  key          = "mycfc/production/terraform.tfstate"
  use_lockfile = true
  encrypt      = true
}
```

Backend bucket/region are supplied through partial backend configuration, not hardcoded secrets. Commit `.terraform.lock.hcl` and constrain Terraform/provider versions.

## 3. Network

Use three availability zones when the selected region supports them.

- VPC CIDR default `10.42.0.0/16`.
- Three public ALB subnets, one per AZ.
- Three private application subnets, one per AZ.
- Three isolated database subnets, one per AZ.
- Internet Gateway and public route tables only for ALB subnets.
- ECS tasks receive no public IP.
- No NAT Gateway. Private AWS access is through VPC endpoints.

VPC endpoints:

- Gateway: S3 attached to application route tables.
- Interface with private DNS: ECR API, ECR DKR, CloudWatch Logs, SSM, Secrets Manager, KMS, STS, ECS, ECS Agent and ECS Telemetry where region supports them.
- Endpoint security group allows TCP 443 only from application subnets/task SG.
- Endpoint policies restrict ECR repositories, S3 bucket and secret/parameter ARNs where the service supports policy restrictions.

## 4. Security groups

- ALB SG: inbound 80/443 from IPv4 and IPv6 internet; outbound only to ECS task SG on container port 8080.
- ECS task SG: inbound 8080 only from ALB SG; outbound 5432 to RDS SG, 443 to endpoint SG, and 443 to S3 prefix list. No unrestricted `0.0.0.0/0` egress.
- RDS SG: inbound 5432 only from ECS task SG and migration task SG if separate.
- Interface endpoint SG: inbound 443 only from ECS task/migration SG.

Use security-group references rather than CIDRs where possible.

## 5. DNS, TLS, ALB and WAF

- Request an ACM public certificate for `domain_name` and `www.domain_name` in the deployment region.
- Validate through Route 53 DNS records managed by Terraform.
- Public ALB spans all public subnets.
- Listener 80 redirects permanently to HTTPS 443.
- Listener 443 uses the ACM certificate, modern AWS recommended TLS security policy, and forwards to an IP target group on port 8080.
- Route 53 A and AAAA alias records point apex and `www` to ALB.
- Application middleware redirects `www` to canonical apex with 308.
- Target health path `/health/ready` with thresholds specified in file 02.
- Enable ALB deletion protection and access logs to a dedicated private log bucket with lifecycle retention.

Attach AWS WAF v2 Web ACL with:

- AWS managed common rule set.
- Known-bad-inputs rule set.
- Amazon IP reputation rule set.
- Rate-based rule for `/login` and `/registo` at a conservative per-IP threshold.
- Separate higher threshold for all requests.
- CloudWatch metrics and sampled requests enabled; do not log request bodies or sensitive headers.

## 6. ECR and image policy

- Private ECR repository with immutable tags.
- Scan on push enabled.
- AES-256 encryption.
- Lifecycle: retain last 30 tagged release images; expire untagged images after 7 days.
- ECS task definition uses image digest, never a mutable tag.

## 7. ECS Fargate

- ECS cluster with Container Insights enabled.
- Fargate Linux x86_64 unless the Docker build is explicitly changed everywhere to ARM64.
- Application task: 0.5 vCPU / 1 GiB default; configurable.
- Desired count 2 across AZs; autoscaling min 2, max 6 based on CPU 60% and ALB requests per target.
- Rolling deployment minimum healthy 100%, maximum 200%.
- Deployment circuit breaker enabled with automatic rollback.
- Enable execute-command only if audit/logging and IAM are configured; default false.
- Read-only root filesystem, non-root user, drop all Linux capabilities, no privileged mode.
- Container health check calls local `/health/live`.
- Stop timeout aligns with application shutdown timeout plus safety margin.
- CloudWatch log group retention 30 days, encrypted, with `prevent_destroy` optional via variable.

Terraform owns cluster, service and base task-definition shape. GitHub Actions registers image-specific task-definition revisions. ECS service has lifecycle ignore only for the task-definition revision and desired count managed by autoscaling; do not ignore network, IAM or security configuration.

## 8. Database

RDS PostgreSQL 16:

- Instance class default `db.t4g.micro`, configurable.
- Multi-AZ enabled.
- 20 GiB gp3 encrypted storage; autoscaling maximum 100 GiB.
- Private DB subnet group only; not publicly accessible.
- Backup retention 14 days; copy tags to snapshots.
- Deletion protection enabled; final snapshot required; skip-final-snapshot false.
- Auto minor-version upgrade enabled in defined maintenance window.
- Performance Insights enabled with 7-day retention if supported by class.
- Enhanced monitoring at 60 seconds with dedicated role.
- Parameter group sets timezone `Europe/Lisbon`, `log_min_duration_statement=1000`, and safe connection logging without statement/password leakage.
- Master credentials managed by RDS/Secrets Manager; application does not use the master account.

Create an application DB credential secret and a migration/admin credential secret. Migration bootstrap SQL creates/updates least-privileged application role. App runtime role gets only app credential; migration task gets migration credential. Secret values are marked sensitive and never output.

Coordinate DB capacity with ECS: `DB_MAX_CONNS=8` and max six tasks means at most 48 app connections plus migration/admin headroom. Terraform validates this against a documented `db_connection_budget` variable.

## 9. Application S3 bucket

- Private bucket with public-access block and bucket-owner-enforced ownership.
- Versioning enabled.
- Default SSE-S3; bucket policy denies unencrypted transport and non-approved principals.
- No CORS required because uploads/download-signing are server-side.
- Lifecycle aborts incomplete multipart uploads after 7 days and moves non-current versions to expiration according to a documented retention policy.
- Application task role: `PutObject`, `DeleteObject`, `GetObject` only under `repairs/*`, plus `ListBucket` restricted to prefix when required.
- No public ACL or website hosting.

## 10. Runtime secrets and configuration

Use Secrets Manager for DB credentials and CSRF key. Use SSM Parameter Store for non-secret but centrally managed configuration if desired. ECS task definition injects secrets by ARN and regular variables explicitly.

Never store a composed `DATABASE_URL` in Terraform output. The app may build it from injected `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, and `DB_PASSWORD`; update file 02 implementation to support this production form while retaining `DATABASE_URL` for local/test. Exactly one mode must validate as complete.

Task role and execution role are separate:

- Execution role: pull only the project ECR image, write only its log group, read only referenced startup secrets.
- Task role: S3 repair prefix and no infrastructure mutation.
- Migration task role: migration/admin secret access plus DB network; no S3 unless migration code demonstrably needs it.

## 11. GitHub OIDC roles

Create GitHub OIDC provider once per account when not already managed. Trust policy MUST require:

- Audience `sts.amazonaws.com`.
- Subject exactly `repo:<github_org>/<github_repo>:environment:<github_environment>`.
- No wildcard organisation/repository.

Separate roles:

1. `github-infra-plan`: read-only plus state read/lock for pull-request plans; no apply.
2. `github-infra-apply`: scoped permissions necessary for Terraform production stack and state.
3. `github-deploy`: ECR push, task-definition register/pass only approved roles, ECS run migration task, describe/wait/update only the named cluster/service, read needed Terraform outputs/state if chosen.

Apply permissions boundaries where the account supports them. All `iam:PassRole` resources and conditions are explicit.

## 12. Observability and alarms

Create dashboards/alarms for:

- ALB unhealthy hosts, 5xx rate, target response time.
- ECS service running task count below desired, CPU/memory high, deployment failure events.
- RDS CPU, free storage, connections, replica/multi-AZ events and database errors.
- WAF blocked-rate anomaly.
- S3 4xx/5xx if request metrics enabled.

Create EventBridge rule for failed ECS deployments and route to SNS. Optional email subscription remains pending until operator confirms it; Terraform must not claim confirmation.

## 13. Terraform quality gates

Mandatory:

- `terraform fmt -check -recursive`.
- `terraform validate`.
- `tflint` with AWS plugin.
- `checkov` or `tfsec` pinned in CI with documented narrow suppressions only.
- `terraform plan -detailed-exitcode` for PR.
- No secrets in plan artifacts uploaded to untrusted contexts.
- Resource names/tags include project/environment/managed-by/repository.
- Critical resources have `prevent_destroy` where operationally appropriate.

## 14. Acceptance criteria

- A fresh supported AWS account can deploy the stack without App Runner eligibility.
- Only ALB and public DNS are internet-facing.
- ECS tasks have no public IP and can pull ECR, emit logs, read required secrets, reach S3 and RDS without NAT.
- Internet cannot connect directly to task or RDS ports.
- S3 anonymous read fails.
- OIDC token from another repository, branch-only subject or environment is denied.
- Failed ECS deployment automatically rolls back.
- RDS deletion is blocked until protection is deliberately disabled and a final snapshot is selected.
- Terraform second apply produces no unexpected changes after CI has deployed a new task-definition revision.
