# Terraform state bootstrap

Run this stack once with local state to create the production state bucket:

```sh
terraform init
terraform apply -var-file=terraform.tfvars
```

The bucket has versioning, SSE-S3, TLS-only access, 90-day non-current-version retention, and deletion protection. It intentionally does not create a DynamoDB table: Terraform's S3 backend uses native locking.

Do not commit `terraform.tfvars` or local state. After bootstrap, copy the production backend example to `backend.hcl`, set its bucket and region, and initialize the production stack with `terraform init -backend-config=backend.hcl`.
