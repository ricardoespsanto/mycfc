# Privacy production operations through GitHub

This is the disabled-by-default transport foundation for the remaining production work in issue #274. It provides no generic shell, SSH session, workflow-provided path, or free-form argument. The first release exposes only the read-only `status` operation and a quiescence-checking `preflight` operation. Publishing a request does not enable privacy features, provision a credential, delete data, or activate the worker.

## Transport and trust boundary

The protected **Privacy production operation** workflow accepts an allowlisted operation, the exact current `main` SHA, and the exact active application image by digest. Before publishing anything, it requires:

- dispatch from that exact current `main` commit;
- GitHub's valid commit signature and a successful canonical main CI run;
- a `production` environment approval;
- the existing GitHub OIDC deploy role rather than an AWS access key;
- an immutable `mycfc-production@sha256:...` application image whose revision label and GitHub provenance bind it to that commit and `deploy.yml`.

The workflow creates a canonical request with no payload field. It expires after ten minutes, packages only that JSON document into an immutable `privacy-op-<run-id>-<attempt>` ECR image, and adds GitHub provenance bound to this workflow and source commit. The operation tag is unique and the production ECR repository is immutable.

The production host never accepts an inbound workflow connection. Its root-owned timer uses the existing read-only ECR release profile to select the oldest unprocessed operation image. Before reading the request, the agent verifies the immutable digest, exact contract label, source-revision label and GitHub provenance, including rejection of self-hosted provenance. The root wrapper then independently requires:

- a root-owned mode-`0600` host environment and request file;
- exact JSON keys and types under `mycfc/privacy-production-operation-request/v1`;
- a request identity equal to the GitHub run and attempt;
- issue and expiry times with a maximum 15-minute lifetime;
- the exact source SHA and image to match the active `GIT_SHA` and `MYCFC_IMAGE` on the host;
- a compiled operation allowlist, the existing production-release lock, a one-at-a-time operation lock and replay record;
- independent credential and destructive gates for those operation classes.

Unknown operations, extra JSON fields, arbitrary paths, stale requests, another active release, another image, another commit, insecure files, replays with an invalid receipt, and concurrent operations fail closed. The agent processes each immutable request digest once; retrying an unsuccessful operation requires a new approved workflow dispatch.

## Host gates

The installer installs the agent service and timer but keeps them disabled with:

```dotenv
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=false
PRIVACY_PRODUCTION_CREDENTIAL_OPERATIONS_ENABLED=false
PRIVACY_PRODUCTION_DESTRUCTIVE_OPERATIONS_ENABLED=false
```

The general gate permits polling only after a separate host-configuration approval and bundle installation. It does not enable either protected operation class. A future credential operation must be present as an exact state-machine case and also requires the credential gate. A future destructive operation must be present as an exact state-machine case and also requires the destructive gate. Adding a workflow choice, root-side case, source tests and human-reviewed rollout remains mandatory; changing a string in the dispatch form cannot invent a command.

The root allowlist reserves `retention-provision`, `retention-rotate`, and `retention-revoke` around the dedicated privacy-retention credential contract. They pass only the fixed mode to `privacy-retention.sh`. Provision and rotate require the credential gate. Revoke requires both credential and destructive gates. It also reserves `acceptance-provision`, `acceptance-rotate`, `acceptance-revoke`, and `acceptance-run` for the fixed `mycfc_privacy_acceptance` synthetic-test lifecycle. The three credential transitions use the credential gate, revoke also uses the destructive gate, and all four fail closed until the dedicated acceptance wrapper is integrated. These operations are absent from the workflow choices until their CLIs, protected URL files, rollout and tests are integrated and reviewed.

## Privacy-safe receipts

The wrapper writes one atomic root-owned mode-`0600` receipt under `/var/lib/mycfc/privacy-operations/receipts`. It contains only:

- the fixed contract, request ID and operation code;
- source SHA, immutable image, and request SHA-256;
- result and allowlisted reason code;
- start and finish timestamps;
- booleans for the worker, retention, restore and backup-cleanup services.

It never contains request payloads, database URLs, credentials, object keys, member or case identifiers, Terraform state, raw provider responses, shell output or stack traces. The existing CloudWatch forwarding wrapper sends the fixed lifecycle lines and receipt outcome to `/mycfc/production/deployment`. The workflow assumes the read-only operations-observer role and accepts only a fresh exact success or rejection marker for its request ID; it never prints the raw log event.

## Installation and initial verification

Source delivery alone does not change the host. A later separately approved bundle installation must place the scripts and units under `/opt/mycfc/deployment`, retain the existing release credential ownership, and set only the general gate to `true`. Leave both protected-class gates false. Then run `status` followed by `preflight` from the Actions UI for the active image and current main SHA. Verify the exact CloudWatch receipts and confirm that no privacy service, feature flag, credential or database state changed.

If polling or receipt observation fails, keep every phase-specific operation blocked. Inspect the local systemd journal and the fixed CloudWatch reason code without copying raw files or secrets into GitHub. Disable the timer with `PRIVACY_PRODUCTION_OPERATIONS_ENABLED=false` and rerun the installer to stop future polling. Already processed request digests and receipts remain as non-identifying operational evidence.

## Remaining implementation boundary

This foundation does not yet publish the deployment bundle to the host automatically, add the later phase-specific workflow choices, provision secret material, run the media purge, run a restore drill, create activation artifacts, collect two signatures, activate the database, enable the worker, or run synthetic acceptance. Each later action needs its own exact request contract, state transition, preconditions, privacy-safe receipt, tests, human approval and rollback.

The source ECR lifecycle policy expires `privacy-op-` request images after seven days without changing the existing release, purge, or untagged rules. Apply that reviewed Terraform change before enabling the timer in production; source changes alone do not alter ECR.
