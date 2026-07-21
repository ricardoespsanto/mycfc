# Task: AWS Infrastructure (Terraform) & GitOps Pipeline

Generate the infrastructure as code and CI/CD pipelines.

1. **Dockerfile**: Multi-stage build running `templ generate` and `sqlc generate`.
2. **Terraform**: 
   * Provision an AWS RDS PostgreSQL instance.
   * Provision an AWS S3 Bucket for media storage with strict private access policies.
   * Provision AWS ECR and AWS App Runner. Assign an IAM Task Role to App Runner granting it `s3:PutObject` access to the bucket.
   * Provision AWS Route53 records and an ACM SSL certificate for the custom domain.
   * Inject `DATABASE_URL`, `CSRF_SECRET`, `SESSION_SECRET`, and `S3_BUCKET_NAME` into App Runner.
3. **GitHub Actions**: 
   * Authenticate via OIDC.
   * Run `goose up` directly against the AWS RDS instance to apply database migrations *before* deploying.
   * Build/push the Docker image to ECR, then trigger the App Runner update.
