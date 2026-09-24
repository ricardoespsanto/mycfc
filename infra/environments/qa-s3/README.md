# Isolated QA S3 identity stack (source only)

This root is separate from `production` and uses the fixed state key
`mycfc/qa-s3/terraform.tfstate`. It declares two bounded GitHub OIDC roles and
their permissions-boundary policies. It creates **no test bucket** and changes
no application storage. Do not add this root to the production Terraform
workflow: that stack still contains intentionally retired privacy resources.

The `github-qa-s3-runner` trust accepts only the GitHub OIDC subject
`repo:ricardoespsanto/mycfc:environment:qa-s3` with audience
`sts.amazonaws.com`. Its policy permits bucket creation, private configuration,
versioned object tests and complete cleanup only under the account- and
region-specific `mycfc-qa-s3-<account>-<region>-*` namespace. It has no IAM,
KMS, Terraform-state, production-bucket or role-chaining grants. The separate
`github-qa-s3-janitor` trust accepts only the `main` branch OIDC subject and
cannot create buckets or read test objects. Its only account-wide permission is
`ListAllMyBuckets`, which exposes bucket **names and creation dates** so it can
find interrupted runs; all further reads and every mutation are restricted to
the QA namespace. S3 does not resource-scope `ListAllMyBuckets`. This metadata
visibility is the explicit tradeoff for avoiding a persistent bucket registry
or Lambda/EventBridge infrastructure.

## Provisioning gate

The OIDC provider must already exist in the current AWS account. **Before
applying the runner role**, create and verify a GitHub `qa-s3` environment with
required human reviewers, administrator bypass disabled, and deployment
branches restricted to `main`. The OIDC subject names only the environment,
not its branch or reviewer settings; an unprotected or nonexistent environment
would let a workflow claim the role without the intended approval gate. Do not
apply this stack until these GitHub settings are verified.

Review this root and its policy JSON first. Use a separately approved administrator session
to plan and apply this root; do not use either new QA role to manage its own
permissions. Set `backend.hcl` from the example with the existing state bucket,
and `terraform.tfvars` with the exact same-account OIDC provider ARN. Keep both
local files untracked. With the repository-pinned Terraform version:

```sh
terraform -chdir=infra/environments/qa-s3 init -backend-config=backend.hcl
terraform -chdir=infra/environments/qa-s3 plan -var-file=terraform.tfvars -out=qa-s3.tfplan
# Stop for human review of the exact plan and separate live-change approval.
terraform -chdir=infra/environments/qa-s3 apply qa-s3.tfplan
```

Those commands are an operator procedure, not an authorization to run them.
Keep the saved plan, backend settings and state off PR artifacts and logs. A
reviewer must confirm the plan affects only this QA root, creates the two roles,
two matching boundary policies and attachments, and cannot modify the existing
OIDC provider or production resources. After approval and apply, verify both
role trust policies and permission boundaries in AWS before enabling test runs.
Because these roles share the production account, also inspect the live
production bucket, state bucket and KMS resource policies for grants to QA role
sessions or wildcard principals. AWS notes that some resource-based grants to
role sessions can bypass an implicit permissions-boundary deny; if such a grant
exists, do not enable QA runs until an explicit deny or policy correction has
been reviewed.

## Later CI migration contract (not enabled by this stack)

1. Keep the protected `qa-s3` GitHub environment configured as a prerequisite.
   Store the QA runner role ARN and region as environment variables, not secrets.
   Never expose this role to ordinary `pull_request`, `pull_request_target` or
   unreviewed fork code. A protected dispatch may test an explicitly reviewed
   immutable PR SHA, but the approved workflow code must come from `main`.
2. Use one globally unique bucket per approved job under the exact output
   prefix, with a run ID/attempt/random suffix. Tag `Project=mycfc` and
   `Purpose=qa-s3`, set full public-access block, bucket-owner-enforced
   ownership, SSE-S3 and versioning. After first enabling versioning on a new
   bucket, wait 15 minutes before object tests per AWS propagation guidance.
   Existing tests mutate bucket-wide versioning and cannot share a bucket.
3. Migrate only the asset-storage tests, leaving local development independent
   of AWS. The job must clean up in an `always()` step: abort multipart uploads,
   paginate all object versions **and delete markers**, delete each by version
   ID, then delete the bucket. A failed cleanup must fail the job and surface the
   bucket name without exposing object contents.
4. Schedule a separate main-branch janitor using its own role. It must match
   the exact output prefix and the complete run-ID/attempt/random name format,
   validate the account/region, reject any conflicting QA tags, and use S3
   `CreationDate` to act only on buckets older than a conservative threshold
   (at least 24 hours). Missing tags alone cannot exclude a bucket: a canceled
   job may stop between creation and tagging. It must follow the same complete
   version/delete-marker/multipart cleanup, report failures, and never act on
   a bucket that fails the name, age or tag checks. This independent run covers
   cancellation and lost-runner cases. Ordinary PR CI must not receive either
   role; it can remain credential-free until the approved test migration exists.

The Terraform root does not install either workflow, create the protected
environment, dispatch tests, or touch live AWS. Those are separate approval
and implementation gates.
