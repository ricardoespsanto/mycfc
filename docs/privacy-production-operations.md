# Privacy production operations through GitHub

Issue #274 uses a disabled-by-default, outbound-only GitHub-to-host control path. It does not provide SSH, a generic shell, free-form arguments, or secret transport. Every request is approved in the protected `production` environment, bound to the exact signed and CI-green `main` commit and active attested image, expires within ten minutes, and is executed once by the root-owned host agent.

## Request and evidence binding

The workflow publishes the canonical `mycfc/privacy-production-operation-request/v2` document as an immutable, attested `privacy-op-<run>-<attempt>` ECR image. The document contains only the fixed operation code, release identity, timestamps, workflow identity and one `evidence_sha256`. Operations without evidence require 64 zeroes. Evidence-bearing operations require the exact lowercase SHA-256 reviewed at dispatch.

The host reads evidence only from fixed root-owned mode-`0600` paths. It never accepts an Actions-provided path or file content. Evidence-set manifests bind every staged activation file by SHA-256 before the existing cryptographic verifier or activation broker is invoked. Credentials remain in their existing protected host files and are never copied to GitHub, the request image, a receipt, stdout, or CloudWatch.

The host independently requires the active `GIT_SHA` and immutable `MYCFC_IMAGE`, a root-owned mode-`0600` environment and request, the production release lock, a one-operation lock, exact JSON keys, the compiled allowlist, and a new request digest. The ECR agent verifies the request image's label and GitHub provenance before extraction. Rejected requests require a new protected dispatch.

## Independent host gates

All gates default to `false`:

```dotenv
PRIVACY_PRODUCTION_OPERATIONS_ENABLED=false
PRIVACY_PRODUCTION_CREDENTIAL_OPERATIONS_ENABLED=false
PRIVACY_PRODUCTION_DESTRUCTIVE_OPERATIONS_ENABLED=false
PRIVACY_PRODUCTION_ACTIVATION_OPERATIONS_ENABLED=false
```

The general gate enables polling. Credential operations additionally require the credential gate. Irreversible deletion, retention execution, enablement and other live state changes require the destructive gate. Evidence recording, approval preparation, activation, acceptance runs, and enablement of request/worker configuration also require the activation gate. Emergency reductions (`activation-disable`, `worker-disable`, `flags-disable`, and `retention-disable`) remain available with the general gate so a stuck maintenance window cannot block rollback. An operation in more than one class must pass every applicable gate.

Keep the credential, destructive, and activation gates false during bundle installation and the initial `status` and `preflight` checks. Enable a gate only for the approved maintenance window, then return it to false and rerun the installer. Changing a workflow choice cannot create a host command that is absent from the compiled state machine.

## Fixed operations

| Phase | Operations | Required evidence SHA-256 | Effect |
| --- | --- | --- | --- |
| Baseline | `status`, `preflight` | zero | Observe service state; preflight rejects active privacy maintenance. |
| 1 | `infrastructure-observe` | `/etc/mycfc/privacy-production-operations/infrastructure.json` | Validate the reviewed infrastructure artifact without applying Terraform or reading state. |
| 1 | `policy-import` | canonical policy SHA-256 `98d80915d8911b296768eddb278934cfb6c0f49c731bd50b14358756c07a163e` | Import only the canonical policy through the existing strict service path and the staged active-administrator UUID. |
| 1 | `activation-disable-provision`, `retention-*`, `acceptance-*` credential modes | zero | Invoke the fixed, isolated credential lifecycle. Revoke also requires the destructive gate. |
| 2 | `legacy-inventory` | zero | Run the bounded signed purge image in dry-run mode and retain a protected aggregate evidence file. |
| 2 | `legacy-purge` | approved inventory digest | Run the bounded irreversible purge only for the approved inventory digest. |
| 2 | `legacy-verify` | zero | Repeat inventory and require zero versions and delete markers. |
| 2 | `legacy-credential-remove` | teardown artifact | Require proof that Terraform removed the IAM identity and access keys, then remove the local credential copy. It cannot substitute for the Terraform teardown. |
| 3 | `backup-run`, `backup-posture`, `backup-cleanup-inventory`, `restore-run`, `restore-verify` | zero | Invoke the existing authenticated backup, bounded dry-run cleanup, isolated restore and attestation verifiers. |
| 3 | `retention-run`, `retention-enable`, `retention-disable` | zero | Run retention once, or manage its timer through the exact configuration editor. Run and enable require the destructive gate. |
| 4 | `activation-record` | evidence-set manifest | Bind, verify, and record the four current activation artifacts. |
| 4 | `activation-courier-provision`, `activation-courier-rotate`, `activation-courier-revoke` | zero | Manage only the fixed exchange courier key through the already installed release-agent profile. Terraform never creates or stores the courier key. Revoke also requires the destructive gate. |
| 4 | `activation-ceremony-open` | evidence-set manifest | Prepare and immutably publish one approval material version valid for at most 15 minutes. The collector automatically verifies and activates after both independent approvals arrive. Requires both destructive and activation gates. |
| 4 | `acceptance-run` | zero | Exercise the real request, executor, object/provider, tombstone, completion and cleanup paths with a generated synthetic fixture; retain separately verified signed evidence only on the host. |
| 4 | `acceptance-canary-retry`, `acceptance-canary-failure`, `acceptance-canary-aged`, `acceptance-canary-heartbeat`, `acceptance-canary-recovery` | zero | Exercise one fixed synthetic operational condition. The host accepts only the mode's exact aggregate events and signed outcome. GitHub separately requires a fresh isolated canary `ALARM` followed by `OK`. |
| Rollback | `activation-disable` | zero | Engage the isolated database kill switch through the disable-only credential. |
| 5 | `flags-enable`, `worker-enable` | zero | Atomically update only the three reviewed flags, recreate and health-check the active app, verify worker readiness, and start the service. Both protected live-operation gates are required. |
| Rollback | `worker-disable`, `flags-disable` | zero | Stop the worker first, then disable all three flags and recreate and health-check the active app. |

