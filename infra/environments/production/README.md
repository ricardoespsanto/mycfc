# Production Terraform

1. Apply `../../bootstrap` using its local state.
2. Copy `backend.hcl.example` to untracked `backend.hcl` and provide the state bucket and region.
3. Copy `terraform.tfvars.example` to untracked `terraform.tfvars`, replacing every account-specific value.
4. Run `terraform init -backend-config=backend.hcl`, then `terraform plan -var-file=terraform.tfvars`.

The Google Calendar browser key is not confidential, but it must be restricted in Google Cloud to the production origin and Google Calendar API. Terraform state contains sensitive generated secret values; restrict state-bucket access accordingly.
