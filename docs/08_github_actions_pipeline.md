# Task: GitOps Pipeline and True Local Dev Parity

1. **GitHub Actions (`deploy.yml`)**:
   * Authenticate via OIDC (`aws-actions/configure-aws-credentials@v4`).
   * **Migrations:** Use AWS CLI to synchronously invoke the Terraform-provisioned VPC Lambda function to run `goose up` against the private RDS. Fail the pipeline if the Lambda fails.
   * Build/Push Docker image to ECR, trigger App Runner update.

2. **Local Dev Environment**:
   * **`docker-compose.yml`**: Define `postgres:16-alpine` and `minio/minio` (S3 emulator). Enforce `TZ=Europe/Lisbon` on the Postgres container.
   * **`Makefile`**:
     * `make dev-infra`: Starts compose stack.
     * `make migrate`: Runs local goose.
     * `make dev`: Uses `air` (github.com/cosmtrek/air) to concurrently watch and recompile `templ`, run `sqlc`, and hot-reload the Go binary. Do not use standard `go run` for the dev command.