`acceptance-provision`, `acceptance-rotate`, and `acceptance-revoke` manage only the isolated `mycfc_privacy_acceptance` login. Runtime modes accept no person, request, execution or fixture identifier. The runner generates a synthetic fixture inside PostgreSQL, exercises the real executor and completion code, cleans it up, and emits only fixed aggregate events plus a signed `mycfc/privacy-synthetic-acceptance/v1` envelope. The host wrapper rejects unexpected lines, wrong mode/outcome/conditions, a payload-digest mismatch, an untrusted key ID or an invalid Ed25519 signature. It stores the envelope as root-owned mode `0600` under `/var/lib/mycfc/privacy-operations/acceptance`; the envelope is never copied into Actions or CloudWatch.

Runtime modes require these root-owned mode-`0600` files under `/etc/mycfc/privacy-acceptance`: `login-database-url`, `app-database-url`, `executor-database-url`, `signing-private.key`, `signing-public.key`, `signing-key-id`, `tombstone-public.key`, `tombstone-locator.key`, and `aws-credentials`. The private signing key is mounted only into the one-shot runner. The public key and separate key-ID trust file stay on the host so the wrapper does not trust the runner's self-asserted key. `/etc/mycfc/privacy-acceptance.env` supplies the reviewed database, application role, schema digest, signing key ID, tombstone key IDs, broker function and AWS region. The wrapper overrides the image digest from the active immutable `MYCFC_IMAGE` before execution and requires that exact digest in the signed payload. Its AWS profile must be authorized only for the exact ledger broker invocation used by the acceptance fixture.

The four adverse event kinds (`retry`, `failure`, `aged`, and `heartbeat_missing`) feed only `MyCFC/PrivacyCanary/PrivacyAcceptanceCanaryAlarm` from the deployment log group. The recovery event feeds the separate `PrivacyAcceptanceCanaryRecovery` metric. Neither event family enters ordinary privacy-worker alarms. After the privacy-safe host receipt, GitHub's read-only observer polls the dedicated alarm's state-update history and requires an `ALARM` transition after this dispatch followed by a later `OK` transition. Reading transition history prevents a short-lived transition from being missed while the host operation is still running. A signed runner envelope proves the synthetic database outcome; only this independent observation proves the CloudWatch transition.

The policy importer requires these root-owned mode-`0600` files:

- `/etc/mycfc/privacy-production-operations/policy-operator`, containing only the active administrator UUID;
- `/etc/mycfc/privacy-production-operations/club-2026-09-15-v1.json`, with the exact canonical digest.

Activation evidence manifests use contract `mycfc/privacy-activation-evidence-set/v1` and a `files` object binding `restore-attestation.json`, `infrastructure.json`, `provider-registry.json`, `schema-inventory.json`, `restore-attestation.key`, and `artifact-public.key`. The dual-signer ceremony has no GitHub-provided approval bundle or manual staging route. The host writes material, the two protected signer workflows write their own fixed S3 object, and the collector downloads exact immutable versions, verifies the complete bundle offline, then invokes the database broker. See [`privacy-activation-exchange.md`](privacy-activation-exchange.md).

## Privacy-safe receipts

Each request receives one atomic root-owned mode-`0600` receipt under `/var/lib/mycfc/privacy-operations/receipts`. It contains the fixed request and operation identifiers, source and image binding, request and evidence SHA-256 values, result, an allowlisted reason, timestamps, and service-state booleans. It never contains credentials, database URLs, Terraform state, object keys, member/case identifiers, raw provider responses, approval payloads, or command output. GitHub looks only for the exact fresh success or rejection lifecycle marker in the deployment log group. A canary dispatch additionally observes only the fixed dedicated alarm name and state timestamps; it does not read the signed envelope or database evidence.

Phase outputs remain on the host in protected evidence directories. Copying them into another trust boundary requires a separate reviewed operation. The workflow summary intentionally reports only the operation identity and success state.

## Rollout and rollback

Install the reviewed bundle with every operations gate false. Set only the general gate true, install again, then dispatch `status` and `preflight` for the exact active SHA and image. Review every phase's source diff, host inputs and Terraform plan before temporarily opening its additional gate.

Rollback order is fixed: dispatch `activation-disable`, dispatch `worker-disable`, then dispatch `flags-disable`. Confirm readiness is blocked and the service is inactive before disabling infrastructure capabilities. Preserve evidence, queues, tombstones, immutable logs and failure state. Deleted media and erased identifiers are never reconstructed directly; recovery uses the authenticated restore-and-replay route.
